// Package historytest is the contract every history driver must pass:
//
//	func TestContract(t *testing.T) { historytest.Run(t, func(t *testing.T) history.Store { … }) }
//
// Tests use fresh element ids, so they can share one database.
package historytest

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/history"
)

// Run runs the whole contract against stores made by open (already migrated).
func Run(t *testing.T, open func(t *testing.T) history.Store) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(*testing.T, history.Store)
	}{
		{"RoundTrip", roundTrip},
		{"AppendIsIdempotent", appendIdempotent},
		{"EventsOrderAndLimit", eventsOrderAndLimit},
		{"LastPerElement", lastPerElement},
		{"BucketsValue", bucketsValue},
		{"BucketsFields", bucketsFields},
		{"BucketsAlignment", bucketsAlignment},
		{"BucketsSubMinute", bucketsSubMinute},
		{"BigBatch", bigBatch},
		{"Reset", reset}, // last: it wipes the database
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, open(t))
		})
	}
}

// base is a fixed, minute-aligned time well inside any retention window.
func base() time.Time {
	return time.Now().UTC().Add(-48 * time.Hour).Truncate(24 * time.Hour)
}

type gen struct {
	el, dev uuid.UUID
}

func newGen() gen { return gen{el: uuid.New(), dev: uuid.New()} }

