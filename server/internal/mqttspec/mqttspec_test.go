package mqttspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFilterAndMatch(t *testing.T) {
	for _, f := range []string{"a/b", "a/+/c", "a/#", "#", "+", "+/+/#"} {
		if err := ValidateFilter(f); err != nil {
			t.Errorf("%q: %v", f, err)
		}
	}
	for _, f := range []string{"", "a/b#", "a/#/c", "a+/b", "$share/g/a"} {
		if ValidateFilter(f) == nil {
			t.Errorf("%q must be invalid", f)
		}
	}
	cases := []struct {
		f, t string
		want bool
	}{
		{"sensors/+/up", "sensors/cold-1/up", true},
		{"sensors/+/up", "sensors/cold-1/down", false},
		{"sensors/+/up", "sensors/a/b/up", false},
		{"sensors/#", "sensors", true},
		{"sensors/#", "sensors/a/b", true},
		{"#", "$SYS/broker", false},
		{"+/x", "$SYS/x", false},
		{"a/b", "a/b", true},
		{"a/b", "a/b/c", false},
	}
	for _, c := range cases {
		if got := Match(c.f, c.t); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.f, c.t, got)
		}
	}
}

func TestPaths(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"a":{"b":[10,{"c d":5}]},"x":1}`), &doc)
	for path, want := range map[string]any{"a.b[0]": 10.0, `a.b[1]["c d"]`: 5.0, "x": 1.0} {
		p, err := ParsePath(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := p.Get(doc); !ok || got != want {
			t.Errorf("%s = %v %v", path, got, ok)
		}
	}
	p, _ := ParsePath("a.b[5]")
	if _, ok := p.Get(doc); ok {
		t.Error("out of range index must miss")
	}
	if _, err := ParsePath("a[x]"); err == nil {
		t.Error("bad index must fail")
	}
	whole, _ := ParsePath("")
	if v, ok := whole.Get(doc); !ok || v == nil {
		t.Error("empty path is the whole value")
	}
}

func TestDeviceAndFieldMap(t *testing.T) {
	if _, err := ParseDevice(json.RawMessage(`{"segment":1}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{``, `{}`, `{"segment":1,"fixed":"x"}`, `{"segment":-1}`} {
		if _, err := ParseDevice(json.RawMessage(bad)); err == nil {
			t.Errorf("device %s must be invalid", bad)
		}
	}
	ms, err := ParseFieldMap(json.RawMessage(`[{"element":"T","value":"data.t"},{"element":"C","value":"","wrap":"raw"}]`))
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v %v", ms, err)
	}
	for _, bad := range []string{`[]`, `[{"value":"a"}]`, `[{"element":"x","wrap":"zip"}]`, `{"element":"x"}`} {
		if _, err := ParseFieldMap(json.RawMessage(bad)); err == nil {
			t.Errorf("field map %s must be invalid", bad)
		}
	}
}

func TestTopicTemplateAndInjectionGuard(t *testing.T) {
	if err := ValidateTopicTemplate("cmd/{device}/{element}"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"cmd/+", "cmd/#", "$SYS/x", "cmd/{nope}"} {
		if ValidateTopicTemplate(bad) == nil {
			t.Errorf("template %q must be invalid", bad)
		}
	}
	got, err := RenderTopic("cmd/{device}/{element}", map[string]string{"device": "cold-1", "element": "Fan"})
	if err != nil || got != "cmd/cold-1/Fan" {
		t.Fatalf("%q %v", got, err)
	}
	for _, v := range []string{"a/b", "+", "#", "$x", "", "a\x00"} {
		if _, err := RenderTopic("cmd/{element}", map[string]string{"element": v}); err == nil {
			t.Errorf("value %q must be refused", v)
		}
	}
}

func TestAuthAndSecrets(t *testing.T) {
	if _, err := ParseAuth(json.RawMessage(`{"method":"password","username":"u","password":"hunter2"}`)); err == nil ||
		!strings.Contains(err.Error(), "reference") {
		t.Fatalf("plaintext password must be refused: %v", err)
	}
	a, err := ParseAuth(json.RawMessage(`{"username":"u","password":"env:MQTT_PW"}`))
	if err != nil || a.Method != AuthPassword {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := ParseTLS(json.RawMessage(`{"cert":"file:/c.pem"}`)); err == nil {
		t.Fatal("cert without key must fail")
	}
	t.Setenv("QUACK_TEST_SECRET", "s3cret")
	if b, err := Resolve("env:QUACK_TEST_SECRET"); err != nil || string(b) != "s3cret" {
		t.Fatalf("%s %v", b, err)
	}
	f := filepath.Join(t.TempDir(), "pw")
	_ = os.WriteFile(f, []byte("filepw"), 0o600)
	if b, err := Resolve("file:" + f); err != nil || string(b) != "filepw" {
		t.Fatalf("%s %v", b, err)
	}
	if _, err := Resolve("env:QUACK_TEST_NOT_SET_X"); err == nil {
		t.Fatal("missing env must fail")
	}
	if _, err := Resolve("plaintext"); err == nil || strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("non-reference must fail without echoing the value: %v", err)
	}
}
