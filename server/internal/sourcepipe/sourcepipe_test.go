package sourcepipe

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taha2samy/quackquack/server/internal/events"
)

func mustCompile(t testing.TB, src string) *Decoder {
	t.Helper()
	d, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func rule(t testing.TB, u events.MQTTUplink, dec *Decoder) *Rule {
	t.Helper()
	decs := map[string]*Decoder{}
	if dec != nil {
		id := "dec-1"
		u.DecoderID = &id
		decs[id] = dec
	}
	r, err := CompileRule(u, decs)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestJSONToManyElements(t *testing.T) {
	r := rule(t, events.MQTTUplink{Format: "json", Device: json.RawMessage(`{"segment":1}`),
		FieldMap: json.RawMessage(`[{"element":"Temperature","value":"t"},{"element":"Humidity","value":"h"},
			{"element":"Battery","value":"bat","when":"exists"},{"element":"Raw","value":"","wrap":"raw"}]`)}, nil)
	vals, rej := Process(r, Message{Topic: "sensors/cold-1/up", Payload: []byte(`{"t":21.5,"h":48}`)})
	if len(rej) != 0 {
		t.Fatalf("rejections %v", rej)
	}
	if len(vals) != 3 || vals[0].DeviceExternalID != "cold-1" || vals[0].Element != "Temperature" ||
		string(vals[0].Message) != `{"value":21.5}` || string(vals[1].Message) != `{"value":48}` ||
		string(vals[2].Message) != `{"h":48,"t":21.5}` {
		t.Fatalf("values %+v", vals)
	}
}

func TestMissingFieldsAreReported(t *testing.T) {
	r := rule(t, events.MQTTUplink{Format: "json", Device: json.RawMessage(`{"fixed":"d1"}`),
		FieldMap: json.RawMessage(`[{"element":"A","value":"a"},{"element":"B","value":"b"}]`)}, nil)
	vals, rej := Process(r, Message{Topic: "x", Payload: []byte(`{"a":1}`)})
	if len(vals) != 1 || len(rej) != 1 || rej[0].Fatal || !strings.Contains(rej[0].Reason, `"b" missing`) {
		t.Fatalf("vals %v rej %v", vals, rej)
	}
	_, rej = Process(r, Message{Topic: "x", Payload: []byte(`not json`)})
	if len(rej) != 1 || !rej[0].Fatal {
		t.Fatalf("non-JSON must be fatal: %v", rej)
	}
	_, rej = Process(r, Message{Topic: "x", Payload: []byte(`{}`)})
	if !rej[len(rej)-1].Fatal {
		t.Fatalf("no values at all must be fatal: %v", rej)
	}
}

func TestDeviceFromFieldAndTime(t *testing.T) {
	tf := "ts"
	r := rule(t, events.MQTTUplink{Format: "json", Device: json.RawMessage(`{"field":"meta.id"}`), Time: &tf,
		FieldMap: json.RawMessage(`[{"element":"V","value":"readings[1].v"}]`)}, nil)
	vals, rej := Process(r, Message{Topic: "any", Payload: []byte(`{"meta":{"id":"m-7"},"ts":1791000000000,"readings":[{"v":1},{"v":2}]}`)})
	if len(rej) != 0 || len(vals) != 1 || vals[0].DeviceExternalID != "m-7" || string(vals[0].Message) != `{"value":2}` ||
		vals[0].TS == nil || !vals[0].TS.Equal(time.UnixMilli(1791000000000)) {
		t.Fatalf("vals %+v rej %v", vals, rej)
	}
	_, rej = Process(r, Message{Topic: "any", Payload: []byte(`{"readings":[]}`)})
	if len(rej) == 0 || !rej[0].Fatal || !strings.Contains(rej[0].Reason, "no device id") {
		t.Fatalf("missing device must be fatal: %v", rej)
	}
}

func TestTextAndNumberFormats(t *testing.T) {
	r := rule(t, events.MQTTUplink{Format: "number", Device: json.RawMessage(`{"fixed":"d"}`),
		FieldMap: json.RawMessage(`[{"element":"T","value":""}]`)}, nil)
	vals, _ := Process(r, Message{Payload: []byte(" 21.75 ")})
	if len(vals) != 1 || string(vals[0].Message) != `{"value":21.75}` {
		t.Fatalf("%v", vals)
	}
	if _, rej := Process(r, Message{Payload: []byte("warm")}); !rej[0].Fatal {
		t.Fatal("not a number must be fatal")
	}
}

// A real vendor codec from the TTN Device Repository runs unchanged.
func TestTTNDeviceRepositoryCodec(t *testing.T) {
	src, err := os.ReadFile("testdata/dragino_lht65.js")
	if err != nil {
		t.Fatal(err)
	}
	dec := mustCompile(t, string(src))
	payload, _ := hex.DecodeString("cbf60b0d0376010add7fff")
	out, err := dec.Decode(UplinkInput{Bytes: payload, FPort: 2})
	if err != nil {
		t.Fatal(err)
	}
	data := out.Data.(map[string]any)
	for k, want := range map[string]float64{"BatV": 3.062, "TempC_SHT": 28.29, "Hum_SHT": 88.6, "TempC_DS": 27.81} {
		if got, ok := toFloat(data[k]); !ok || got != want {
			t.Errorf("%s = %v, want %v", k, data[k], want)
		}
	}
	if data["Ext_sensor"] != "Temperature Sensor" {
		t.Errorf("Ext_sensor = %v", data["Ext_sensor"])
	}
	// through a rule: binary payload → elements
	r := rule(t, events.MQTTUplink{Format: "bytes", Device: json.RawMessage(`{"segment":1}`),
		FieldMap: json.RawMessage(`[{"element":"Temperature","value":"TempC_SHT"},{"element":"Humidity","value":"Hum_SHT"}]`)}, dec)
	vals, rej := Process(r, Message{Topic: "lora/lht65-01/up", Payload: payload, UserProperties: map[string]string{"fPort": "2"}})
	if len(rej) != 0 || len(vals) != 2 || string(vals[0].Message) != `{"value":28.29}` || vals[0].DeviceExternalID != "lht65-01" {
		t.Fatalf("vals %+v rej %v", vals, rej)
	}
	// wrong port: the codec's own error comes back as a fatal rejection
	_, rej = Process(r, Message{Topic: "lora/lht65-01/up", Payload: payload})
	if len(rej) == 0 || !strings.Contains(rej[len(rej)-1].Reason, "unknown FPort") {
		t.Fatalf("codec errors must surface: %v", rej)
	}
}

func TestLegacyDecoderAndEncode(t *testing.T) {
	dec := mustCompile(t, `function Decoder(bytes, port) { return {t: bytes[0] / 2, port: port}; }
		function encodeDownlink(input) { return {bytes: [input.data.value ? 1 : 0, 0xff]}; }`)
	out, err := dec.Decode(UplinkInput{Bytes: []byte{43}, FPort: 3})
	if err != nil {
		t.Fatal(err)
	}
	if m := out.Data.(map[string]any); m["t"] != 21.5 || m["port"] != int64(3) {
		t.Fatalf("legacy decode %v", m)
	}
	b, err := dec.Encode(DownlinkInput{Element: "Fan", Data: map[string]any{"value": 1}})
	if err != nil || len(b) != 2 || b[0] != 1 || b[1] != 0xff {
		t.Fatalf("encode %v %v", b, err)
	}
	jsonEnc := mustCompile(t, `function encodeDownlink(i) { return {payload: {on: i.data.value == 1, el: i.element}}; }`)
	b, err = jsonEnc.Encode(DownlinkInput{Element: "Fan", Data: map[string]any{"value": 1}})
	if err != nil || string(b) != `{"el":"Fan","on":true}` {
		t.Fatalf("payload encode %s %v", b, err)
	}
	bad := mustCompile(t, `function encodeDownlink(i) { return {bytes: [300]}; }`)
	if _, err := bad.Encode(DownlinkInput{}); err == nil {
		t.Fatal("bytes over 255 must fail")
	}
}

func TestSandbox(t *testing.T) {
	loop := mustCompile(t, `function decodeUplink(i) { while (true) {} }`)
	start := time.Now()
	if _, err := loop.Decode(UplinkInput{}); err == nil || !strings.Contains(err.Error(), "20ms") {
		t.Fatalf("infinite loop: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("interrupt too slow")
	}
	topLevelLoop, err := Compile(`while (true) {}`)
	if err == nil || topLevelLoop != nil {
		t.Fatal("a loop at load time must be stopped too")
	}
	for _, name := range []string{"require", "process", "setTimeout", "fetch", "console"} {
		d := mustCompile(t, `function decodeUplink(i) { return {data: {t: typeof `+name+`}}; }`)
		out, err := d.Decode(UplinkInput{})
		if err != nil || out.Data.(map[string]any)["t"] != "undefined" {
			t.Errorf("%s must be undefined: %v %v", name, out.Data, err)
		}
	}
	nan := mustCompile(t, `function decodeUplink(i) { return {data: {t: 0/0}}; }`)
	if _, err := nan.Decode(UplinkInput{}); err == nil || !strings.Contains(err.Error(), "NaN") {
		t.Fatalf("NaN: %v", err)
	}
	inf := mustCompile(t, `function decodeUplink(i) { return {data: {deep: [{t: 1/0}]}}; }`)
	if _, err := inf.Decode(UplinkInput{}); err == nil {
		t.Fatal("Infinity must be rejected")
	}
	state := mustCompile(t, `var n = 0; function decodeUplink(i) { n++; return {data: {n: n}}; }`)
	for range 3 {
		out, _ := state.Decode(UplinkInput{})
		if out.Data.(map[string]any)["n"] != int64(1) {
			t.Fatal("state must not survive between messages")
		}
	}
	if _, err := Compile(`function nope() {}`); err == nil {
		t.Fatal("a decoder without decodeUplink/Decoder/encodeDownlink must be refused")
	}
	if _, err := Compile(strings.Repeat(" ", MaxSourceBytes+1)); err == nil {
		t.Fatal("oversized source must be refused")
	}
	thrown := mustCompile(t, `function decodeUplink(i) { throw new Error("bad frame"); }`)
	if _, err := thrown.Decode(UplinkInput{}); err == nil || !strings.Contains(err.Error(), "bad frame") {
		t.Fatalf("thrown errors must surface: %v", err)
	}
}

func TestLimits(t *testing.T) {
	r := rule(t, events.MQTTUplink{Format: "json", Device: json.RawMessage(`{"fixed":"d"}`),
		FieldMap: json.RawMessage(`[{"element":"A","value":"a"}]`)}, nil)
	if _, rej := Process(r, Message{Payload: make([]byte, MaxInputBytes+1)}); !rej[0].Fatal {
		t.Fatal("oversized payload must be refused")
	}
	big := mustCompile(t, `function decodeUplink(i) { return {data: {a: "x".repeat(70000)}}; }`)
	r2 := rule(t, events.MQTTUplink{Format: "json", Device: json.RawMessage(`{"fixed":"d"}`),
		FieldMap: json.RawMessage(`[{"element":"A","value":"a"}]`)}, big)
	vals, rej := Process(r2, Message{Payload: []byte(`{}`)})
	if len(vals) != 0 || !strings.Contains(rej[0].Reason, "64 KiB") {
		t.Fatalf("oversized value: %v %v", vals, rej)
	}
}

func TestConcurrentDecode(t *testing.T) {
	dec := mustCompile(t, `function decodeUplink(i) { return {data: {t: i.bytes[0]}}; }`)
	var wg sync.WaitGroup
	for n := range 50 {
		wg.Go(func() {
			out, err := dec.Decode(UplinkInput{Bytes: []byte{byte(n)}})
			if err != nil || out.Data.(map[string]any)["t"] != int64(n) {
				t.Errorf("concurrent decode %d: %v %v", n, out.Data, err)
			}
		})
	}
	wg.Wait()
}

func BenchmarkProcessJSON(b *testing.B) {
	r := rule(b, events.MQTTUplink{Format: "json", Device: json.RawMessage(`{"segment":1}`),
		FieldMap: json.RawMessage(`[{"element":"T","value":"t"},{"element":"H","value":"h"},{"element":"B","value":"b"}]`)}, nil)
	m := Message{Topic: "sensors/cold-1/up", Payload: []byte(`{"t":21.5,"h":48,"b":3.9}`)}
	for b.Loop() {
		Process(r, m)
	}
}

func BenchmarkProcessWithDecoder(b *testing.B) {
	dec := mustCompile(b, `function decodeUplink(i) { return {data: {t: i.bytes[0] / 2, h: i.bytes[1], b: i.bytes[2] / 10}}; }`)
	r := rule(b, events.MQTTUplink{Format: "bytes", Device: json.RawMessage(`{"segment":1}`),
		FieldMap: json.RawMessage(`[{"element":"T","value":"t"},{"element":"H","value":"h"},{"element":"B","value":"b"}]`)}, dec)
	m := Message{Topic: "sensors/cold-1/up", Payload: []byte{43, 48, 39}}
	for b.Loop() {
		Process(r, m)
	}
}