func (g gen) ev(at time.Time, source, payload string) history.Event {
	id, _ := uuid.NewV7()
	ev := history.Event{
		Time: at.UTC().Truncate(time.Millisecond), ID: id, ElementID: g.el, DeviceID: g.dev, Source: source,
		ActorID: g.dev.String(), ActorName: "dev", Payload: json.RawMessage(payload),
	}
	if source == history.SourceUser {
		ev.ActorID, ev.ActorName = "7", "alice"
	}
	ev.Value = history.NumericValue(ev.Payload)
	return ev
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func mustAppend(t *testing.T, s history.Store, evs ...history.Event) int {
	t.Helper()
	n, err := s.Append(ctx(t), evs)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	return n
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func roundTrip(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(10*time.Minute + 123*time.Millisecond)
	cts := at.Add(-2 * time.Second)
	a := g.ev(at, history.SourceDevice, `{"temperature": 21.5, "label": "غرفة 🦆", "nested": {"ok": true}, "list": [1, "x"]}`)
	a.ClientTS = &cts
	b := g.ev(at.Add(time.Second), history.SourceUser, `{"value": 1}`)
	c := g.ev(at.Add(2*time.Second), history.SourceDevice, `"bare string"`)
	if n := mustAppend(t, s, a, b, c); n != 3 {
		t.Fatalf("append returned %d new, want 3", n)
	}
	got, err := s.Events(ctx(t), history.EventQuery{ElementID: g.el, From: at.Add(-time.Minute), To: at.Add(time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []history.Event{a, b, c}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.Time.Equal(w.Time) || g.ID != w.ID || g.ElementID != w.ElementID || g.DeviceID != w.DeviceID ||
			g.Source != w.Source || g.ActorID != w.ActorID || g.ActorName != w.ActorName {
			t.Fatalf("event %d metadata:\n got %+v\nwant %+v", i, g, w)
		}
		if !jsonEqual(g.Payload, w.Payload) {
			t.Fatalf("event %d payload: got %s want %s", i, g.Payload, w.Payload)
		}
		if (g.Value == nil) != (w.Value == nil) || (g.Value != nil && *g.Value != *w.Value) {
			t.Fatalf("event %d value: got %v want %v", i, g.Value, w.Value)
		}
		if (g.ClientTS == nil) != (w.ClientTS == nil) || (g.ClientTS != nil && !g.ClientTS.Equal(*w.ClientTS)) {
			t.Fatalf("event %d client_ts: got %v want %v", i, g.ClientTS, w.ClientTS)
		}
	}
	// [From, To): an event exactly at To is excluded, one exactly at From included
	got, _ = s.Events(ctx(t), history.EventQuery{ElementID: g.el, From: b.Time, To: c.Time, Limit: 10})
	if len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("half-open range: got %d events", len(got))
	}
}

func appendIdempotent(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(time.Hour)
	var evs []history.Event
	for i := range 10 {
		evs = append(evs, g.ev(at.Add(time.Duration(i)*time.Second), history.SourceDevice, fmt.Sprintf(`{"value": %d}`, i)))
	}
	if n := mustAppend(t, s, evs...); n != 10 {
		t.Fatalf("first append: %d new", n)
	}
	// redelivery of the whole batch, of a part, and a mix with new events
	if n := mustAppend(t, s, evs...); n != 0 {
		t.Fatalf("re-append: %d new, want 0", n)
	}
	extra := g.ev(at.Add(20*time.Second), history.SourceDevice, `{"value": 100}`)
	if n := mustAppend(t, s, append([]history.Event{extra}, evs[3:7]...)...); n != 1 {
		t.Fatalf("mixed append: %d new, want 1", n)
	}
	got, _ := s.Events(ctx(t), history.EventQuery{ElementID: g.el, From: at, To: at.Add(time.Minute), Limit: 100})
	if len(got) != 11 {
		t.Fatalf("stored %d events, want 11", len(got))
	}
	bs, err := s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: "value", From: at, To: at.Add(time.Minute), Step: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].N != 11 || *bs[0].Max != 100 || math.Abs(*bs[0].Avg-145.0/11) > 1e-9 {
		t.Fatalf("rollup must not count duplicates: %+v", dump(bs))
	}
}

func eventsOrderAndLimit(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(2 * time.Hour)
	var evs []history.Event
	for i := range 20 {
		// pairs share a millisecond: ties are ordered by the UUIDv7 id
		evs = append(evs, g.ev(at.Add(time.Duration(i/2)*time.Second), history.SourceDevice, fmt.Sprintf(`{"value": %d}`, i)))
	}
	// append out of order
	mustAppend(t, s, evs[10:]...)
	mustAppend(t, s, evs[:10]...)
	q := history.EventQuery{ElementID: g.el, From: at, To: at.Add(time.Hour), Limit: 5}
	oldest, err := s.Events(ctx(t), q)
	if err != nil {
		t.Fatal(err)
	}
	q.Newest = true
	newest, err := s.Events(ctx(t), q)
	if err != nil {
		t.Fatal(err)
	}
	if ids(oldest) != ids(evs[:5]) {
		t.Fatalf("oldest 5 in order:\n got %s\nwant %s", ids(oldest), ids(evs[:5]))
	}
	if ids(newest) != ids(evs[15:]) {
		t.Fatalf("newest 5, ascending:\n got %s\nwant %s", ids(newest), ids(evs[15:]))
	}
	other, _ := s.Events(ctx(t), history.EventQuery{ElementID: uuid.New(), From: at, To: at.Add(time.Hour), Limit: 5})
	if len(other) != 0 {
		t.Fatal("events leaked across elements")
	}
}

func lastPerElement(t *testing.T, s history.Store) {
	g1, g2, g3 := newGen(), newGen(), newGen()
	at := base().Add(3 * time.Hour)
	var all []history.Event
	var dev1 []history.Event
	for i := range 8 {
		e := g1.ev(at.Add(time.Duration(i)*time.Second), history.SourceDevice, fmt.Sprintf(`{"value": %d}`, i))
		dev1 = append(dev1, e)
		all = append(all, e)
		// user commands are never replayed
		all = append(all, g1.ev(at.Add(time.Duration(i)*time.Second+time.Millisecond), history.SourceUser, `{"value": -1}`))
	}
	old := g2.ev(at.Add(-30*24*time.Hour), history.SourceDevice, `{"value": 1}`)
	recent := g2.ev(at, history.SourceDevice, `{"value": 2}`)
	all = append(all, old, recent)
	mustAppend(t, s, all...)

	got, err := s.Last(ctx(t), []uuid.UUID{g1.el, g2.el, g3.el}, 3, at.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ids(got[g1.el]) != ids(dev1[5:]) {
		t.Fatalf("element 1: newest 3 device events oldest first:\n got %s\nwant %s", ids(got[g1.el]), ids(dev1[5:]))
	}
	if ids(got[g2.el]) != ids([]history.Event{recent}) {
		t.Fatalf("element 2: since must exclude older events: got %s", ids(got[g2.el]))
	}
	if _, ok := got[g3.el]; ok {
		t.Fatal("element without events must be absent")
	}
	if len(got) != 2 {
		t.Fatalf("got %d elements", len(got))
	}
	empty, err := s.Last(ctx(t), nil, 3, at)
	if err != nil || len(empty) != 0 {
		t.Fatalf("no ids: %v %v", empty, err)
	}
}

func bucketsValue(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(4 * time.Hour)
	mustAppend(t, s,
		g.ev(at.Add(5*time.Second), history.SourceDevice, `{"value": 10}`),
		g.ev(at.Add(10*time.Second), history.SourceDevice, `20`),                           // bare number
		g.ev(at.Add(70*time.Second), history.SourceDevice, `{"x": "2026-01-01", "y": 30}`), // chart shape
		g.ev(at.Add(80*time.Second), history.SourceDevice, `{"value": true}`),              // 1
		g.ev(at.Add(90*time.Second), history.SourceDevice, `{"value": "ON"}`),              // not numeric
		g.ev(at.Add(130*time.Second), history.SourceUser, `{"value": 5}`),                  // commands count too
	)
	bs, err := s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: "value", From: at, To: at.Add(10 * time.Minute), Step: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		bucket(at, 15, 10, 20, 2),
		bucket(at.Add(time.Minute), 15.5, 1, 30, 2),
		bucket(at.Add(2*time.Minute), 5, 5, 5, 1),
	}
	if fmt.Sprint(dump(bs)) != fmt.Sprint(want) {
		t.Fatalf("buckets:\n got %v\nwant %v", dump(bs), want)
	}
	// From/To bound the result
	bs, _ = s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: "value", From: at.Add(time.Minute), To: at.Add(2 * time.Minute), Step: time.Minute})
	if len(bs) != 1 || !bs[0].Time.Equal(at.Add(time.Minute)) {
		t.Fatalf("bounded: %v", dump(bs))
	}
}

