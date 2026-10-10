package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/gateway/mqtt"
	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
)

func TestGatewayPipelineCoreOrder(t *testing.T) {
	g, pub := testGateway(t)
	rateVal := 1.0
	burstVal := 1
	deadbandSteps := json.RawMessage(`[{"kind":"deadband","field":"value","abs":1.0}]`)

	td := addDevice(t, g, events.ElementConfig{
		Name:      "temp",
		Rate:      &rateVal,
		Burst:     &burstVal,
		Pipeline:  &events.PipelineConfig{Version: 1, Steps: deadbandSteps},
		OverLimit: "drop",
	})

	// 1. First message: filtered should not consume tokens if we test filtering before token
	// Let's seed hub.Latest so deadband has a previous value
	firstVal := json.RawMessage(`{"value":10.0}`)
	res1, err := g.publishDeviceMessage(td.dev, "origin1", DeviceMessage{
		Element:  "temp",
		ByName:   true,
		Message:  firstVal,
		ClientID: "c-1",
	})
	if err != nil || res1.Status != StatusAccepted {
		t.Fatalf("first message expected accepted, got status=%s err=%v", res1.Status, err)
	}
	evs := pub.events()
	if len(evs) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.QuackPipeline == nil || *ev.QuackPipeline != 1 {
		t.Fatalf("expected quackpipeline=1, got %v", ev.QuackPipeline)
	}

	// 2. Second message: delta is 0.2 < 1.0 -> filtered by deadband!
	filteredVal := json.RawMessage(`{"value":10.2}`)
	res2, err := g.publishDeviceMessage(td.dev, "origin1", DeviceMessage{
		Element:  "temp",
		ByName:   true,
		Message:  filteredVal,
		ClientID: "c-2",
	})
	if err != nil {
		t.Fatalf("filtered message should not return error, got %v", err)
	}
	if res2.Status != StatusFiltered {
		t.Fatalf("expected status=filtered, got %s", res2.Status)
	}
	// No new event published to bus
	if len(pub.events()) != 1 {
		t.Fatalf("filtered message must not be published to bus")
	}

	// 3. Retry of filtered message with same client ID is duplicate
	res2Retry, err := g.publishDeviceMessage(td.dev, "origin1", DeviceMessage{
		Element:  "temp",
		ByName:   true,
		Message:  filteredVal,
		ClientID: "c-2",
	})
	if err != nil || res2Retry.Status != StatusDuplicate {
		t.Fatalf("expected retry of filtered message to be duplicate, got status=%s err=%v", res2Retry.Status, err)
	}

	// 4. Rate-limit test: A fresh element with burst=1, send filtered message first
	td2 := addDevice(t, g, events.ElementConfig{
		Name:      "temp2",
		Rate:      &rateVal,
		Burst:     &burstVal,
		Pipeline:  &events.PipelineConfig{Version: 1, Steps: deadbandSteps},
		OverLimit: "drop",
	})
	// Set hub latest for temp2
	g.hub.Remember(td2.elem["temp2"], ringEntry{
		id:      uuid.New(),
		at:      time.Now(),
		message: map[string]any{"value": 10.0},
	})
	// Send filtered value: delta 0.1 < 1.0
	resFiltered, err := g.publishDeviceMessage(td2.dev, "origin1", DeviceMessage{
		Element: "temp2",
		ByName:  true,
		Message: json.RawMessage(`{"value":10.1}`),
	})
	if err != nil || resFiltered.Status != StatusFiltered {
		t.Fatalf("expected filtered, got status=%s err=%v", resFiltered.Status, err)
	}
	// Bucket should still have its token! Send passing value: delta 5.0 >= 1.0
	resPassing, err := g.publishDeviceMessage(td2.dev, "origin1", DeviceMessage{
		Element: "temp2",
		ByName:  true,
		Message: json.RawMessage(`{"value":15.0}`),
	})
	if err != nil || resPassing.Status != StatusAccepted {
		t.Fatalf("expected passing message to be accepted without rate limit, got status=%s err=%v", resPassing.Status, err)
	}
}

