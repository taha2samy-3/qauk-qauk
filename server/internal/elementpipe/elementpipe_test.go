package elementpipe

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- helpers ----

func ptr[T any](v T) *T { return &v }

func mustCompile(t *testing.T, steps ...StepConfig) *Pipeline {
	t.Helper()
	p, err := Compile(1, steps)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return p
}

func compileErr(t *testing.T, want string, steps ...StepConfig) {
	t.Helper()
	_, err := Compile(1, steps)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want an error containing %q, got %v", want, err)
	}
}

func run(t *testing.T, p *Pipeline, m Msg, ctx Context) (Msg, RunResult) {
	t.Helper()
	out, res, _, err := p.ApplyWithContext(m, ctx)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return out, res
}

func runErr(t *testing.T, p *Pipeline, m Msg) *ErrPipeline {
	t.Helper()
	_, _, _, err := p.Apply(m, nil)
	var ep *ErrPipeline
	if !errors.As(err, &ep) {
		t.Fatalf("want an ErrPipeline, got %v", err)
	}
	return ep
}

func num(t *testing.T, m Msg, path string) float64 {
	t.Helper()
	f, _ := compileField(path)
	v, ok := f.num(m)
	if !ok {
		t.Fatalf("%s is not a number in %v", path, m)
	}
	return v
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func jsonMsg(t *testing.T, s string) Msg {
	t.Helper()
	var m Msg
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// ---- pick ----

func TestPick(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindPick, Path: "sensors[0].t"})
	out, _ := run(t, p, jsonMsg(t, `{"sensors":[{"t":21.5}]}`), Context{})
	near(t, num(t, out, "value"), 21.5)

	obj := mustCompile(t, StepConfig{Kind: KindPick, Path: "data"})
	out, _ = run(t, obj, jsonMsg(t, `{"data":{"temperature":21,"humidity":40}}`), Context{})
	if _, wrapped := out["value"]; wrapped || num(t, out, "temperature") != 21 {
		t.Fatalf("an object at the path must become the message, got %v", out)
	}
	if ep := runErr(t, p, jsonMsg(t, `{"x":1}`)); !strings.Contains(ep.Reason, "not found") {
		t.Fatal(ep)
	}
	compileErr(t, "path is required", StepConfig{Kind: KindPick})
}

// ---- scale / unit ----

func TestScaleAndInverse(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScale, Mul: ptr(0.1), Add: 2})
	out, _ := run(t, p, Msg{"value": 500.0}, Context{})
	near(t, num(t, out, "value"), 52)
	inv, err := p.Inverse(Msg{"value": 52.0})
	if err != nil {
		t.Fatal(err)
	}
	near(t, num(t, inv, "value"), 500)

	compileErr(t, "mul is required", StepConfig{Kind: KindScale})
	compileErr(t, "not be zero", StepConfig{Kind: KindScale, Mul: ptr(0.0)})
	compileErr(t, "too small to invert", StepConfig{Kind: KindScale, Mul: ptr(1e-320)})
	compileErr(t, "finite", StepConfig{Kind: KindScale, Mul: ptr(math.Inf(1))})
}

func TestScaleOverflowFails(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScale, Mul: ptr(10.0)})
	if ep := runErr(t, p, Msg{"value": 1e308}); !strings.Contains(ep.Reason, "Inf") {
		t.Fatal(ep)
	}
}

func TestFieldPaths(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScale, Field: "sensor.t", Mul: ptr(2.0)})
	in := jsonMsg(t, `{"sensor":{"t":1.5,"id":"a"},"x":1}`)
	out, _ := run(t, p, in, Context{})
	near(t, num(t, out, "sensor.t"), 3)
	near(t, num(t, in, "sensor.t"), 1.5) // input untouched
	if out["sensor"].(map[string]any)["id"] != "a" || num(t, out, "x") != 1 {
		t.Fatalf("other fields lost: %v", out)
	}
}

