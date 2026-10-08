package historytest

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/history"
)

// Bench appends `elements` × `perElement` events (one per second, four
// numeric attributes each) in ingest-sized batches, then times the queries the
// platform makes. It logs a summary line per measurement; it asserts nothing
// beyond correctness of counts. The store is Reset afterwards.
func Bench(t *testing.T, s history.Store, elements, perElement int) {
	ctx := context.Background()
	// timestamps from now on, like live ingestion (no late-data handling)
	start := time.Now().UTC().Truncate(time.Minute)
	ids := make([]uuid.UUID, elements)
	for i := range ids {
		ids[i] = uuid.New()
	}
	dev := uuid.New()
	const batch = 1000
	var buf []history.Event
	total := 0
	t0 := time.Now()
	flush := func() {
		if len(buf) == 0 {
			return
		}
		if _, err := s.Append(ctx, buf); err != nil {
			t.Fatal(err)
		}
		total += len(buf)
		buf = buf[:0]
	}
	// interleaved like real traffic: every element reports each second
	for sec := range perElement {
		for e, el := range ids {
			id, _ := uuid.NewV7()
			payload := fmt.Sprintf(`{"temperature": %d.%d, "humidity": %d, "battery": 3.9, "gps": {"lat": 30.04}}`, 20+sec%10, e%10, 40+sec%20)
			ev := history.Event{Time: start.Add(time.Duration(sec) * time.Second), ID: id, ElementID: el, DeviceID: dev,
				Source: history.SourceDevice, ActorID: "d", ActorName: "d", Payload: []byte(payload)}
			buf = append(buf, ev)
			if len(buf) == batch {
				flush()
			}
		}
	}
	flush()
	took := time.Since(t0)
	t.Logf("BENCH append: %d events (%d points) in %s = %.0f events/s", total, total*4, took.Round(time.Millisecond), float64(total)/took.Seconds())

	span := time.Duration(perElement) * time.Second
	timeIt := func(name string, runs int, fn func() int) {
		var ds []time.Duration
		n := 0
		for range runs {
			q := time.Now()
			n = fn()
			ds = append(ds, time.Since(q))
		}
		slices.Sort(ds)
		t.Logf("BENCH %-38s p50=%-9s p95=%-9s (%d rows)", name, ds[len(ds)/2].Round(time.Microsecond), ds[len(ds)*95/100].Round(time.Microsecond), n)
	}
	el := ids[0]
	timeIt(fmt.Sprintf("buckets 1m over %s (temperature)", span.Round(time.Minute)), 30, func() int {
		b, err := s.Buckets(ctx, history.BucketQuery{ElementID: el, Field: "temperature", From: start, To: start.Add(span), Step: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		return len(b)
	})
	timeIt(fmt.Sprintf("buckets 1h over %s (gps.lat)", span.Round(time.Minute)), 30, func() int {
		b, err := s.Buckets(ctx, history.BucketQuery{ElementID: el, Field: "gps.lat", From: start, To: start.Add(span), Step: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		return len(b)
	})
	timeIt("raw newest 5000", 30, func() int {
		evs, err := s.Events(ctx, history.EventQuery{ElementID: el, From: start, To: start.Add(span), Limit: 5000, Newest: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(evs)
	})
	timeIt(fmt.Sprintf("replay: last 50 of %d elements", elements), 30, func() int {
		m, err := s.Last(ctx, ids, 50, start.Add(-30*24*time.Hour)) // the gateway's default replay window
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, v := range m {
			n += len(v)
		}
		return n
	})
	if os.Getenv("QUACK_IT_BENCH_KEEP") != "" {
		return // leave the data to measure sizes
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
}