func TestGatewayPipelineTransformAndDLQ(t *testing.T) {
	g, pub := testGateway(t)
	scaleSteps := json.RawMessage(`[{"kind":"scale","field":"value","mul":2.0,"add":5.0}]`)

	td := addDevice(t, g, events.ElementConfig{
		Name:     "temp",
		Pipeline: &events.PipelineConfig{Version: 3, Steps: scaleSteps},
	})

	// Send valid number: 10 -> 10 * 2 + 5 = 25
	res, err := g.publishDeviceMessage(td.dev, "origin", DeviceMessage{
		Element: "temp",
		ByName:  true,
		Message: json.RawMessage(`{"value":10}`),
	})
	if err != nil || res.Status != StatusAccepted {
		t.Fatalf("expected accepted, got %s err=%v", res.Status, err)
	}
	evs := pub.events()
	if len(evs) == 0 {
		t.Fatal("expected published event")
	}
	lastEv := evs[len(evs)-1]
	if lastEv.QuackPipeline == nil || *lastEv.QuackPipeline != 3 {
		t.Fatalf("expected quackpipeline=3, got %v", lastEv.QuackPipeline)
	}
	var data events.ElementMessage
	if err := lastEv.DecodeData(&data); err != nil {
		t.Fatal(err)
	}
	var transformed map[string]any
	_ = json.Unmarshal(data.Message, &transformed)
	if v, ok := transformed["value"].(float64); !ok || v != 25.0 {
		t.Fatalf("expected transformed value 25, got %v", transformed["value"])
	}

	// Step failure: non-numeric value for numeric step scale
	resFail, errFail := g.publishDeviceMessage(td.dev, "origin", DeviceMessage{
		Element: "temp",
		ByName:  true,
		Message: json.RawMessage(`{"value":"not-a-number"}`),
	})
	if errFail == nil {
		t.Fatal("expected ErrPipeline on non-numeric value")
	}
	var ep *ErrPipeline
	if errFail == nil || errFail.Error() == "" {
		t.Fatalf("expected pipeline failure, got %v", resFail)
	}
	_ = ep
}

func TestGatewayPipelineRestAndGRPC(t *testing.T) {
	g, _ := testGateway(t)
	deadbandSteps := json.RawMessage(`[{"kind":"deadband","field":"value","abs":2.0}]`)
	td := addDevice(t, g, events.ElementConfig{
		Name:     "temp",
		Pipeline: &events.PipelineConfig{Version: 1, Steps: deadbandSteps},
	})
	// Warm hub latest with 50.0
	g.hub.Remember(td.elem["temp"], ringEntry{
		id:      uuid.New(),
		at:      time.Now(),
		message: map[string]any{"value": 50.0},
	})

	srv := restServer(t, g, td)
	tok := td.token(t)

	// REST publish with value 50.5 (delta 0.5 < 2.0) -> status "filtered"
	resp := doREST(t, http.MethodPost, srv.URL+"/device/v1/messages", tok, "application/json",
		`[{"element":"temp","message":{"value":50.5}}]`)
	if resp.status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.status)
	}
	rs := results(t, resp)
	if len(rs) != 1 || rs[0].Status != "filtered" {
		t.Fatalf("expected status=filtered on REST, got %+v", rs)
	}

	// REST publish with invalid string value for deadband (non-numbers pass through deadband, so let's test a step that fails)
	// Add element with scale step
	scaleSteps := json.RawMessage(`[{"kind":"scale","field":"value","mul":2.0}]`)
	td2 := addDevice(t, g, events.ElementConfig{
		Name:     "scaled",
		Pipeline: &events.PipelineConfig{Version: 1, Steps: scaleSteps},
	})
	tok2 := td2.token(t)
	srv2 := restServer(t, g, td2)
	resp2 := doREST(t, http.MethodPost, srv2.URL+"/device/v1/messages", tok2, "application/json",
		`[{"element":"scaled","message":{"value":"not-a-number"}}]`)
	if resp2.status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.status)
	}
	rs2 := results(t, resp2)
	if len(rs2) != 1 || rs2[0].Status != "pipeline_failed" || rs2[0].Code != "pipeline_failed" {
		t.Fatalf("expected status=pipeline_failed code=pipeline_failed on REST, got %+v", rs2)
	}

	// gRPC tests
	client := grpcClient(t, g)
	// Filtered on gRPC
	resG, err := client.Publish(context.Background(), authed(&devicev1.PublishRequest{
		Messages: []*devicev1.DeviceMessage{{
			Element: "temp",
			Message: value(t, map[string]any{"value": 50.5}),
		}},
	}, tok))
	if err != nil {
		t.Fatal(err)
	}
	if len(resG.Msg.Results) != 1 || resG.Msg.Results[0].Status != "filtered" {
		t.Fatalf("expected gRPC status=filtered, got %+v", resG.Msg.Results)
	}

	// pipeline_failed on gRPC
	resFailG, err := client.Publish(context.Background(), authed(&devicev1.PublishRequest{
		Messages: []*devicev1.DeviceMessage{{
			Element: "scaled",
			Message: value(t, map[string]any{"value": "not-a-number"}),
		}},
	}, tok2))
	if err != nil {
		t.Fatal(err)
	}
	if len(resFailG.Msg.Results) != 1 || resFailG.Msg.Results[0].Status != "pipeline_failed" || resFailG.Msg.Results[0].Code != "pipeline_failed" {
		t.Fatalf("expected gRPC status=pipeline_failed code=pipeline_failed, got %+v", resFailG.Msg.Results)
	}
}

