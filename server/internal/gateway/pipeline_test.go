package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/gateway/mqtt"
	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
	"github.com/taha2samy/quackquack/server/internal/store"
)

func steps(s string) *events.PipelineConfig {
	return &events.PipelineConfig{Version: 1, Steps: json.RawMessage(s)}
}

func lastValue(v string) ringEntry {
	return ringEntry{id: uuid.New(), at: time.Now(), msg: newLazyMessage(json.RawMessage(v))}
}

func publishedValue(t *testing.T, ev *events.Event) map[string]any {
	t.Helper()
	var m events.ElementMessage
	if err := ev.DecodeData(&m); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(m.Message, &v); err != nil {
		t.Fatalf("published %s: %v", m.Message, err)
	}
	return v
}

func TestGatewayPipelineCoreOrder(t *testing.T) {
	g, pub := testGateway(t)
	deadband := steps(`[{"kind":"deadband","field":"value","abs":1.0}]`)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Rate: rate(1), Burst: burst(1), Pipeline: deadband})

	res1, err := g.publishDeviceMessage(td.dev, "origin1", DeviceMessage{Element: "temp", ByName: true, Message: val(10), ClientID: "c-1"})
	if err != nil || res1.Status != StatusAccepted {
		t.Fatalf("first message: %s %v", res1.Status, err)
	}
	if evs := pub.events(); len(evs) != 1 || evs[0].QuackPipeline == nil || *evs[0].QuackPipeline != 1 {
		t.Fatalf("expected one event with quackpipeline=1, got %v", evs)
	}
	// the hub remembers the published value: 10.2 is within the deadband
	res2, err := g.publishDeviceMessage(td.dev, "origin1", DeviceMessage{Element: "temp", ByName: true,
		Message: json.RawMessage(`{"value":10.2}`), ClientID: "c-2"})
	if err != nil || res2.Status != StatusFiltered || pub.count() != 1 {
		t.Fatalf("expected filtered and nothing published, got %s %v (%d events)", res2.Status, err, pub.count())
	}
	retry, err := g.publishDeviceMessage(td.dev, "origin1", DeviceMessage{Element: "temp", ByName: true,
		Message: json.RawMessage(`{"value":10.2}`), ClientID: "c-2"})
	if err != nil || retry.Status != StatusDuplicate {
		t.Fatalf("a retry of a filtered message is a duplicate, got %s %v", retry.Status, err)
	}

	// filtered values don't use rate-limit tokens (burst 1)
	td2 := addDevice(t, g, events.ElementConfig{Name: "temp2", Rate: rate(1), Burst: burst(1), Pipeline: deadband})
	g.hub.Remember(td2.elem["temp2"], lastValue(`{"value":10.0}`))
	if r, err := g.publishDeviceMessage(td2.dev, "o", DeviceMessage{Element: "temp2", ByName: true, Message: json.RawMessage(`{"value":10.1}`)}); err != nil || r.Status != StatusFiltered {
		t.Fatalf("expected filtered, got %s %v", r.Status, err)
	}
	if r, err := g.publishDeviceMessage(td2.dev, "o", DeviceMessage{Element: "temp2", ByName: true, Message: val(15)}); err != nil || r.Status != StatusAccepted {
		t.Fatalf("the token must still be there, got %s %v", r.Status, err)
	}
}