func bucketsFields(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(5 * time.Hour)
	mustAppend(t, s,
		g.ev(at.Add(1*time.Second), history.SourceDevice, `{"temperature": 20, "gps": {"lat": 30.0}, "sensors": [{"t": 1}], "h": "40"}`),
		g.ev(at.Add(2*time.Second), history.SourceDevice, `{"temperature": 22, "gps": {"lat": 30.5}, "sensors": [{"t": 3}], "h": "x"}`),
		g.ev(at.Add(3*time.Second), history.SourceDevice, `{"temperature": 1e400}`), // out of range: skipped, not an error
	)
	for field, want := range map[string]string{
		"temperature":   bucket(at, 21, 20, 22, 2),
		"gps.lat":       bucket(at, 30.25, 30, 30.5, 2),
		"sensors[0].t":  bucket(at, 2, 1, 3, 2),
		"h":             bucket(at, 40, 40, 40, 1),
		"missing.field": "",
	} {
		bs, err := s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: field, From: at, To: at.Add(time.Hour), Step: time.Hour})
		if err != nil {
			t.Fatalf("%s: %v", field, err)
		}
		got := ""
		if len(bs) == 1 {
			got = dump(bs)[0]
		} else if len(bs) > 1 {
			got = fmt.Sprint(dump(bs))
		}
		if got != want {
			t.Errorf("field %s: got %q want %q", field, got, want)
		}
	}
}

func bucketsAlignment(t *testing.T, s history.Store) {
	g := newGen()
	day := base() // midnight UTC
	mustAppend(t, s,
		g.ev(day.Add(7*time.Minute), history.SourceDevice, `1`),
		g.ev(day.Add(13*time.Minute), history.SourceDevice, `2`),
		g.ev(day.Add(61*time.Minute), history.SourceDevice, `3`),
		g.ev(day.Add(25*time.Hour), history.SourceDevice, `4`),
	)
	q := func(step time.Duration, from time.Time) []string {
		bs, err := s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: "value", From: from, To: day.Add(48 * time.Hour), Step: step})
		if err != nil {
			t.Fatal(err)
		}
		return dump(bs)
	}
	checks := []struct {
		step time.Duration
		want []string
	}{
		{5 * time.Minute, []string{bucket(day.Add(5*time.Minute), 1, 1, 1, 1), bucket(day.Add(10*time.Minute), 2, 2, 2, 1),
			bucket(day.Add(60*time.Minute), 3, 3, 3, 1), bucket(day.Add(25*time.Hour), 4, 4, 4, 1)}},
		{15 * time.Minute, []string{bucket(day, 1.5, 1, 2, 2), bucket(day.Add(time.Hour), 3, 3, 3, 1), bucket(day.Add(25*time.Hour), 4, 4, 4, 1)}},
		{time.Hour, []string{bucket(day, 1.5, 1, 2, 2), bucket(day.Add(time.Hour), 3, 3, 3, 1), bucket(day.Add(25*time.Hour), 4, 4, 4, 1)}},
		{24 * time.Hour, []string{bucket(day, 2, 1, 3, 3), bucket(day.Add(24*time.Hour), 4, 4, 4, 1)}},
	}
	for _, c := range checks {
		// buckets align to the epoch (midnight UTC for days), not to From
		if got := q(c.step, day.Add(-30*time.Minute)); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("step %s:\n got %v\nwant %v", c.step, got, c.want)
		}
	}
}

