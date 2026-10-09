package events_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/schemas"
)

func compile(t *testing.T) *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	entries, _ := schemas.FS.ReadDir(".")
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f, _ := schemas.FS.Open(e.Name())
		doc, err := jsonschema.UnmarshalJSON(f)
		if err != nil {
			t.Fatal(err)
		}
		id := doc.(map[string]any)["$id"].(string)
		if err := c.AddResource(id, doc); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func validate(t *testing.T, c *jsonschema.Compiler, ev *events.Event) {
	t.Helper()
	raw, _ := json.Marshal(ev)
	var doc any
	_ = json.Unmarshal(raw, &doc)
	env, err := c.Compile("urn:quack:schema:cloudevent")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Validate(doc); err != nil {
		t.Fatalf("envelope invalid: %v\n%s", err, raw)
	}
	data, err := c.Compile(ev.DataSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.Validate(doc.(map[string]any)["data"]); err != nil {
		t.Fatalf("data invalid for %s: %v\n%s", ev.Type, err, raw)
	}
}

func TestEventsMatchSchemas(t *testing.T) {
	c := compile(t)
	el, dev := uuid.New(), uuid.New()
	ts := events.Now()
	m, _ := events.New(events.TypeElementMessage, events.GatewaySource("gw-1"), el.String(), events.ElementMessage{
		ElementID: el, DeviceID: dev, Source: events.SourceDevice, Actor: events.Actor{ID: dev.String(), Name: "d"},
		Origin: events.Origin{GatewayID: "gw-1", ConnID: "c-1"}, Message: json.RawMessage(`{"value":1}`), ClientTS: &ts,
	})
	validate(t, c, m)
	uid := int64(3)
	ctrl, _ := events.Control(events.ControlChanged{Kind: events.KindPermission, Op: events.OpDelete, ID: "9", ElementID: &el, UserID: &uid})
	validate(t, c, ctrl)
	p, _ := events.New(events.TypeDevicePresence, events.GatewaySource("gw-1"), dev.String(), events.DevicePresence{DeviceID: dev, Connected: true, GatewayID: "gw-1"})
	validate(t, c, p)
	r, b := 2.5, 5
	dc, _ := events.New(events.TypeDeviceConfig, events.SourceAPI, dev.String(), events.DeviceConfig{
		Device: events.DeviceInfo{ID: dev, Name: "d"}, Version: 1,
		Key:      &events.DeviceKey{ID: uuid.New(), PEM: "-----BEGIN PUBLIC KEY-----", Algorithm: "ES256", Active: true},
		Elements: []events.ElementConfig{{ID: el, Name: "e", Points: 10, Rate: &r, Burst: &b, OverLimit: "latest"}, {ID: uuid.New(), Name: "f", OverLimit: "drop"}},
	})
	validate(t, c, dc)
	noKey, _ := events.New(events.TypeDeviceConfig, events.SourceAPI, dev.String(), events.DeviceConfig{
		Device: events.DeviceInfo{ID: dev, Name: "d"}, Elements: []events.ElementConfig{}})
	validate(t, c, noKey)
	if _, err := uuid.Parse(m.ID); err != nil || uuid.MustParse(m.ID).Version() != 7 {
		t.Fatalf("event id must be UUIDv7: %s", m.ID)
	}
}

func TestTimeFormat(t *testing.T) {
	b, _ := json.Marshal(events.Now())
	if len(b) != len(`"2026-10-07T10:00:00.123Z"`) || b[len(b)-2] != 'Z' {
		t.Fatalf("time format %s", b)
	}
}