func TestUnits(t *testing.T) {
	for _, c := range []struct {
		from, to string
		in, out  float64
	}{
		{"degC", "degF", 100, 212},
		{"°C", "K", 0, 273.15},
		{"F", "C", 32, 0},
		{"bar", "psi", 1, 14.503773773},
		{"kPa", "Pa", 1, 1000},
		{"m/s", "km/h", 10, 36},
		{"kn", "m/s", 1, 0.514444444},
		{"kWh", "J", 1, 3.6e6},
		{"L/min", "m3/h", 1000, 60},
	} {
		p := mustCompile(t, StepConfig{Kind: KindUnit, From: c.from, To: c.to})
		out, _ := run(t, p, Msg{"value": c.in}, Context{})
		if math.Abs(num(t, out, "value")-c.out) > 1e-6 {
			t.Errorf("%s→%s: %v → %v, want %v", c.from, c.to, c.in, out["value"], c.out)
		}
		back, err := p.Inverse(out)
		if err != nil {
			t.Fatal(err)
		}
		near(t, num(t, back, "value"), c.in)
	}
	compileErr(t, "can't convert", StepConfig{Kind: KindUnit, From: "C", To: "bar"})
	compileErr(t, "unknown unit", StepConfig{Kind: KindUnit, From: "m", To: "s/km/h"})
	if len(Units()) < 40 || len(KnownUnits()) != len(Units()) {
		t.Fatalf("units: %d", len(Units()))
	}
}

// ---- round / clamp ----

func TestRoundHalfAwayFromZero(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindRound, Decimals: ptr(0)})
	for in, want := range map[float64]float64{2.5: 3, -2.5: -3, 2.4: 2, -2.4: -2} {
		out, _ := run(t, p, Msg{"value": in}, Context{})
		near(t, num(t, out, "value"), want)
	}
	big := mustCompile(t, StepConfig{Kind: KindRound, Decimals: ptr(6)})
	out, _ := run(t, big, Msg{"value": 1e303}, Context{})
	near(t, num(t, out, "value"), 1e303)
	compileErr(t, "0-6", StepConfig{Kind: KindRound, Decimals: ptr(7)})
}

func TestClamp(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindClamp, Min: ptr(0.0), Max: ptr(100.0)})
	out, _ := run(t, p, Msg{"value": 120.0}, Context{})
	near(t, num(t, out, "value"), 100)
	compileErr(t, "min must be", StepConfig{Kind: KindClamp, Min: ptr(5.0), Max: ptr(1.0)})
}

// ---- map ----

func table(kv ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = json.RawMessage(kv[i+1])
	}
	return out
}

func TestMapAndInverse(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindMap, Table: table("OPEN", `"ON"`, "CLOSED", `"OFF"`)})
	out, _ := run(t, p, Msg{"value": "OPEN"}, Context{})
	if out["value"] != "ON" {
		t.Fatal(out)
	}
	inv, err := p.Inverse(Msg{"value": "OFF"})
	if err != nil || inv["value"] != "CLOSED" {
		t.Fatalf("%v %v", inv, err)
	}
	if !p.Invertible() || !p.IsInvertible() {
		t.Fatal("a one-to-one map is invertible")
	}
	if _, err := p.Inverse(Msg{"value": "MAYBE"}); err == nil {
		t.Fatal("a value outside the table must fail, not pass through")
	}
}

func TestMapNumbersAndTypes(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindMap, Table: table("1000000", `"big"`, "0.00001", `"tiny"`, "1", `"one"`, "true", `"yes"`)})
	for in, want := range map[any]string{1e6: "big", 0.00001: "tiny", json.Number("1.0"): "one", true: "yes"} {
		out, _ := run(t, p, Msg{"value": in}, Context{})
		if out["value"] != want {
			t.Errorf("%v → %v, want %s", in, out["value"], want)
		}
	}
	// commands get the key back typed: "1" → 1, "true" → true
	inv, err := p.Inverse(Msg{"value": "one"})
	if err != nil || inv["value"] != 1.0 {
		t.Fatalf("%v %v", inv, err)
	}
	// typed compare: the string "1" is not the number 1
	num := mustCompile(t, StepConfig{Kind: KindMap, Table: table("ON", `1`, "OFF", `0`)})
	if _, err := num.Inverse(Msg{"value": "1"}); err == nil {
		t.Fatal(`"1" must not match 1`)
	}
	if out, err := num.Inverse(Msg{"value": json.Number("1")}); err != nil || out["value"] != "ON" {
		t.Fatalf("%v %v", out, err)
	}
}

