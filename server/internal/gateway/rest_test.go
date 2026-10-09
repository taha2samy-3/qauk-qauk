package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
)

// restServer serves the REST device routes of a test gateway. The device is
// pre-marked active so presence (database) isn't touched.
func restServer(t *testing.T, g *Gateway, td *testDev) *httptest.Server {
	t.Helper()
	g.rest.last[td.cfg.Device.ID] = restLease{seen: time.Now().Add(time.Hour)}
	mux := http.NewServeMux()
	mux.Handle("POST /device/v1/messages", g.RESTMessagesHandler())
	mux.Handle("GET /device/v1/sync", g.RESTSyncHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type restResp struct {
	status int
	header http.Header
	body   map[string]json.RawMessage
}

func doREST(t *testing.T, method, url, token, contentType, body string, hdr ...string) restResp {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := restResp{status: resp.StatusCode, header: resp.Header}
	_ = json.NewDecoder(resp.Body).Decode(&out.body)
	return out
}

func results(t *testing.T, r restResp) []restResult {
	t.Helper()
	var rs []restResult
	if err := json.Unmarshal(r.body["results"], &rs); err != nil {
		t.Fatalf("no results in %v", r.body)
	}
	return rs
}

func TestRESTPublishBatch(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp"}, events.ElementConfig{Name: "hum"})
	srv := restServer(t, g, td)
	tok := td.token(t)
	body := `[{"element":"temp","message":{"value":21.5},"id":"a1","ts":"2026-10-09T10:00:00Z"},
		{"element":"` + td.elem["hum"].String() + `","message":{"value":40}},
		{"element":"nope","message":{"value":1}},
		{"element":"temp"}]`
	r := doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "application/json", body)
	if r.status != 200 {
		t.Fatalf("status %d %v", r.status, r.body)
	}
	rs := results(t, r)
	want := []string{"accepted", "accepted", "rejected", "rejected"}
	codes := []string{"", "", "unknown_element", "invalid_message"}
	for i, res := range rs {
		if res.Status != want[i] || res.Code != codes[i] {
			t.Errorf("item %d: %+v", i, res)
		}
	}
	if rs[0].ElementID != td.elem["temp"].String() || rs[0].EventID == "" {
		t.Errorf("item 0 ids: %+v", rs[0])
	}
	msgs := pub.messages(t)
	if len(msgs) != 2 || msgs[0].ClientTS == nil || msgs[0].ClientTS.Format(time.RFC3339) != "2026-10-09T10:00:00Z" {
		t.Fatalf("published %+v", msgs)
	}
	// a retry of "a1" is acknowledged without publishing again
	r = doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "", `{"element":"temp","message":{"value":21.5},"id":"a1"}`)
	if again := results(t, r); again[0].Status != StatusDuplicate || again[0].EventID != rs[0].EventID {
		t.Fatalf("retry: %+v, first %+v", again, rs[0])
	}
	if pub.count() != 2 {
		t.Fatalf("retry published again (%d)", pub.count())
	}
}

func mustRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestRESTAuthAndRouting(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp"})
	srv := restServer(t, g, td)
	if r := doREST(t, "POST", srv.URL+"/device/v1/messages", "", "", `{}`); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := doREST(t, "POST", srv.URL+"/device/v1/messages", "x.y.z", "", `{}`); r.status != 401 {
		t.Fatalf("bad token: %d", r.status)
	}
	r := doREST(t, "POST", srv.URL+"/device/v1/messages", td.token(t), "", `{"element":"temp","message":1}`, DeviceHeader, uuid.NewString())
	if r.status != 403 {
		t.Fatalf("mismatched %s: %d", DeviceHeader, r.status)
	}
	r = doREST(t, "POST", srv.URL+"/device/v1/messages", td.token(t), "", `{"element":"temp","message":1}`, DeviceHeader, td.cfg.Device.ID.String())
	if r.status != 200 {
		t.Fatalf("matching header: %d", r.status)
	}
}

func TestRESTBodyLimits(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp"})
	srv := restServer(t, g, td)
	tok := td.token(t)
	big := `{"element":"temp","message":"` + strings.Repeat("x", restMaxBody) + `"}`
	if r := doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "", big); r.status != 413 {
		t.Fatalf("over 1 MiB: %d", r.status)
	}
	items := make([]string, restMaxItems+1)
	for i := range items {
		items[i] = `{"element":"temp","message":1}`
	}
	if r := doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "", "["+strings.Join(items, ",")+"]"); r.status != 422 {
		t.Fatalf("too many items: %d", r.status)
	}
	if r := doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "", `"nope"`); r.status != 422 {
		t.Fatalf("not an object/array: %d", r.status)
	}
}