func TestGatewayPipelineTransformAndDLQ(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp",
		Pipeline: &events.PipelineConfig{Version: 3, Steps: json.RawMessage(`[{"kind":"scale","mul":2.0,"add":5.0}]`)}})

	if r, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: val(10)}); err != nil || r.Status != StatusAccepted {
		t.Fatalf("%s %v", r.Status, err)
	}
	evs := pub.events()
	if ev := evs[len(evs)-1]; *ev.QuackPipeline != 3 || publishedValue(t, ev)["value"] != 25.0 {
		t.Fatalf("expected 25 with quackpipeline=3, got %v", publishedValue(t, ev))
	}

	// a failing step: an ErrPipeline, nothing published, a DLQ record that
	// is valid JSON even when the message is over 4 KiB
	long := `{"value":"not-a-number","pad":"` + strings.Repeat("x", 5000) + `"}`
	_, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: json.RawMessage(long)})
	var ep *elementpipe.ErrPipeline
	if !errors.As(err, &ep) || ep.Step != "scale" {
		t.Fatalf("want an ErrPipeline from scale, got %v", err)
	}
	if pub.count() != len(evs) {
		t.Fatal("a failed message must not be published")
	}
	recs := pub.records()
	if len(recs) != 1 || recs[0].Topic != events.TopicElementPipelineDLQ {
		t.Fatalf("want one DLQ record, got %d", len(recs))
	}
	var dlq events.ElementPipelineFailed
	if err := json.Unmarshal(recs[0].Value, &dlq); err != nil {
		t.Fatalf("DLQ record isn't valid JSON: %v", err)
	}
	if dlq.Step != "scale" || dlq.MessageSize != len(long) || len(dlq.Message) != 4096 || dlq.PipelineVersion != 3 {
		t.Fatalf("DLQ record: step=%s size=%d len=%d version=%d", dlq.Step, dlq.MessageSize, len(dlq.Message), dlq.PipelineVersion)
	}
}

// MQTT counts Done calls to acknowledge a message once: a failure is returned,
// not also reported through Done.
func TestPipelineFailureDoesNotCallDone(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Pipeline: steps(`[{"kind":"scale","mul":2}]`)})
	calls := 0
	_, err := g.publishDeviceMessage(td.dev, "mqtt/c", DeviceMessage{Element: "temp", ByName: true,
		Message: json.RawMessage(`{"value":"x"}`), Done: func(error) { calls++ }})
	if err == nil || calls != 0 {
		t.Fatalf("err=%v done calls=%d (want an error and no Done call)", err, calls)
	}
	// filtered messages are acknowledged through Done
	td2 := addDevice(t, g, events.ElementConfig{Name: "t2", Pipeline: steps(`[{"kind":"drop_if","op":"<","value":0}]`)})
	_, err = g.publishDeviceMessage(td2.dev, "mqtt/c", DeviceMessage{Element: "t2", ByName: true,
		Message: val(-1), Done: func(error) { calls++ }})
	if err != nil || calls != 1 {
		t.Fatalf("filtered: err=%v done calls=%d", err, calls)
	}
}

func TestPipelineWrapsBareValuesAndChecksOutput(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g,
		events.ElementConfig{Name: "temp", Pipeline: steps(`[{"kind":"scale","mul":2}]`)},
		events.ElementConfig{Name: "nul", Pipeline: steps(`[{"kind":"script","source":"function transform(m){ m.s = 'a' + String.fromCharCode(0) + 'b'; return m }"}]`)},
		events.ElementConfig{Name: "html", Pipeline: steps(`[{"kind":"scale","field":"n","mul":1}]`)},
	)
	if r, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: json.RawMessage(`21.5`)}); err != nil || r.Status != StatusAccepted {
		t.Fatalf("a bare number must run as {\"value\": x}: %s %v", r.Status, err)
	}
	evs := pub.events()
	if got := publishedValue(t, evs[len(evs)-1]); got["value"] != 43.0 {
		t.Fatalf("got %v", got)
	}
	_, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "nul", ByName: true, Message: val(1)})
	var ep *elementpipe.ErrPipeline
	if !errors.As(err, &ep) || ep.Step != "output" {
		t.Fatalf("an unstorable result must fail at output, got %v", err)
	}
	// untouched fields keep their values (a big integer stays exact)
	if _, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "html", ByName: true,
		Message: json.RawMessage(`{"n":1,"serial":9007199254740993,"html":"<b>&"}`)}); err != nil {
		t.Fatal(err)
	}
	evs = pub.events()
	var m events.ElementMessage
	_ = evs[len(evs)-1].DecodeData(&m)
	v, _ := decodeMessage(m.Message)
	if o := v.(map[string]any); o["serial"] != json.Number("9007199254740993") || o["html"] != "<b>&" {
		t.Fatalf("fields changed on the way: %s", m.Message)
	}
}