func TestMapNotOneToOneIsNotAppliedToCommands(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindMap, Table: table("a", `1`, "b", `1`)})
	if p.Invertible() || p.IsInvertible() || p.StepIsInvertible(0) {
		t.Fatal("a non one-to-one map can't be inverted")
	}
	inv, err := p.Inverse(Msg{"value": 1.0})
	if err != nil || inv["value"] != 1.0 {
		t.Fatalf("the command must pass unchanged: %v %v", inv, err)
	}
}

func TestMapMissingFieldFailsEvenWithDefault(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindMap, Table: table("a", `1`), Default: ptr(json.RawMessage(`0`))})
	if ep := runErr(t, p, Msg{"other": 1.0}); !strings.Contains(ep.Reason, "missing") {
		t.Fatal(ep)
	}
	out, _ := run(t, p, Msg{"value": "zzz"}, Context{})
	if out["value"] != 0.0 {
		t.Fatalf("default: %v", out)
	}
	if _, err := p.Inverse(Msg{"other": 1.0}); err == nil {
		t.Fatal("inverse with the field missing must fail")
	}
}

func TestMapTableValuesAreNotShared(t *testing.T) {
	p := mustCompile(t,
		StepConfig{Kind: KindMap, Table: table("ON", `{"state":1}`)},
		StepConfig{Kind: KindScript, Source: `function transform(m) { m.value.state++; return m }`})
	for i := 0; i < 3; i++ {
		out, _ := run(t, p, Msg{"value": "ON"}, Context{})
		if num(t, out, "value.state") != 2 {
			t.Fatalf("run %d: %v (the table was changed by an earlier run)", i, out)
		}
	}
	direct := mustCompile(t, StepConfig{Kind: KindMap, Table: table("ON", `{"state":1}`)})
	a, _ := run(t, direct, Msg{"value": "ON"}, Context{})
	a["value"].(map[string]any)["state"] = 99.0
	b, _ := run(t, direct, Msg{"value": "ON"}, Context{})
	if num(t, b, "value.state") != 1 {
		t.Fatal("an emitted table value is shared with the table")
	}
}

// ---- deadband ----

func TestDeadbandAbs(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindDeadband, Abs: ptr(0.5)})
	if _, res := run(t, p, Msg{"value": 20.2}, Context{Last: Msg{"value": 20.0}}); res != RunFiltered {
		t.Fatal("a small change must be filtered")
	}
	if _, res := run(t, p, Msg{"value": 20.6}, Context{Last: Msg{"value": 20.0}}); res != RunOK {
		t.Fatal("a big change must pass")
	}
	if _, res := run(t, p, Msg{"value": 20.0}, Context{}); res != RunOK {
		t.Fatal("the first value must pass")
	}
	if _, res := run(t, p, Msg{"value": "n/a"}, Context{Last: Msg{"value": 20.0}}); res != RunOK {
		t.Fatal("non-numbers pass")
	}
}

func TestDeadbandPctFromZero(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindDeadband, Pct: ptr(5.0)})
	for _, v := range []float64{100, -50, 0.001} {
		if _, res := run(t, p, Msg{"value": v}, Context{Last: Msg{"value": 0.0}}); res != RunOK {
			t.Fatalf("0 → %v must pass", v)
		}
	}
	if _, res := run(t, p, Msg{"value": 0.0}, Context{Last: Msg{"value": 0.0}}); res != RunFiltered {
		t.Fatal("0 → 0 is no change")
	}
	if _, res := run(t, p, Msg{"value": 104.0}, Context{Last: Msg{"value": 100.0}}); res != RunFiltered {
		t.Fatal("4% < 5%")
	}
}

func TestDeadbandAbsAndPctPassWhenEitherIsExceeded(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindDeadband, Abs: ptr(10.0), Pct: ptr(1.0)})
	if _, res := run(t, p, Msg{"value": 102.0}, Context{Last: Msg{"value": 100.0}}); res != RunOK {
		t.Fatal("2% exceeds pct")
	}
	if _, res := run(t, p, Msg{"value": 100.5}, Context{Last: Msg{"value": 100.0}}); res != RunFiltered {
		t.Fatal("below both")
	}
}

