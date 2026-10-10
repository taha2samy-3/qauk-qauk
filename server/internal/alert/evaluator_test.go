package alert

import (
	"testing"

	"github.com/google/uuid"
	"github.com/taha2samy/quackquack/server/internal/store"
)

func TestAlertEvaluatorAboveCondition(t *testing.T) {
	ev := NewEvaluator()
	ruleID := uuid.New()
	elemID := uuid.New()
	rule := store.ElementAlertRule{
		ID:         ruleID,
		ElementID:  elemID,
		Name:       "High Temp",
		Condition:  "above",
		Threshold:  35.0,
		Hysteresis: 1.0,
		Severity:   "critical",
	}

	// 1. Below threshold: no trigger
	trig, recov := ev.CheckCondition(rule, 34.0)
	if trig || recov {
		t.Fatalf("expected no trigger, got trig=%v recov=%v", trig, recov)
	}

	// 2. Crosses above 35: triggers
	trig, recov = ev.CheckCondition(rule, 35.5)
	if !trig || recov {
		t.Fatalf("expected trigger, got trig=%v recov=%v", trig, recov)
	}

	// 3. Still above 35: doesn't re-trigger (already active)
	trig, recov = ev.CheckCondition(rule, 36.0)
	if trig || recov {
		t.Fatalf("expected no new trigger while active, got trig=%v recov=%v", trig, recov)
	}

	// 4. Drops to 34.5 (above threshold - hysteresis of 34.0): stays in alarm
	trig, recov = ev.CheckCondition(rule, 34.5)
	if trig || recov {
		t.Fatalf("expected hysteresis to hold alarm, got trig=%v recov=%v", trig, recov)
	}

	// 5. Drops below 34.0: recovers
	trig, recov = ev.CheckCondition(rule, 33.8)
	if trig || !recov {
		t.Fatalf("expected recovery, got trig=%v recov=%v", trig, recov)
	}
}

func TestAlertEvaluatorBelowCondition(t *testing.T) {
	ev := NewEvaluator()
	ruleID := uuid.New()
	rule := store.ElementAlertRule{
		ID:         ruleID,
		Condition:  "below",
		Threshold:  10.0,
		Hysteresis: 0.5,
		Severity:   "warning",
	}

	// Above 10: normal
	trig, _ := ev.CheckCondition(rule, 15.0)
	if trig {
		t.Fatalf("expected normal")
	}

	// Drops to 9.0: triggers
	trig, _ = ev.CheckCondition(rule, 9.0)
	if !trig {
		t.Fatalf("expected trigger")
	}

	// Recovers when it reaches 10.5
	_, recov := ev.CheckCondition(rule, 10.6)
	if !recov {
		t.Fatalf("expected recovery")
	}
}

func TestAlertEvaluatorOutsideRangeCondition(t *testing.T) {
	ev := NewEvaluator()
	ruleID := uuid.New()
	max := 30.0
	rule := store.ElementAlertRule{
		ID:           ruleID,
		Condition:    "outside_range",
		Threshold:    10.0,
		ThresholdMax: &max,
		Hysteresis:   1.0,
		Severity:     "warning",
	}

	// 1. Value inside range [10, 30]: normal
	trig, recov := ev.CheckCondition(rule, 20.0)
	if trig || recov {
		t.Fatalf("expected normal in-range, got trig=%v recov=%v", trig, recov)
	}

	// 2. Value drops below min (8.0 < 10.0): triggers
	trig, recov = ev.CheckCondition(rule, 8.0)
	if !trig || recov {
		t.Fatalf("expected trigger below min, got trig=%v recov=%v", trig, recov)
	}

	// 3. Value enters between 10.0 and 11.0 (within hysteresis band): stays in alarm
	trig, recov = ev.CheckCondition(rule, 10.5)
	if trig || recov {
		t.Fatalf("expected hysteresis hold, got trig=%v recov=%v", trig, recov)
	}

	// 4. Value reaches 12.0 (>= 10 + 1.0 and <= 30 - 1.0): recovers
	trig, recov = ev.CheckCondition(rule, 12.0)
	if trig || !recov {
		t.Fatalf("expected recovery in safe band, got trig=%v recov=%v", trig, recov)
	}

	// 5. Value rises above max (32.0 > 30.0): triggers
	trig, recov = ev.CheckCondition(rule, 32.0)
	if !trig || recov {
		t.Fatalf("expected trigger above max, got trig=%v recov=%v", trig, recov)
	}

	// 6. Drops back to 28.0 (<= 30 - 1.0): recovers
	trig, recov = ev.CheckCondition(rule, 28.0)
	if trig || !recov {
		t.Fatalf("expected recovery after max, got trig=%v recov=%v", trig, recov)
	}
}

func TestAlertEvaluatorEqualsCondition(t *testing.T) {
	ev := NewEvaluator()
	ruleID := uuid.New()
	rule := store.ElementAlertRule{
		ID:        ruleID,
		Condition: "equals",
		Threshold: 0.0, // e.g. error code 0 or offline state
		Severity:  "critical",
	}

	// 1. Initial non-matching: normal
	trig, recov := ev.CheckCondition(rule, 1.0)
	if trig || recov {
		t.Fatalf("expected no trigger")
	}

	// 2. Matches threshold: triggers
	trig, recov = ev.CheckCondition(rule, 0.0)
	if !trig || recov {
		t.Fatalf("expected trigger on equals")
	}

	// 3. Stays matching: does not re-trigger
	trig, recov = ev.CheckCondition(rule, 0.0)
	if trig || recov {
		t.Fatalf("expected no re-trigger")
	}

	// 4. Value changes away from threshold: recovers
	trig, recov = ev.CheckCondition(rule, 1.0)
	if trig || !recov {
		t.Fatalf("expected recovery when value leaves equals")
	}
}