func TestPipelineThatDoesNotCompileFailsClosed(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Pipeline: steps(`[{"kind":"scale","mul":0}]`)})
	_, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: val(650)})
	var ep *elementpipe.ErrPipeline
	if !errors.As(err, &ep) || ep.Step != "compile" || pub.count() != 0 {
		t.Fatalf("a broken pipeline must not publish raw values: %v (%d events)", err, pub.count())
	}
	// with a working version known, a broken update keeps it
	td.cfg.Version = 2
	td.cfg.Elements[0].Pipeline = &events.PipelineConfig{Version: 2, Steps: json.RawMessage(`[{"kind":"scale","mul":2}]`)}
	td.apply(t, g)
	td.cfg.Version = 3
	td.cfg.Elements[0].Pipeline = &events.PipelineConfig{Version: 3, Steps: json.RawMessage(`[{"kind":"bogus"}]`)}
	td.apply(t, g)
	if r, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: val(10)}); err != nil || r.Status != StatusAccepted {
		t.Fatalf("%s %v", r.Status, err)
	}
	evs := pub.events()
	if *evs[len(evs)-1].QuackPipeline != 2 || publishedValue(t, evs[len(evs)-1])["value"] != 20.0 {
		t.Fatal("the last working version (2) must keep running")
	}
}

func TestGatewayPipelineRestAndGRPC(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Pipeline: steps(`[{"kind":"deadband","abs":2.0}]`)})
	g.hub.Remember(td.elem["temp"], lastValue(`{"value":50.0}`))
	srv := restServer(t, g, td)
	tok := td.token(t)
	resp := doREST(t, http.MethodPost, srv.URL+"/device/v1/messages", tok, "application/json", `[{"element":"temp","message":{"value":50.5}}]`)
	if rs := results(t, resp); resp.status != http.StatusOK || len(rs) != 1 || rs[0].Status != StatusFiltered {
		t.Fatalf("REST filtered: %d %+v", resp.status, rs)
	}

	td2 := addDevice(t, g, events.ElementConfig{Name: "scaled", Pipeline: steps(`[{"kind":"scale","mul":2.0}]`)})
	tok2 := td2.token(t)
	srv2 := restServer(t, g, td2)
	resp2 := doREST(t, http.MethodPost, srv2.URL+"/device/v1/messages", tok2, "application/json", `[{"element":"scaled","message":{"value":"not-a-number"}}]`)
	rs2 := results(t, resp2)
	if resp2.status != http.StatusOK || len(rs2) != 1 || rs2[0].Status != StatusPipelineFailed || !strings.Contains(rs2[0].Error, "scale") {
		t.Fatalf("REST pipeline_failed: %+v", rs2)
	}

	client := grpcClient(t, g)
	resG, err := client.Publish(context.Background(), authed(&devicev1.PublishRequest{
		Messages: []*devicev1.DeviceMessage{{Element: "temp", Message: value(t, map[string]any{"value": 50.5})}}}, tok))
	if err != nil || len(resG.Msg.Results) != 1 || resG.Msg.Results[0].Status != StatusFiltered {
		t.Fatalf("gRPC filtered: %v %+v", err, resG)
	}
	resF, err := client.Publish(context.Background(), authed(&devicev1.PublishRequest{
		Messages: []*devicev1.DeviceMessage{{Element: "scaled", Message: value(t, map[string]any{"value": "x"})}}}, tok2))
	if err != nil || len(resF.Msg.Results) != 1 || resF.Msg.Results[0].Status != StatusPipelineFailed {
		t.Fatalf("gRPC pipeline_failed: %v %+v", err, resF)
	}
}