func bucketsSubMinute(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(6 * time.Hour)
	mustAppend(t, s,
		g.ev(at.Add(1*time.Second), history.SourceDevice, `1`),
		g.ev(at.Add(9*time.Second), history.SourceDevice, `3`),
		g.ev(at.Add(12*time.Second), history.SourceDevice, `5`),
	)
	bs, err := s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: "value", From: at, To: at.Add(time.Minute), Step: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{bucket(at, 2, 1, 3, 2), bucket(at.Add(10*time.Second), 5, 5, 5, 1)}
	if fmt.Sprint(dump(bs)) != fmt.Sprint(want) {
		t.Fatalf("10s buckets read raw points:\n got %v\nwant %v", dump(bs), want)
	}
}

func bigBatch(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(7 * time.Hour)
	const n = 5000
	evs := make([]history.Event, n)
	for i := range n {
		evs[i] = g.ev(at.Add(time.Duration(i)*100*time.Millisecond), history.SourceDevice,
			fmt.Sprintf(`{"value": %d, "temperature": %d.5, "gps": {"lat": 30, "lng": 31}}`, i%100, i%40))
	}
	start := time.Now()
	if got := mustAppend(t, s, evs...); got != n {
		t.Fatalf("appended %d new, want %d", got, n)
	}
	t.Logf("appended %d events (%d points) in %s", n, n*4, time.Since(start))
	bs, err := s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: "temperature", From: at, To: at.Add(time.Hour), Step: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].N != n {
		t.Fatalf("temperature over the batch: %v", dump(bs))
	}
	got, _ := s.Events(ctx(t), history.EventQuery{ElementID: g.el, From: at, To: at.Add(time.Hour), Limit: 10, Newest: true})
	if ids(got) != ids(evs[n-10:]) {
		t.Fatal("newest 10 of a big batch")
	}
}

func reset(t *testing.T, s history.Store) {
	g := newGen()
	at := base().Add(8 * time.Hour)
	mustAppend(t, s, g.ev(at, history.SourceDevice, `{"value": 1}`))
	if err := s.Reset(ctx(t)); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Events(ctx(t), history.EventQuery{ElementID: g.el, From: at.Add(-time.Hour), To: at.Add(time.Hour), Limit: 10})
	bs, _ := s.Buckets(ctx(t), history.BucketQuery{ElementID: g.el, Field: "value", From: at.Add(-time.Hour), To: at.Add(time.Hour), Step: time.Hour})
	if len(evs) != 0 || len(bs) != 0 {
		t.Fatalf("after Reset: %d events, %d buckets", len(evs), len(bs))
	}
	// the store keeps working after a reset
	if n := mustAppend(t, s, g.ev(at, history.SourceDevice, `{"value": 2}`)); n != 1 {
		t.Fatalf("append after reset: %d", n)
	}
}

func bucket(at time.Time, avg, lo, hi float64, n int64) string {
	return fmt.Sprintf("%s avg=%g min=%g max=%g n=%d", at.UTC().Format(time.RFC3339), avg, lo, hi, n)
}

func dump(bs []history.Bucket) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		f := func(p *float64) float64 {
			if p == nil {
				return math.NaN()
			}
			return math.Round(*p*1e9) / 1e9
		}
		out[i] = bucket(b.Time, f(b.Avg), f(b.Min), f(b.Max), b.N)
	}
	return out
}

func ids(evs []history.Event) string {
	s := ""
	for _, e := range evs {
		s += e.ID.String()[:8] + "@" + e.Time.Format("15:04:05.000") + " "
	}
	return s
}