func TestDeadbandMaxSilence(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindDeadband, Abs: ptr(1.0), MaxSilence: ptr("1m")})
	now := time.Now()
	last := Msg{"value": 20.0}
	if _, res := run(t, p, Msg{"value": 20.1}, Context{Time: now, Last: last, LastAt: now.Add(-30 * time.Second)}); res != RunFiltered {
		t.Fatal("within max_silence: filtered")
	}
	if _, res := run(t, p, Msg{"value": 20.1}, Context{Time: now, Last: last, LastAt: now.Add(-61 * time.Second)}); res != RunOK {
		t.Fatal("after max_silence: passes")
	}
	compileErr(t, "at least 1s", StepConfig{Kind: KindDeadband, Abs: ptr(1.0), MaxSilence: ptr("1ns")})
	if got := p.MaxSilence(); got != time.Minute {
		t.Fatal(got)
	}
}

func TestDeadbandValidation(t *testing.T) {
	compileErr(t, "at least one of abs or pct", StepConfig{Kind: KindDeadband})
	compileErr(t, "abs must be a number > 0", StepConfig{Kind: KindDeadband, Abs: ptr(-1.0)})
	compileErr(t, "pct must be a number > 0", StepConfig{Kind: KindDeadband, Pct: ptr(math.NaN())})
}

func TestDeadbandMustComeAfterSteps(t *testing.T) {
	compileErr(t, "put the deadband after",
		StepConfig{Kind: KindDeadband, Abs: ptr(1.0)}, StepConfig{Kind: KindScale, Mul: ptr(0.1)})
	compileErr(t, "put the deadband after",
		StepConfig{Kind: KindDeadband, Field: "a", Abs: ptr(1.0)}, StepConfig{Kind: KindRound, Field: `["a"]`, Decimals: ptr(1)})
	compileErr(t, "put the deadband after",
		StepConfig{Kind: KindDeadband, Abs: ptr(1.0)}, StepConfig{Kind: KindScript, Source: `function transform(m){return m}`})
	// fine: other fields, drop_if, and deadband last
	mustCompile(t, StepConfig{Kind: KindDeadband, Field: "a", Abs: ptr(1.0)}, StepConfig{Kind: KindScale, Field: "b", Mul: ptr(2.0)})
	mustCompile(t, StepConfig{Kind: KindDeadband, Abs: ptr(1.0)}, StepConfig{Kind: KindDropIf, Op: "<", Value: ptr(0.0)})
	mustCompile(t, StepConfig{Kind: KindScale, Mul: ptr(0.1)}, StepConfig{Kind: KindDeadband, Abs: ptr(1.0)})
}

// ---- drop_if ----

func TestDropIf(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindDropIf, Op: "<", Value: ptr(-40.0)})
	if _, res := run(t, p, Msg{"value": -50.0}, Context{}); res != RunFiltered {
		t.Fatal("must drop")
	}
	if _, res := run(t, p, Msg{"value": "offline"}, Context{}); res != RunOK {
		t.Fatal("non-numbers pass")
	}
	compileErr(t, "op must be", StepConfig{Kind: KindDropIf, Op: "~", Value: ptr(1.0)})
	compileErr(t, "value is required", StepConfig{Kind: KindDropIf, Op: "<"})
}

// ---- script ----

func TestScriptTransformAndNull(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScript, Source: `function transform(m, ctx) {
		if (m.value < 0) return null;
		m.value = m.value * 2; m.el = ctx.element; return m }`})
	out, _ := run(t, p, Msg{"value": 3.0}, Context{Element: "Temp"})
	if num(t, out, "value") != 6 || out["el"] != "Temp" {
		t.Fatal(out)
	}
	if _, res := run(t, p, Msg{"value": -1.0}, Context{}); res != RunFiltered {
		t.Fatal("null filters")
	}
}

func TestScriptUndefinedIsAnError(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScript, Source: `function transform(m) { m.value *= 2 }`})
	if ep := runErr(t, p, Msg{"value": 1.0}); !strings.Contains(ep.Reason, "missing return") {
		t.Fatal(ep)
	}
}