func TestGatewayPipelineCommandInverse(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "motor", Pipeline: steps(`[{"kind":"scale","mul":2.0}]`)})
	userMsg := &events.ElementMessage{ElementID: td.elem["motor"], DeviceID: td.cfg.Device.ID, Source: events.SourceUser,
		Actor: events.Actor{ID: "1", Name: "alice"}, Message: json.RawMessage(`{"value":100}`)}

	// browsers keep engineering units (100), the device gets 100 / 2 = 50
	devFrame, brFrame := renderMessage(userMsg, events.Now(), g.pipelineOf(userMsg.DeviceID, userMsg.ElementID))
	var devObj, brObj struct {
		Message map[string]any `json:"message"`
	}
	_ = json.Unmarshal(devFrame(), &devObj)
	_ = json.Unmarshal(brFrame, &brObj)
	if brObj.Message["value"] != 100.0 || devObj.Message["value"] != 50.0 {
		t.Fatalf("browser %v, device %v", brObj.Message, devObj.Message)
	}

	var downlink map[string]any
	unreg := g.mqttCore().OnCommand(func(cmd mqtt.Command) { _ = json.Unmarshal(cmd.Message, &downlink) })
	defer unreg()
	g.notifyCommand(userMsg, uuid.New(), time.Now())
	if downlink["value"] != 50.0 {
		t.Fatalf("downlink got %v", downlink)
	}

	// a bare command value comes back bare
	bare := *userMsg
	bare.Message = json.RawMessage(`100`)
	devFrame, _ = renderMessage(&bare, events.Now(), g.pipelineOf(bare.DeviceID, bare.ElementID))
	if !strings.Contains(string(devFrame()), `"message":50`) {
		t.Fatalf("bare command: %s", devFrame())
	}
}

// A command the pipeline can't convert never reaches the device: refused at
// the browser, and (if it still arrives) not delivered or remembered.
func TestCommandThatCannotBeConvertedIsNotDelivered(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "fan", Pipeline: steps(`[{"kind":"script",
		"source":"function transform(m){return m}\nfunction untransform(m){ if (m.value > 100) throw new Error('too fast'); return m }"}]`)})
	el := td.elem["fan"]

	b := testBrowser(7, "b1")
	b.SetPerm(el, store.PermReadWrite)
	g.hub.Subscribe(b, elementInfo{ID: el, DeviceID: td.cfg.Device.ID}, []byte("c"), nil, true)
	b.drain()
	g.browserPublish(b, el.String(), json.RawMessage(`{"value":500}`))
	frames := b.drain()
	if pub.count() != 0 || len(frames) != 1 || !strings.Contains(string(frames[0]), "too fast") {
		t.Fatalf("want the command refused with the reason; published=%d frames=%s", pub.count(), frames)
	}
	g.browserPublish(b, el.String(), json.RawMessage(`{"value":50}`))
	if pub.count() != 1 {
		t.Fatal("a convertible command must go through")
	}

	// arriving from elsewhere (another gateway on another version): no frame
	cmd := &events.ElementMessage{ElementID: el, DeviceID: td.cfg.Device.ID, Source: events.SourceUser,
		Actor: events.Actor{ID: "7", Name: "u"}, Message: json.RawMessage(`{"value":500}`)}
	devFrame, _ := renderMessage(cmd, events.Now(), g.pipelineOf(cmd.DeviceID, cmd.ElementID))
	if devFrame() != nil {
		t.Fatal("the device must get nothing, not the unconverted command")
	}
	d := testDevice(td.cfg.Device.ID, "d1")
	g.hub.AddDevice(d, []elementInfo{{ID: el, DeviceID: td.cfg.Device.ID}})
	d.drain()
	g.hub.Deliver(cmd, uuid.New(), time.Now(), devFrame, []byte("br"), "")
	if got := d.drain(); len(got) != 0 {
		t.Fatalf("device got %s", got)
	}
	if cmds := g.hub.CommandsSince([]uuid.UUID{el}, nil); !strings.Contains(string(cmds[el].frame), `"value":50}`) {
		t.Fatalf("REST sync must keep the last convertible command, got %s", cmds[el].frame)
	}
	called := false
	unreg := g.mqttCore().OnCommand(func(mqtt.Command) { called = true })
	defer unreg()
	g.notifyCommand(cmd, uuid.New(), time.Now())
	if called {
		t.Fatal("no MQTT downlink for an unconvertible command")
	}
}

