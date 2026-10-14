package alert

import (
	"encoding/json"
	"testing"

	"github.com/taha2samy/quackquack/server/internal/store"
)

func rule(cond string, t float64, h float64) store.ElementAlertRule {
	return store.ElementAlertRule{Condition: cond, Threshold: t, Hysteresis: h}
}

// run feeds values and returns the firing state after each.
func run(r store.ElementAlertRule, values ...float64) []bool {
	firing := false
	out := make([]bool, len(values))
	for i, v := range values {
		firing = Next(r, firing, v)
		out[i] = firing
	}
	return out
}

func eq(t *testing.T, got []bool, want ...bool) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestAboveWithHysteresis(t *testing.T) {
	// fires above 30; resolves only at or below 28
	eq(t, run(rule("above", 30, 2), 29, 31, 29, 28.5, 28, 31), false, true, true, true, false, true)
}

func TestBelowWithHysteresis(t *testing.T) {
	eq(t, run(rule("below", 5, 1), 6, 4, 5.5, 6, 4), false, true, true, false, true)
}

func TestOutsideRange(t *testing.T) {
	max := 30.0
	r := rule("outside_range", 10, 2)
	r.ThresholdMax = &max
	// out, back inside but within hysteresis of the edge, well inside, out high
	eq(t, run(r, 20, 9, 11, 15, 31, 29, 27), false, true, true, false, true, true, false)
}

func TestEquals(t *testing.T) {
	eq(t, run(rule("equals", 1, 0), 0, 1, 1, 0), false, true, true, false)
	// with hysteresis it stays firing within the band
	eq(t, run(rule("equals", 50, 5), 50, 53, 56), true, true, false)
}

func TestUnknownConditionKeepsState(t *testing.T) {
	if Next(rule("nope", 1, 0), true, 100) != true || Next(rule("nope", 1, 0), false, 100) != false {
		t.Fatal("an unknown condition must not change the state")
	}
}

func TestNumber(t *testing.T) {
	for in, want := range map[string]float64{`21.5`: 21.5, `{"value": 3}`: 3, `true`: 1, `{"value": false}`: 0} {
		if v, ok := Number(json.RawMessage(in)); !ok || v != want {
			t.Errorf("%s: %v %v", in, v, ok)
		}
	}
	for _, in := range []string{`"21"`, `{"temperature": 3}`, `null`, `{"value": "x"}`, `nope`} {
		if _, ok := Number(json.RawMessage(in)); ok {
			t.Errorf("%s must not be evaluated", in)
		}
	}
}

func TestDescribe(t *testing.T) {
	max := 2.5
	r := rule("outside_range", 1, 0)
	r.ThresholdMax = &max
	if got := describe(r); got != "outside 1–2.5" {
		t.Fatal(got)
	}
}