func TestScriptCannotChangeInputsOrKeepState(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScript, Source: `function transform(m, ctx) {
		ctx.last.counter = (ctx.last.counter || 0) + 1; ctx.last.value = 999;
		m.data.t = 12345; m.arr.push(5); m.n = ctx.last.counter; return m }`})
	last := Msg{"value": 1.0}
	in := jsonMsg(t, `{"value":1,"data":{"t":1},"arr":[1,2]}`)
	for i := 0; i < 3; i++ {
		out, _ := run(t, p, in, Context{Last: last})
		if num(t, out, "n") != 1 {
			t.Fatalf("run %d: state kept between messages: %v", i, out)
		}
		if len(out["arr"].([]any)) != 3 {
			t.Fatalf("push lost: %v", out)
		}
	}
	if len(last) != 1 || last["value"] != 1.0 {
		t.Fatalf("ctx.last was modified: %v", last)
	}
	if num(t, in, "data.t") != 1 || len(in["arr"].([]any)) != 2 {
		t.Fatalf("input modified: %v", in)
	}
}

// Run with -race: one compiled pipeline, many goroutines, one shared last.
func TestConcurrentRunsShareNothing(t *testing.T) {
	p := mustCompile(t,
		StepConfig{Kind: KindScale, Mul: ptr(2.0)},
		StepConfig{Kind: KindScript, Source: `function transform(m, ctx) { ctx.last.x = 1; m.v2 = m.value + 1; return m }`},
		StepConfig{Kind: KindDeadband, Abs: ptr(0.5)})
	last := Msg{"value": 0.0, "v2": 1.0}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, _, _, err := p.ApplyWithContext(Msg{"value": float64(i)}, Context{Last: last}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(last) != 2 {
		t.Fatalf("last modified: %v", last)
	}
}

func TestScriptResultChecks(t *testing.T) {
	for src, want := range map[string]string{
		`function transform(m) { m.value = NaN; return m }`:              "NaN or Infinity",
		`function transform(m) { m.value = 1/0; return m }`:              "NaN or Infinity",
		`function transform(m) { m.f = function(){}; return m }`:         "function",
		`function transform(m) { return 5 }`:                             "object or null",
		`function transform(m) { return [1] }`:                           "object or null",
		`function transform(m) { m.s = "x".repeat(64 << 20); return m }`: "too large",
		`function transform(m) { throw new Error("boom") }`:              "boom",
		`function transform(m) { while (true) {} }`:                      "20ms",
		`function transform(m) { return require("fs") }`:                 "require",
	} {
		p := mustCompile(t, StepConfig{Kind: KindScript, Source: src})
		start := time.Now()
		ep := runErr(t, p, Msg{"value": 1.0})
		if !strings.Contains(ep.Reason, want) {
			t.Errorf("%s: %q does not mention %q", src, ep.Reason, want)
		}
		if took := time.Since(start); took > 150*time.Millisecond {
			t.Errorf("%s: took %v", src, took)
		}
	}
	// the result size is bounded (a cheap way to build >64 KiB, also under -race)
	big := mustCompile(t, StepConfig{Kind: KindScript, Source: `function transform(m) { var a = []; for (var i = 0; i < 5000; i++) a.push("0123456789abcdef"); m.a = a; return m }`})
	if ep := runErr(t, big, Msg{"value": 1.0}); !strings.Contains(ep.Reason, "over 64 KiB") && !strings.Contains(ep.Reason, "20ms") {
		t.Fatal(ep)
	}
	// a Date becomes a UTC string
	p := mustCompile(t, StepConfig{Kind: KindScript, Source: `function transform(m) { m.d = new Date(0); return m }`})
	out, _ := run(t, p, Msg{"value": 1.0}, Context{})
	if out["d"] != "1970-01-01T00:00:00.000Z" {
		t.Fatal(out["d"])
	}
}

func TestScriptUntransform(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScript, Source: `
		function transform(m) { m.value = m.value / 30; return m }
		function untransform(m, ctx) { m.value = m.value * 30; m.el = ctx.element; return m }`})
	inv, err := p.InverseWithContext(Msg{"value": 50.0}, Context{Element: "Fan"})
	if err != nil || num(t, inv, "value") != 1500 || inv["el"] != "Fan" {
		t.Fatalf("%v %v", inv, err)
	}
}