// A device's other connections get its readings in device units.
func TestDeviceReadingsReachSiblingsInDeviceUnits(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "gate", Pipeline: steps(`[{"kind":"map","table":{"OPEN":"ON","CLOSED":"OFF"}}]`)})
	el := td.elem["gate"]
	sibling := testDevice(td.cfg.Device.ID, "sib")
	g.hub.AddDevice(sibling, []elementInfo{{ID: el, DeviceID: td.cfg.Device.ID}})
	sibling.drain()
	if _, err := g.publishDeviceMessage(td.dev, "origin", DeviceMessage{Element: "gate", ByName: true, Message: json.RawMessage(`{"value":"CLOSED"}`)}); err != nil {
		t.Fatal(err)
	}
	got := sibling.drain()
	if len(got) != 1 || !strings.Contains(string(got[0]), `"value":"CLOSED"`) {
		t.Fatalf("sibling got %s", got)
	}
}

func TestCoalescedMessagesKeepPipelineVersionAndDeadband(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Rate: rate(200), Burst: burst(1), OverLimit: store.OverLimitLatest,
		Pipeline: &events.PipelineConfig{Version: 5, Steps: json.RawMessage(`[{"kind":"deadband","abs":1}]`)}})
	send := func(v string) string {
		r, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: json.RawMessage(v)})
		if err != nil {
			t.Fatal(err)
		}
		return r.Status
	}
	if s := send(`{"value":10}`); s != StatusAccepted {
		t.Fatal(s)
	}
	if s := send(`{"value":20}`); s != StatusCoalesced { // over the limit: held
		t.Fatal(s)
	}
	// compared with the held 20 (not the published 10): a real change, replaces it
	if s := send(`{"value":10.1}`); s != StatusCoalesced {
		t.Fatalf("want coalesced (replacing the held value), got %s", s)
	}
	deadline := time.Now().Add(2 * time.Second)
	for pub.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	evs := pub.events()
	if len(evs) != 2 {
		t.Fatalf("want the held message flushed, got %d events", len(evs))
	}
	if evs[1].QuackPipeline == nil || *evs[1].QuackPipeline != 5 || publishedValue(t, evs[1])["value"] != 10.1 {
		t.Fatalf("flushed %v (quackpipeline %v)", publishedValue(t, evs[1]), evs[1].QuackPipeline)
	}
}

func (g *Gateway) mqttCore() mqttCore {
	return mqttCore{g}
}

func TestGatewayPipelineLiveUpdate(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Pipeline: steps(`[{"kind":"scale","mul":2.0}]`)})
	if r, _ := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: val(10)}); r.Status != StatusAccepted {
		t.Fatal(r.Status)
	}
	td.cfg.Version = 2
	td.cfg.Elements[0].Pipeline = &events.PipelineConfig{Version: 2, Steps: json.RawMessage(`[{"kind":"scale","mul":3.0}]`)}
	td.apply(t, g)
	if r, _ := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: val(10)}); r.Status != StatusAccepted {
		t.Fatal(r.Status)
	}
	evs := pub.events()
	if ev := evs[len(evs)-1]; *ev.QuackPipeline != 2 || publishedValue(t, ev)["value"] != 30.0 {
		t.Fatalf("v2: %v", publishedValue(t, ev))
	}
}
