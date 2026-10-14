package jsbox

import (
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, src string) (string, time.Duration, error) {
	t.Helper()
	s, err := Compile("t.js", "var out = (function () { "+src+" })();")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	vm, stop, err := s.Load(50*time.Millisecond, "deadline")
	if err != nil {
		return "", time.Since(start), err
	}
	defer stop()
	return vm.Get("out").String(), time.Since(start), nil
}

func TestGuardsStopBigAllocations(t *testing.T) {
	for name, src := range map[string]string{
		"repeat":      `return "x".repeat(64 << 20)`,
		"padStart":    `return "x".padStart(64 << 20)`,
		"padEnd":      `return "x".padEnd(64 << 20)`,
		"fill":        `var a = []; a.length = 1e8; return a.fill(0)`,
		"join":        `var a = []; a.length = 1e8; return a.join(",")`,
		"ArrayBuffer": `return new ArrayBuffer(512 << 20)`,
		"Uint8Array":  `return new Uint8Array(1e8)`,
		"Float64":     `return new Float64Array(1e8)`,
	} {
		t.Run(name, func(t *testing.T) {
			_, took, err := run(t, src)
			if err == nil || !strings.Contains(Message(err), "too large") {
				t.Fatalf("want RangeError, got %v", err)
			}
			if took > 40*time.Millisecond {
				t.Errorf("took %v", took)
			}
		})
	}
}

func TestNormalUseStillWorks(t *testing.T) {
	got, _, err := run(t, `
		var b = new Uint8Array([1, 2, 3]);
		var dv = new DataView(new ArrayBuffer(4)); dv.setFloat32(0, 1.5);
		var a = new Array(3).fill(7);
		return [ "ab".repeat(3), "7".padStart(3, "0"), "7".padEnd(2, "x"), [1,2].join("-"), b.length, dv.getFloat32(0),
		  a.join(""), Array.isArray(a), a instanceof Array, b instanceof Uint8Array, Uint8Array.from([1,2]).length,
		  new Uint8Array(new ArrayBuffer(8)).length, new Float64Array(2).length ].join("|")`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ababab|007|7x|1-2|3|1.5|777|true|true|true|2|8|2" {
		t.Fatalf("got %q", got)
	}
}

func TestOnlyReferencedPrimitivesAreGuarded(t *testing.T) {
	if g := guardSource(`function f(x) { return x * 2 }`); g != "" {
		t.Fatalf("no guarded primitive used, got guards:\n%s", g)
	}
	g := guardSource(`s.repeat(2); new Uint8Array(2)`)
	if !strings.Contains(g, `"repeat"`) || !strings.Contains(g, `"Uint8Array"`) || strings.Contains(g, "Float64Array") {
		t.Fatalf("guards:\n%s", g)
	}
}

func TestRecursionIsBounded(t *testing.T) {
	for _, src := range []string{
		`function f() { return f() } return f()`,
		`function f() { return [0].map(f) } return f()`,
	} {
		_, took, err := run(t, src)
		if err == nil {
			t.Fatalf("%s: want an error", src)
		}
		if took > 100*time.Millisecond {
			t.Errorf("%s: took %v", src, took)
		}
	}
}

func TestDeadline(t *testing.T) {
	_, took, err := run(t, `while (true) {}`)
	if err == nil || Message(err) != "deadline" {
		t.Fatalf("got %v", err)
	}
	if took > 100*time.Millisecond {
		t.Errorf("took %v", took)
	}
}

func TestLoadReportsErrors(t *testing.T) {
	s, err := Compile("t.js", `throw new Error("boom")`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(20*time.Millisecond, "x"); err == nil || !strings.Contains(Message(err), "boom") {
		t.Fatalf("got %v", err)
	}
}

func BenchmarkLoadNoGuards(b *testing.B) {
	s, _ := Compile("b.js", `function transform(m) { return m }`)
	for b.Loop() {
		_, stop, err := s.Load(20*time.Millisecond, "x")
		if err != nil {
			b.Fatal(err)
		}
		stop()
	}
}