func TestUntransformFailuresBlockTheCommand(t *testing.T) {
	for _, src := range []string{
		`function untransform(m) { throw new Error("no") }`,
		`function untransform(m) { while (true) {} }`,
		`function untransform(m) { return null }`,
		`function untransform(m) { m.value = NaN; return m }`,
		`function untransform(m) { return 7 }`,
	} {
		p := mustCompile(t, StepConfig{Kind: KindScript, Source: "function transform(m) { return m }\n" + src})
		out, err := p.Inverse(Msg{"value": 50.0})
		var ep *ErrPipeline
		if !errors.As(err, &ep) || out != nil {
			t.Errorf("%s: want an ErrPipeline and no command, got %v %v", src, out, err)
		}
	}
}

func TestBuiltinInverseFailures(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindScale, Mul: ptr(0.1)})
	for _, m := range []Msg{{"value": "50"}, {"setpoint": 50.0}} {
		if _, err := p.Inverse(m); err == nil {
			t.Errorf("%v: want an error", m)
		}
	}
}

func TestScriptCompileErrors(t *testing.T) {
	compileErr(t, "transform", StepConfig{Kind: KindScript, Source: `function other() {}`})
	compileErr(t, "syntax", StepConfig{Kind: KindScript, Source: `function transform( {`})
	compileErr(t, "source exceeds", StepConfig{Kind: KindScript, Source: "function transform(m){return m}//" + strings.Repeat("x", MaxScriptSource)})
	compileErr(t, "load", StepConfig{Kind: KindScript, Source: `throw new Error("top")`})
	compileErr(t, "load", StepConfig{Kind: KindScript, Source: `var s = "x".repeat(1 << 30); function transform(m){return m}`})
}

// ---- pipeline ----

func TestStepOrderMatters(t *testing.T) {
	scaleThenClamp := mustCompile(t, StepConfig{Kind: KindScale, Mul: ptr(10.0)}, StepConfig{Kind: KindClamp, Min: ptr(0.0), Max: ptr(50.0)})
	clampThenScale := mustCompile(t, StepConfig{Kind: KindClamp, Min: ptr(0.0), Max: ptr(50.0)}, StepConfig{Kind: KindScale, Mul: ptr(10.0)})
	a, _ := run(t, scaleThenClamp, Msg{"value": 10.0}, Context{})
	b, _ := run(t, clampThenScale, Msg{"value": 10.0}, Context{})
	if num(t, a, "value") != 50 || num(t, b, "value") != 100 {
		t.Fatalf("%v %v", a, b)
	}
}

func TestInverseRunsInReverseOrder(t *testing.T) {
	// forward: (v × 2) + 0 then C→F; inverse must undo F→C first, then ÷2
	p := mustCompile(t, StepConfig{Kind: KindScale, Mul: ptr(2.0)}, StepConfig{Kind: KindUnit, From: "C", To: "F"},
		StepConfig{Kind: KindRound, Decimals: ptr(1)})
	out, _ := run(t, p, Msg{"value": 10.0}, Context{})
	near(t, num(t, out, "value"), 68)
	back, err := p.Inverse(out)
	if err != nil {
		t.Fatal(err)
	}
	near(t, num(t, back, "value"), 10)
	if got := p.StepsInvertible(); len(got) != 3 || !got[0] || !got[1] || got[2] {
		t.Fatal(got)
	}
	if !p.Invertible() || !p.IsInvertible() {
		t.Fatal("scale and unit invert; round only loses precision")
	}
}

func TestPickIsNotInvertible(t *testing.T) {
	p := mustCompile(t, StepConfig{Kind: KindPick, Path: "t"}, StepConfig{Kind: KindScale, Mul: ptr(2.0)})
	if p.IsInvertible() || !p.Invertible() || p.StepIsInvertible(0) {
		t.Fatal("pick can't be inverted, scale can")
	}
}

