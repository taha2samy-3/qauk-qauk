package history

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func pointsOf(t *testing.T, payload string) map[string]float64 {
	t.Helper()
	ev := Event{Time: time.Now(), ID: uuid.New(), ElementID: uuid.New(), Payload: json.RawMessage(payload)}
	ev.Value = NumericValue(ev.Payload)
	out := map[string]float64{}
	for _, p := range Points(ev) {
		if _, dup := out[p.Field]; dup {
			t.Fatalf("%s: duplicate field %q", payload, p.Field)
		}
		out[p.Field] = p.Value
	}
	return out
}

func TestPoints(t *testing.T) {
	cases := []struct {
		payload string
		want    map[string]float64
	}{
		{`{"value": 21.5}`, map[string]float64{"value": 21.5}},
		{`21.5`, map[string]float64{"value": 21.5}},
		{`true`, map[string]float64{"value": 1}},
		{`{"value": false}`, map[string]float64{"value": 0}},
		{`{"value": "ON"}`, map[string]float64{}},
		{`{"value": " 7.25 "}`, map[string]float64{"value": 7.25}},
		// chart shape: y doubles as the element's value
		{`{"x": "2026-10-07T10:00:00Z", "y": 3}`, map[string]float64{"y": 3, "value": 3}},
		{`{"temperature": 21, "humidity": "48.5", "relay": "ON", "ok": true, "gps": {"lat": 30.04, "lng": 31.23}}`,
			map[string]float64{"temperature": 21, "humidity": 48.5, "ok": 1, "gps.lat": 30.04, "gps.lng": 31.23}},
		{`{"sensors": [{"temp": 1}, {"temp": 2}], "list": [5, 6]}`,
			map[string]float64{"sensors[0].temp": 1, "sensors[1].temp": 2, "list[0]": 5, "list[1]": 6}},
		// an object key that is a digit is a key, not an index
		{`{"m": {"0": 9}}`, map[string]float64{"m.0": 9}},
		// not storable as float8, NaN-ish strings, hex, keys a path can't address
		{`{"big": 1e400, "s": "1e999", "nan": "NaN", "inf": "Infinity", "hex": "0x10", "bad key": 1, "a.b": 2, "u": "1_000"}`,
			map[string]float64{}},
		{`null`, map[string]float64{}},
		{`[1, 2]`, map[string]float64{}},
		{`"text"`, map[string]float64{}},
		{`not json`, map[string]float64{}},
	}
	for _, c := range cases {
		got := pointsOf(t, c.payload)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("Points(%s) = %v, want %v", c.payload, got, c.want)
		}
	}
}

func TestPointsLimits(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := range 200 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"f%03d": %d`, i, i)
	}
	b.WriteString("}")
	if n := len(pointsOf(t, b.String())); n != MaxPointsPerEvent {
		t.Fatalf("got %d points, want the cap %d", n, MaxPointsPerEvent)
	}

	deep := `{"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a":1}}}}}}}}}}`
	if got := pointsOf(t, deep); len(got) != 0 {
		t.Fatalf("nesting beyond MaxDepth must be skipped, got %v", got)
	}

	arr := `{"a": [` + strings.Repeat("1,", 50) + `1]}`
	if n := len(pointsOf(t, arr)); n != MaxArrayItems {
		t.Fatalf("got %d array points, want %d", n, MaxArrayItems)
	}
}

func TestPointsDeterministic(t *testing.T) {
	ev := Event{ID: uuid.New(), Payload: json.RawMessage(`{"c":3,"a":1,"b":2}`)}
	first := fmt.Sprint(Points(ev))
	for range 20 {
		if got := fmt.Sprint(Points(ev)); got != first {
			t.Fatalf("order changed: %s vs %s", got, first)
		}
	}
}

func TestValidField(t *testing.T) {
	for _, f := range []string{"value", "temperature", "gps.lat", "sensors[0].temp", "a[1][2]", "m.0", "$x", "@received"} {
		if !ValidField(f) {
			t.Errorf("%q should be valid", f)
		}
	}
	for _, f := range []string{"", "a.", ".a", "a..b", "a b", "a[x]", "a]", strings.Repeat("a", MaxFieldLen+1)} {
		if ValidField(f) {
			t.Errorf("%q should be invalid", f)
		}
	}
}

func TestValidateMessage(t *testing.T) {
	ok := []string{
		`{"value": 1}`, `"plain"`, `"éا"`, `"🦆"`, // é, ا, 🦆 (a valid pair)
		`{"k\"ey": "a\\u0000b"}`, // an escaped backslash: the text \u0000, not NUL
		`"tab\tnew\nline"`, `"emoji 🦆 مرحبا"`,
	}
	for _, m := range ok {
		if err := ValidateMessage(json.RawMessage(m)); err != nil {
			t.Errorf("%s: unexpected error %v", m, err)
		}
	}
	bad := []string{
		`"a\u0000b"`, `{"\u0000": 1}`, `"\ud800"`, `"\ud800x"`, `"\udc00"`, `"\ud800A"`, `["ok", "\uDBFF"]`,
	}
	for _, m := range bad {
		if err := ValidateMessage(json.RawMessage(m)); err == nil {
			t.Errorf("%s: want an error", m)
		}
	}
	if err := ValidateMessage(json.RawMessage(`{"a":`)); err == nil {
		t.Error("invalid JSON must be rejected")
	}
}

func TestParseTargets(t *testing.T) {
	got := ParseTargets("timescale, clickhouse", ",clickhouse://h:9000/q", "postgres://main")
	want := []Target{{"timescale", "postgres://main"}, {"clickhouse", "clickhouse://h:9000/q"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(ParseTargets("", "", "x")) != 0 {
		t.Fatal("no drivers → no targets")
	}
}
