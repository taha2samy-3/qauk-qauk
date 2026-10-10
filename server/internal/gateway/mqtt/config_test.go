package mqtt

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func snapshot() events.MQTTConfig {
	dev := uuid.New()
	enc := "dec-enc"
	return events.MQTTConfig{
		Connection: events.MQTTConnection{ID: "c1", Name: "c", BrokerURL: "mqtt://b:1883", ClientIDPrefix: "q-c1", Replicas: 1, Enabled: true},
		Uplinks: []events.MQTTUplink{
			{ID: "u1", TopicFilter: "s/+/up", QoS: 1, Format: "json", Enabled: true, Device: json.RawMessage(`{"segment":1}`),
				FieldMap: json.RawMessage(`[{"element":"T","value":"t"}]`)},
			{ID: "u2", TopicFilter: "s/+/up", QoS: 0, Format: "json", Enabled: true, Device: json.RawMessage(`{"segment":1}`),
				FieldMap: json.RawMessage(`[{"element":"H","value":"h"}]`)},
			{ID: "u3", TopicFilter: "bad", Format: "json", Enabled: true, Device: json.RawMessage(`{}`), FieldMap: json.RawMessage(`[]`)},
			{ID: "u4", TopicFilter: "off/#", Format: "json", Enabled: false, Device: json.RawMessage(`{"segment":0}`),
				FieldMap: json.RawMessage(`[{"element":"X","value":"x"}]`)},
		},
		Downlinks: []events.MQTTDownlink{
			{ID: "d1", DeviceExternalID: "fan-1", Element: "Fan", TopicTemplate: "cmd/{device}", Encoder: json.RawMessage(`{"template":{"on":"{{value}}","raw":"{{message}}"}}`)},
			{ID: "d2", DeviceExternalID: "fan-1", Element: "Mode", TopicTemplate: "cmd/{device}/mode", Encoder: json.RawMessage(`{"decoder_id":"` + enc + `"}`)},
			{ID: "d3", DeviceExternalID: "fan-1", Element: "Raw", TopicTemplate: "cmd/{device}/raw"},
		},
		Decoders: []events.MQTTDecoder{{ID: enc, Name: "enc", Version: 1, Source: `function encodeDownlink(i) { return {bytes: [i.data.value]}; }`}},
		Devices:  []events.MQTTGrantedDevice{{DeviceID: dev, ExternalID: "fan-1"}},
		Version:  1,
	}
}

func TestCompileSnapshot(t *testing.T) {
	c := compile(snapshot(), quiet)
	if len(c.rules) != 2 {
		t.Fatalf("rules: %d (a broken rule and a disabled one must be skipped)", len(c.rules))
	}
	if len(c.errors) != 1 {
		t.Fatalf("the broken rule must be reported: %v", c.errors)
	}
	if subs := c.subscriptions(); len(subs) != 1 || subs["s/+/up"] != 1 {
		t.Fatalf("one subscription at the highest QoS: %v", subs)
	}
	if _, ok := c.grants["fan-1"]; !ok || len(c.byDevice) != 1 {
		t.Fatal("grants not indexed")
	}
	// a rule change keeps the connection hash (no reconnect); a broker change doesn't
	s2 := snapshot()
	s2.Uplinks = s2.Uplinks[:1]
	if compile(s2, quiet).connHash != c.connHash {
		t.Fatal("rule changes must not reconnect")
	}
	s2.Connection.BrokerURL = "mqtt://other:1883"
	if compile(s2, quiet).connHash == c.connHash {
		t.Fatal("broker changes must reconnect")
	}
}

func TestEncodeCommand(t *testing.T) {
	c := compile(snapshot(), quiet)
	cmd := Command{Element: "Fan", Message: json.RawMessage(`{"value":1}`)}
	b, err := encodeCommand(c, c.downlinks["fan-1\x00Fan"], "fan-1", cmd)
	if err != nil || string(b) != `{"on":1,"raw":{"value":1}}` {
		t.Fatalf("template: %s %v", b, err)
	}
	cmd.Element = "Mode"
	b, err = encodeCommand(c, c.downlinks["fan-1\x00Mode"], "fan-1", Command{Element: "Mode", Message: json.RawMessage(`{"value":3}`)})
	if err != nil || len(b) != 1 || b[0] != 3 {
		t.Fatalf("encodeDownlink: %v %v", b, err)
	}
	b, err = encodeCommand(c, c.downlinks["fan-1\x00Raw"], "fan-1", Command{Element: "Raw", Message: json.RawMessage(`{"value":"x"}`)})
	if err != nil || string(b) != `{"value":"x"}` {
		t.Fatalf("as is: %s %v", b, err)
	}
}