func TestCompileJSONRejectsUnknownKeysAndLimits(t *testing.T) {
	if _, err := CompileJSON(1, json.RawMessage(`[{"kind":"scale","mul":2,"feild":"raw"}]`)); err == nil ||
		!strings.Contains(err.Error(), "feild") {
		t.Fatalf("a typo must be rejected: %v", err)
	}
	if p, err := CompileJSON(1, json.RawMessage(` [] `)); err != nil || p.HasSteps() {
		t.Fatal(p, err)
	}
	steps := make([]StepConfig, MaxSteps+1)
	for i := range steps {
		steps[i] = StepConfig{Kind: KindScale, Mul: ptr(1.0)}
	}
	compileErr(t, "at most 32 steps", steps...)
	big := map[string]json.RawMessage{}
	for i := 0; i <= MaxTableEntries; i++ {
		big[strings.Repeat("k", i+1)] = json.RawMessage(`1`)
	}
	compileErr(t, "at most 256", StepConfig{Kind: KindMap, Table: big})
	compileErr(t, `unknown kind "nope"`, StepConfig{Kind: "nope"})
}

func TestNilAndEmptyPipelines(t *testing.T) {
	var p *Pipeline
	m := Msg{"value": 1.0}
	if out, res, _, err := p.ApplyWithContext(m, Context{}); err != nil || res != RunOK || out["value"] != 1.0 {
		t.Fatal(out, res, err)
	}
	if out, err := p.Inverse(m); err != nil || out["value"] != 1.0 {
		t.Fatal(out, err)
	}
	if p.HasSteps() || p.Invertible() || p.StepsInvertible() != nil {
		t.Fatal("nil pipeline")
	}
}

func TestWrapScalar(t *testing.T) {
	for _, v := range []any{21.5, true, "ON", nil, []any{1.0}} {
		m, wrapped := WrapScalar(v)
		if !wrapped || len(m) != 1 {
			t.Errorf("%v: %v %v", v, m, wrapped)
		}
	}
	if m, wrapped := WrapScalar(map[string]any{"value": 1.0}); wrapped || m["value"] != 1.0 {
		t.Fatal(m, wrapped)
	}
}

// ---- benchmarks ----

func BenchmarkNoPipeline(b *testing.B) {
	var p *Pipeline
	m := Msg{"value": 1.0}
	for b.Loop() {
		if p.HasSteps() {
			b.Fatal("no steps")
		}
		_, _, _, _ = p.ApplyWithContext(m, Context{})
	}
}

// Five steps that all run (scale, unit, round, clamp, deadband passing).
func BenchmarkFiveBuiltinSteps(b *testing.B) {
	p, err := Compile(1, []StepConfig{
		{Kind: KindScale, Mul: ptr(0.09775)},
		{Kind: KindUnit, From: "C", To: "F"},
		{Kind: KindRound, Decimals: ptr(1)},
		{Kind: KindClamp, Min: ptr(-100.0), Max: ptr(1000.0)},
		{Kind: KindDeadband, Abs: ptr(0.5)},
	})
	if err != nil {
		b.Fatal(err)
	}
	m := Msg{"value": 512.0}
	ctx := Context{Last: Msg{"value": 0.0}, Time: time.Now()}
	for b.Loop() {
		if _, res, _, err := p.ApplyWithContext(m, ctx); err != nil || res != RunOK {
			b.Fatal(res, err)
		}
	}
}

func BenchmarkScriptStep(b *testing.B) {
	p, err := Compile(1, []StepConfig{{Kind: KindScript, Source: `function transform(m) { m.value = m.value * 2; return m }`}})
	if err != nil {
		b.Fatal(err)
	}
	m := Msg{"value": 21.5}
	for b.Loop() {
		if _, _, _, err := p.Apply(m, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// A realistic script: dew point from temperature and humidity.
func BenchmarkScriptDewPoint(b *testing.B) {
	p, err := Compile(1, []StepConfig{{Kind: KindScript, Source: `function transform(m) {
		var a = 17.27, bb = 237.7;
		var g = (a * m.temperature) / (bb + m.temperature) + Math.log(m.humidity / 100);
		m.dew_point = Math.round((bb * g) / (a - g) * 10) / 10;
		return m }`}})
	if err != nil {
		b.Fatal(err)
	}
	m := Msg{"temperature": 21.5, "humidity": 48.0}
	for b.Loop() {
		if _, _, _, err := p.Apply(m, nil); err != nil {
			b.Fatal(err)
		}
	}
}
