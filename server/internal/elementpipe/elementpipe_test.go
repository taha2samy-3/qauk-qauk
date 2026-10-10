package elementpipe

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// ---- helpers ----

func msg(kv ...any) Msg {
	m := make(Msg, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func mustCompile(t *testing.T, steps []StepConfig) *Pipeline {
	t.Helper()
	p, err := Compile(1, steps)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return p
}

func apply(t *testing.T, p *Pipeline, m Msg, last Msg) (Msg, RunResult, FilterReason) {
	t.Helper()
	out, res, fr, err := p.Apply(m, last)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	return out, res, fr
}

func inverse(t *testing.T, p *Pipeline, m Msg) Msg {
	t.Helper()
	out, err := p.Inverse(m)
	if err != nil {
		t.Fatalf("Inverse error: %v", err)
	}
	return out
}

func assertNum(t *testing.T, m Msg, field string, want float64) {
	t.Helper()
	v, ok := getNumericField(m, field)
	if !ok {
		t.Fatalf("field %q not numeric, got %v", field, m[field])
	}
	if math.Abs(v-want) > 1e-9 {
		t.Errorf("field %q: got %v, want %v", field, v, want)
	}
}

func assertStr(t *testing.T, m Msg, field string, want string) {
	t.Helper()
	got := getStringField(m, field)
	if got != want {
		t.Errorf("field %q: got %q, want %q", field, got, want)
	}
}

// ---- pick ----

func TestPickValue(t *testing.T) {
	p := mustCompile(t, []StepConfig{{Kind: KindPick, Path: "temperature"}})
	out, res, _ := apply(t, p, msg("temperature", 21.5, "humidity", 40.0), nil)
	if res != RunOK {
		t.Fatal("expected RunOK")
	}
	assertNum(t, out, "value", 21.5)
	if _, ok := out["humidity"]; ok {
		t.Error("humidity should not be in output")
	}
}

func TestPickMissing(t *testing.T) {
	p := mustCompile(t, []StepConfig{{Kind: KindPick, Path: "temperature"}})
	_, _, _, err := p.Apply(msg("humidity", 40.0), nil)
	if err == nil {
		t.Fatal("expected error for missing field")
	}
}

// ---- scale ----

func TestScale(t *testing.T) {
	mul := 2.0
	p := mustCompile(t, []StepConfig{{Kind: KindScale, Mul: &mul, Add: 1}})
	out, _, _ := apply(t, p, msg("value", 5.0), nil)
	assertNum(t, out, "value", 11.0) // 5*2+1
}

func TestScaleInverse(t *testing.T) {
	mul := 2.0
	p := mustCompile(t, []StepConfig{{Kind: KindScale, Mul: &mul, Add: 1}})
	out := inverse(t, p, msg("value", 11.0))
	assertNum(t, out, "value", 5.0) // (11-1)/2
}

func TestScaleMulZeroRejected(t *testing.T) {
	mul := 0.0
	_, err := Compile(1, []StepConfig{{Kind: KindScale, Mul: &mul}})
	if err == nil {
		t.Fatal("expected error for mul=0")
	}
}

func TestScaleCustomField(t *testing.T) {
	mul := 0.09775
	p := mustCompile(t, []StepConfig{{Kind: KindScale, Field: "raw", Mul: &mul}})
	out, _, _ := apply(t, p, msg("raw", 1023.0), nil)
	assertNum(t, out, "raw", 1023.0*0.09775)
}

// ---- unit ----

func TestUnitCToF(t *testing.T) {
	p := mustCompile(t, []StepConfig{{Kind: KindUnit, From: "C", To: "F"}})
	out, _, _ := apply(t, p, msg("value", 0.0), nil)
	assertNum(t, out, "value", 32.0)
}

func TestUnitInverse(t *testing.T) {
	p := mustCompile(t, []StepConfig{{Kind: KindUnit, From: "C", To: "F"}})
	out := inverse(t, p, msg("value", 32.0))
	assertNum(t, out, "value", 0.0)
}

func TestUnitUnknown(t *testing.T) {
	_, err := Compile(1, []StepConfig{{Kind: KindUnit, From: "furlongs", To: "parsecs"}})
	if err == nil {
		t.Fatal("expected error for unknown unit")
	}
}

// ---- round ----

func TestRound(t *testing.T) {
	d := 1
	p := mustCompile(t, []StepConfig{{Kind: KindRound, Decimals: &d}})
	out, _, _ := apply(t, p, msg("value", 21.45678), nil)
	assertNum(t, out, "value", 21.5)
}

func TestRoundHalfAwayFromZero(t *testing.T) {
	d := 0
	p := mustCompile(t, []StepConfig{{Kind: KindRound, Decimals: &d}})
	out, _, _ := apply(t, p, msg("value", 2.5), nil)
	assertNum(t, out, "value", 3.0)
}

// ---- clamp ----

func TestClamp(t *testing.T) {
	lo, hi := 0.0, 100.0
	p := mustCompile(t, []StepConfig{{Kind: KindClamp, Min: &lo, Max: &hi}})
	for in, want := range map[float64]float64{-5: 0, 50: 50, 200: 100} {
		out, _, _ := apply(t, p, msg("value", in), nil)
		assertNum(t, out, "value", want)
	}
}

func TestClampMinGtMax(t *testing.T) {
	lo, hi := 10.0, 5.0
	_, err := Compile(1, []StepConfig{{Kind: KindClamp, Min: &lo, Max: &hi}})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---- map ----

func TestMap(t *testing.T) {
	raw1, _ := json.Marshal(1)
	raw0, _ := json.Marshal(0)
	p := mustCompile(t, []StepConfig{
		{Kind: KindMap, Table: map[string]json.RawMessage{"ON": raw1, "OFF": raw0}},
	})
	out, _, _ := apply(t, p, msg("value", "ON"), nil)
	assertNum(t, out, "value", 1)
}

func TestMapInverse(t *testing.T) {
	raw1, _ := json.Marshal(1)
	raw0, _ := json.Marshal(0)
	p := mustCompile(t, []StepConfig{
		{Kind: KindMap, Table: map[string]json.RawMessage{"ON": raw1, "OFF": raw0}},
	})
	out := inverse(t, p, msg("value", 1.0))
	assertStr(t, out, "value", "ON")
}

func TestMapNotOneToOne(t *testing.T) {
	raw1, _ := json.Marshal(1)
	p := mustCompile(t, []StepConfig{
		{Kind: KindMap, Table: map[string]json.RawMessage{"ON": raw1, "YES": raw1}},
	})
	// Inverse should not panic, just skip
	out := inverse(t, p, msg("value", "1"))
	if out["value"] == nil {
		t.Error("should have original value")
	}
}

func TestMapUnmappedNoDefault(t *testing.T) {
	raw1, _ := json.Marshal(1)
	p := mustCompile(t, []StepConfig{
		{Kind: KindMap, Table: map[string]json.RawMessage{"ON": raw1}},
	})
	_, _, _, err := p.Apply(msg("value", "OFF"), nil)
	if err == nil {
		t.Fatal("expected error for unmapped value")
	}
}

func TestMapDefault(t *testing.T) {
	raw1, _ := json.Marshal(1)
	rawDef, _ := json.Marshal(-1)
	def := json.RawMessage(rawDef)
	p := mustCompile(t, []StepConfig{
		{Kind: KindMap, Table: map[string]json.RawMessage{"ON": raw1}, Default: &def},
	})
	out, res, _ := apply(t, p, msg("value", "UNKNOWN"), nil)
	if res != RunOK {
		t.Fatal("expected OK with default")
	}
	assertNum(t, out, "value", -1)
}

// ---- deadband ----

func TestDeadbandPassesFirst(t *testing.T) {
	abs := 0.5
	p := mustCompile(t, []StepConfig{{Kind: KindDeadband, Abs: &abs}})
	_, res, _ := apply(t, p, msg("value", 10.0), nil)
	if res != RunOK {
		t.Error("first message without last should always pass")
	}
}

func TestDeadbandFilters(t *testing.T) {
	abs := 0.5
	p := mustCompile(t, []StepConfig{{Kind: KindDeadband, Abs: &abs}})
	_, res, fr := apply(t, p, msg("value", 10.2), msg("value", 10.0))
	if res != RunFiltered || fr != FilterDeadband {
		t.Errorf("expected filtered, got %v %v", res, fr)
	}
}

func TestDeadbandPasses(t *testing.T) {
	abs := 0.5
	p := mustCompile(t, []StepConfig{{Kind: KindDeadband, Abs: &abs}})
	_, res, _ := apply(t, p, msg("value", 10.6), msg("value", 10.0))
	if res != RunOK {
		t.Error("should pass: delta=0.6 > threshold=0.5")
	}
}

func TestDeadbandPct(t *testing.T) {
	pct := 5.0 // 5%
	p := mustCompile(t, []StepConfig{{Kind: KindDeadband, Pct: &pct}})
	// 10 * 5% = 0.5, delta = 0.3 → filter
	_, res, _ := apply(t, p, msg("value", 10.3), msg("value", 10.0))
	if res != RunFiltered {
		t.Error("should be filtered: change < 5%")
	}
	// delta = 0.6 → pass
	_, res2, _ := apply(t, p, msg("value", 10.6), msg("value", 10.0))
	if res2 != RunOK {
		t.Error("should pass: change > 5%")
	}
}

func TestDeadbandPassesNonNumeric(t *testing.T) {
	abs := 0.5
	p := mustCompile(t, []StepConfig{{Kind: KindDeadband, Abs: &abs}})
	_, res, _ := apply(t, p, msg("value", "hello"), msg("value", "world"))
	if res != RunOK {
		t.Error("deadband should pass non-numeric values through")
	}
}

// ---- drop_if ----

func TestDropIf(t *testing.T) {
	val := 0.0
	p := mustCompile(t, []StepConfig{{Kind: KindDropIf, Op: "<", Value: &val}})
	_, res, fr := apply(t, p, msg("value", -1.0), nil)
	if res != RunFiltered || fr != FilterDropIf {
		t.Error("expected filtered")
	}
	_, res2, _ := apply(t, p, msg("value", 1.0), nil)
	if res2 != RunOK {
		t.Error("expected OK")
	}
}

func TestDropIfNonNumericPassesThrough(t *testing.T) {
	val := 0.0
	p := mustCompile(t, []StepConfig{{Kind: KindDropIf, Op: "<", Value: &val}})
	_, res, _ := apply(t, p, msg("value", "hello"), nil)
	if res != RunOK {
		t.Error("drop_if should pass non-numeric through")
	}
}

// ---- script ----

func TestScriptTransform(t *testing.T) {
	p := mustCompile(t, []StepConfig{{
		Kind:   KindScript,
		Source: `function transform(msg, ctx) { msg.value = msg.value * 2; return msg; }`,
	}})
	out, res, _ := apply(t, p, msg("value", 5.0), nil)
	if res != RunOK {
		t.Fatal("expected OK")
	}
	assertNum(t, out, "value", 10.0)
}

func TestScriptReturnNull(t *testing.T) {
	p := mustCompile(t, []StepConfig{{
		Kind:   KindScript,
		Source: `function transform(msg, ctx) { return null; }`,
	}})
	_, res, fr := apply(t, p, msg("value", 5.0), nil)
	if res != RunFiltered || fr != FilterScript {
		t.Error("expected filtered")
	}
}

func TestScriptThrow(t *testing.T) {
	p := mustCompile(t, []StepConfig{{
		Kind:   KindScript,
		Source: `function transform(msg, ctx) { throw new Error("oops"); }`,
	}})
	_, _, _, err := p.Apply(msg("value", 5.0), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestScriptInfiniteLoop(t *testing.T) {
	p := mustCompile(t, []StepConfig{{
		Kind:   KindScript,
		Source: `function transform(msg, ctx) { while(true){} return msg; }`,
	}})
	start := time.Now()
	_, _, _, err := p.Apply(msg("value", 5.0), nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 5*time.Second {
		t.Errorf("should have timed out in ~20ms, took %v", elapsed)
	}
}

func TestScriptNaNRejected(t *testing.T) {
	p := mustCompile(t, []StepConfig{{
		Kind:   KindScript,
		Source: `function transform(msg, ctx) { msg.value = NaN; return msg; }`,
	}})
	_, _, _, err := p.Apply(msg("value", 5.0), nil)
	if err == nil {
		t.Fatal("expected error: NaN rejected")
	}
}

func TestScriptRequireUndefined(t *testing.T) {
	p := mustCompile(t, []StepConfig{{
		Kind:   KindScript,
		Source: `function transform(msg, ctx) { var x = require("fs"); return msg; }`,
	}})
	_, _, _, err := p.Apply(msg("value", 5.0), nil)
	if err == nil {
		t.Fatal("expected error: require is not defined")
	}
}

func TestScriptUntransform(t *testing.T) {
	p := mustCompile(t, []StepConfig{{
		Kind: KindScript,
		Source: `
		function transform(msg, ctx) { msg.value = msg.value * 2; return msg; }
		function untransform(msg, ctx) { msg.value = msg.value / 2; return msg; }
		`,
	}})
	out := inverse(t, p, msg("value", 10.0))
	assertNum(t, out, "value", 5.0)
}

func TestScriptMissingTransform(t *testing.T) {
	_, err := Compile(1, []StepConfig{{
		Kind:   KindScript,
		Source: `function untransform(msg, ctx) { return msg; }`,
	}})
	if err == nil {
		t.Fatal("expected error: transform not defined")
	}
}

// ---- order of steps ----

func TestStepOrder(t *testing.T) {
	// scale then clamp: 1023 * 0.09775 = 99.98..., then clamp 0..100 → 99.98
	mul := 0.09775
	lo, hi := 0.0, 100.0
	d := 1
	p := mustCompile(t, []StepConfig{
		{Kind: KindScale, Field: "value", Mul: &mul},
		{Kind: KindRound, Field: "value", Decimals: &d},
		{Kind: KindClamp, Field: "value", Min: &lo, Max: &hi},
	})
	out, _, _ := apply(t, p, msg("value", 1023.0), nil)
	// 1023 * 0.09775 = 99.97... → round to 1 decimal → 100.0 → clamp → 100.0
	assertNum(t, out, "value", 100.0)
}

func TestReverseOrder(t *testing.T) {
	mul1 := 2.0
	mul2 := 10.0
	// Forward: v * 2 + 1 → result * 10 + 0
	// Inverse: undo step2: (v) / 10 = r, then undo step1: (r - 1) / 2
	p := mustCompile(t, []StepConfig{
		{Kind: KindScale, Mul: &mul1, Add: 1},
		{Kind: KindScale, Mul: &mul2},
	})
	// Forward: 5 * 2 + 1 = 11; 11 * 10 = 110
	fwd, _, _ := apply(t, p, msg("value", 5.0), nil)
	assertNum(t, fwd, "value", 110.0)
	// Inverse: 110 / 10 = 11; (11 - 1) / 2 = 5
	inv := inverse(t, p, msg("value", 110.0))
	assertNum(t, inv, "value", 5.0)
}

// ---- no pipeline ----

func TestNilPipelineNoop(t *testing.T) {
	var p *Pipeline
	m := msg("value", 42.0)
	out, res, _, err := p.Apply(m, nil)
	if err != nil || res != RunOK {
		t.Fatal("nil pipeline should be noop")
	}
	assertNum(t, out, "value", 42.0)
}

func TestEmptyPipelineNoop(t *testing.T) {
	p := mustCompile(t, []StepConfig{})
	m := msg("value", 42.0)
	out, res, _, _ := p.Apply(m, nil)
	if res != RunOK {
		t.Fatal("expected OK")
	}
	assertNum(t, out, "value", 42.0)
}

// ---- validation ----

func TestTooManySteps(t *testing.T) {
	steps := make([]StepConfig, MaxSteps+1)
	for i := range steps {
		d := 1
		steps[i] = StepConfig{Kind: KindRound, Decimals: &d}
	}
	_, err := Compile(1, steps)
	if err == nil {
		t.Fatal("expected error: too many steps")
	}
}

// ---- benchmarks ----

func BenchmarkNoPipeline(b *testing.B) {
	var p *Pipeline
	m := msg("value", 21.5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = p.Apply(m, nil)
	}
}

func BenchmarkFiveBuiltinSteps(b *testing.B) {
	mul := 0.09775
	lo, hi := 0.0, 100.0
	d := 1
	abs := 0.2
	val := 0.0
	p, err := Compile(1, []StepConfig{
		{Kind: KindScale, Mul: &mul},
		{Kind: KindRound, Decimals: &d},
		{Kind: KindClamp, Min: &lo, Max: &hi},
		{Kind: KindDeadband, Abs: &abs},
		{Kind: KindDropIf, Op: "<", Value: &val},
	})
	if err != nil {
		b.Fatal(err)
	}
	m := msg("value", 512.0)
	last := msg("value", 50.0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = p.Apply(m, last)
	}
}

func BenchmarkScriptStep(b *testing.B) {
	p, err := Compile(1, []StepConfig{{
		Kind:   KindScript,
		Source: `function transform(msg, ctx) { msg.value = msg.value * 2; return msg; }`,
	}})
	if err != nil {
		b.Fatal(err)
	}
	m := msg("value", 21.5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = p.Apply(m, nil)
	}
}