func TestRESTRateLimitedBatchIs429(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "e", Rate: rate(1), Burst: burst(1)})
	srv := restServer(t, g, td)
	tok := td.token(t)
	if r := doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "", `{"element":"e","message":1}`); r.status != 200 {
		t.Fatalf("first: %d", r.status)
	}
	r := doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "", `{"element":"e","message":2}`)
	if r.status != 429 || r.header.Get("Retry-After") != "1" {
		t.Fatalf("over the element limit: %d Retry-After=%q", r.status, r.header.Get("Retry-After"))
	}
	g.cfg.DeviceMsgRate = 2
	r = doREST(t, "POST", srv.URL+"/device/v1/messages", tok, "", `[{"element":"e","message":1},{"element":"e","message":1},{"element":"e","message":1}]`)
	if r.status != 429 || !strings.Contains(string(r.body["detail"]), "device rate limit") {
		t.Fatalf("over the device guard: %d %v", r.status, r.body)
	}
	if s, _ := strconv.Atoi(r.header.Get("Retry-After")); s < 1 {
		t.Fatal("Retry-After missing")
	}
}

func TestRESTSenML(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "urn:dev:mac:0024befffe804ff1:temp"}, events.ElementConfig{Name: "urn:dev:mac:0024befffe804ff1:door"})
	srv := restServer(t, g, td)
	body := `[{"bn":"urn:dev:mac:0024befffe804ff1:","bt":1.796e9,"bu":"Cel","n":"temp","v":23.1},
		{"n":"temp","v":23.4,"t":10},
		{"n":"door","vb":true}]`
	r := doREST(t, "POST", srv.URL+"/device/v1/messages", td.token(t), "application/senml+json", body)
	if r.status != 200 {
		t.Fatalf("status %d %v", r.status, r.body)
	}
	msgs := pub.messages(t)
	if len(msgs) != 3 {
		t.Fatalf("published %d", len(msgs))
	}
	if string(msgs[0].Message) != `{"unit":"Cel","value":23.1}` || msgs[0].ClientTS.Unix() != 1796000000 {
		t.Errorf("record 0: %s at %v", msgs[0].Message, msgs[0].ClientTS)
	}
	if msgs[1].ClientTS.Unix() != 1796000010 {
		t.Errorf("record 1 time %v", msgs[1].ClientTS)
	}
	if string(msgs[2].Message) != `{"unit":"Cel","value":true}` || msgs[2].ElementID != td.elem["urn:dev:mac:0024befffe804ff1:door"] {
		t.Errorf("record 2: %s", msgs[2].Message)
	}
}

func TestParseSenMLRelativeTime(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	items, err := parseSenML([]byte(`[{"n":"a","v":1,"t":-5}]`), now)
	if err != nil || !items[0].TS.Equal(now.Add(-5*time.Second)) {
		t.Fatalf("relative time: %v %v", items, err)
	}
	if _, err := parseSenML([]byte(`[{"n":"a"}]`), now); err == nil {
		t.Fatal("a named record without a value is invalid")
	}
}

func TestRESTSyncLongPoll(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "led"}, events.ElementConfig{Name: "fan"})
	srv := restServer(t, g, td)
	tok := td.token(t)
	command := func(el uuid.UUID, v int) {
		m := msg(el, td.cfg.Device.ID, events.SourceUser, v)
		g.publishElement(m, "b1")
	}
	// nothing yet, no wait: empty answer
	r := doREST(t, "GET", srv.URL+"/device/v1/sync", tok, "", "")
	if r.status != 200 || string(r.body["messages"]) != "[]" {
		t.Fatalf("empty sync: %d %v", r.status, r.body)
	}
	// a waiting request returns as soon as a user command arrives
	go func() {
		time.Sleep(100 * time.Millisecond)
		command(td.elem["led"], 1)
	}()
	start := time.Now()
	r = doREST(t, "GET", srv.URL+"/device/v1/sync?wait=5s", tok, "", "")
	if took := time.Since(start); took > 2*time.Second || took < 90*time.Millisecond {
		t.Fatalf("long poll took %v", took)
	}
	var msgs []map[string]any
	_ = json.Unmarshal(r.body["messages"], &msgs)
	if len(msgs) != 1 || msgs[0]["element"] != "led" || msgs[0]["element_id"] != td.elem["led"].String() {
		t.Fatalf("sync messages %v", msgs)
	}
	var cursor string
	_ = json.Unmarshal(r.body["cursor"], &cursor)
	// the same cursor sees nothing new; device's own messages never come back
	g.publishElement(msg(td.elem["fan"], td.cfg.Device.ID, events.SourceDevice, 9), restOrigin)
	r = doREST(t, "GET", srv.URL+"/device/v1/sync?wait=0.2&cursor="+cursor, tok, "", "")
	if string(r.body["messages"]) != "[]" {
		t.Fatalf("expected nothing new, got %s", r.body["messages"])
	}
	// newest value per element wins
	command(td.elem["led"], 2)
	command(td.elem["led"], 3)
	command(td.elem["fan"], 4)
	r = doREST(t, "GET", srv.URL+"/device/v1/sync?cursor="+cursor, tok, "", "")
	_ = json.Unmarshal(r.body["messages"], &msgs)
	if len(msgs) != 2 || msgs[0]["element"] != "led" || string(mustRaw(msgs[0]["message"])) != `{"value":3}` || msgs[1]["element"] != "fan" {
		t.Fatalf("newest per element: %v", msgs)
	}
	if r := doREST(t, "GET", srv.URL+"/device/v1/sync?cursor=@@", tok, "", ""); r.status != 422 {
		t.Fatalf("bad cursor: %d", r.status)
	}
}