func TestGatewayPipelineCommandInverse(t *testing.T) {
	g, _ := testGateway(t)
	scaleSteps := json.RawMessage(`[{"kind":"scale","field":"value","mul":2.0,"add":0.0}]`)

	td := addDevice(t, g, events.ElementConfig{
		Name:     "motor",
		Pipeline: &events.PipelineConfig{Version: 1, Steps: scaleSteps},
	})

	// User command (SourceUser)
	userMsg := &events.ElementMessage{
		ElementID: td.elem["motor"],
		DeviceID:  td.cfg.Device.ID,
		Source:    events.SourceUser,
		Actor:     events.Actor{ID: "1", Name: "alice"},
		Message:   json.RawMessage(`{"value":100}`),
	}

	// 1. renderMessage:
	// browser frame gets original engineering units: 100
	// device frame gets inverse: 100 / 2 = 50
	devFrame, brFrame := renderMessage(userMsg, events.Now(), td.dev.Elements[0].Pipeline)
	var devObj, brObj map[string]json.RawMessage
	_ = json.Unmarshal(devFrame, &devObj)
	_ = json.Unmarshal(brFrame, &brObj)

	var devMsgVal, brMsgVal map[string]any
	_ = json.Unmarshal(devObj["message"], &devMsgVal)
	_ = json.Unmarshal(brObj["message"], &brMsgVal)

	if brMsgVal["value"].(float64) != 100 {
		t.Fatalf("expected browser frame to keep engineering value 100, got %v", brMsgVal["value"])
	}
	if devMsgVal["value"].(float64) != 50 {
		t.Fatalf("expected device frame to have inverse value 50, got %v", devMsgVal["value"])
	}

	// 2. notifyCommand (MQTT downlink):
	downlinkCalled := false
	var downlinkMsg map[string]any
	unreg := g.mqttCore().OnCommand(func(cmd mqtt.Command) {
		downlinkCalled = true
		_ = json.Unmarshal(cmd.Message, &downlinkMsg)
	})
	defer unreg()

	g.notifyCommand(userMsg, uuid.New(), time.Now())
	if !downlinkCalled {
		t.Fatal("expected downlink hook called")
	}
	if downlinkMsg["value"].(float64) != 50 {
		t.Fatalf("expected downlink command to have inverse value 50, got %v", downlinkMsg["value"])
	}
}

func (g *Gateway) mqttCore() mqttCore {
	return mqttCore{g}
}

func TestGatewayPipelineLiveUpdate(t *testing.T) {
	g, pub := testGateway(t)
	pipeV1 := json.RawMessage(`[{"kind":"scale","field":"value","mul":2.0}]`)
	td := addDevice(t, g, events.ElementConfig{
		Name:     "temp",
		Pipeline: &events.PipelineConfig{Version: 1, Steps: pipeV1},
	})

	// Message under version 1
	res1, _ := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: json.RawMessage(`{"value":10}`)})
	if res1.Status != StatusAccepted {
		t.Fatalf("expected accepted, got %s", res1.Status)
	}
	evs1 := pub.events()
	ev1 := evs1[len(evs1)-1]
	if *ev1.QuackPipeline != 1 {
		t.Fatalf("expected quackpipeline=1, got %d", *ev1.QuackPipeline)
	}

	// Update device snapshot live to version 2 (mul: 3.0)
	pipeV2 := json.RawMessage(`[{"kind":"scale","field":"value","mul":3.0}]`)
	td.cfg.Version = 2
	td.cfg.Elements[0].Pipeline = &events.PipelineConfig{Version: 2, Steps: pipeV2}
	td.apply(t, g)

	res2, _ := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: json.RawMessage(`{"value":10}`)})
	if res2.Status != StatusAccepted {
		t.Fatalf("expected accepted, got %s", res2.Status)
	}
	evs2 := pub.events()
	ev2 := evs2[len(evs2)-1]
	if *ev2.QuackPipeline != 2 {
		t.Fatalf("expected quackpipeline=2, got %d", *ev2.QuackPipeline)
	}
	var data2 events.ElementMessage
	_ = ev2.DecodeData(&data2)
	var m2 map[string]any
	_ = json.Unmarshal(data2.Message, &m2)
	if m2["value"].(float64) != 30.0 {
		t.Fatalf("expected value 30 with v2 pipeline, got %v", m2["value"])
	}
}
