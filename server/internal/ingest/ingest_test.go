package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
)

// fakeStore rejects any batch containing a payload with "poison" as bad
// data, or fails everything while down (an outage).
type fakeStore struct {
	history.Store
	mu     sync.Mutex
	stored []history.Event
	calls  int
	down   bool
}

func (f *fakeStore) Append(_ context.Context, evs []history.Event) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.down {
		return 0, errors.New("connection refused")
	}
	for _, e := range evs {
		if strings.Contains(string(e.Payload), "poison") {
			return 0, history.ErrBadData
		}
	}
	f.stored = append(f.stored, evs...)
	return len(evs), nil
}

type fakePub struct{ recs []*kgo.Record }

func (p *fakePub) PublishSync(_ context.Context, recs ...*kgo.Record) error {
	p.recs = append(p.recs, recs...)
	return nil
}

func record(t *testing.T, el uuid.UUID, source, msg string, at time.Time) *kgo.Record {
	t.Helper()
	ev, err := events.New(events.TypeElementMessage, "/quack/gateway/g", el.String(), events.ElementMessage{
		ElementID: el, DeviceID: uuid.New(), Source: source, Actor: events.Actor{ID: "a", Name: "a"},
		Origin: events.Origin{GatewayID: "g", ConnID: "c"}, Message: json.RawMessage(msg),
	})
	if err != nil {
		t.Fatal(err)
	}
	ev.Time = events.Time{Time: at}
	b, _ := json.Marshal(ev)
	return &kgo.Record{Topic: events.TopicElementEvents, Partition: 2, Offset: int64(at.UnixMilli()), Key: []byte(el.String()), Value: b}
}

func newIngester(st history.Store, pub Publisher) *Ingester {
	return &Ingester{Store: st, Publisher: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func topics(recs []*kgo.Record) map[string]int {
	out := map[string]int{}
	for _, r := range recs {
		out[r.Topic]++
	}
	return out
}

func TestOnePoisonedEventDoesNotBlockTheBatch(t *testing.T) {
	st, pub := &fakeStore{}, &fakePub{}
	el := uuid.New()
	now := time.Now()
	var recs []*kgo.Record
	for i := range 16 {
		msg := `{"value": 1}`
		if i == 11 {
			msg = `{"value": "poison"}`
		}
		recs = append(recs, record(t, el, "device", msg, now.Add(time.Duration(i)*time.Millisecond)))
	}
	if err := newIngester(st, pub).process(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	if len(st.stored) != 15 {
		t.Fatalf("stored %d events, want the 15 good ones", len(st.stored))
	}
	got := topics(pub.recs)
	if got[events.TopicElementEventsDLQ] != 1 || got[events.TopicElementState] != 1 {
		t.Fatalf("published %v", got)
	}
	for _, r := range pub.recs {
		if r.Topic == events.TopicElementEventsDLQ {
			if !strings.Contains(string(r.Value), "poison") || header(r, "error") == "" || header(r, "source") == "" {
				t.Fatalf("dead letter must keep the record and say why: %s %v", r.Value, r.Headers)
			}
		}
	}
	if st.calls > 2*5+1 {
		t.Fatalf("bisection took %d appends", st.calls)
	}
}

func TestUnstorableAndUndecodableGoToTheDLQ(t *testing.T) {
	st, pub := &fakeStore{}, &fakePub{}
	now := time.Now()
	recs := []*kgo.Record{
		record(t, uuid.New(), "device", `{"value": "a\u0000b"}`, now),
		{Topic: events.TopicElementEvents, Value: []byte(`not json`)},
		record(t, uuid.New(), "device", `{"value": 2}`, now),
	}
	if err := newIngester(st, pub).process(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	if len(st.stored) != 1 || topics(pub.recs)[events.TopicElementEventsDLQ] != 2 {
		t.Fatalf("stored %d, published %v", len(st.stored), topics(pub.recs))
	}
}

func TestAnOutageIsRetriedNotDeadLettered(t *testing.T) {
	st, pub := &fakeStore{down: true}, &fakePub{}
	recs := []*kgo.Record{record(t, uuid.New(), "device", `{"value": 1}`, time.Now())}
	if err := newIngester(st, pub).process(context.Background(), recs); err == nil {
		t.Fatal("an outage must fail the batch (so it is retried)")
	}
	if len(pub.recs) != 0 {
		t.Fatalf("nothing may be published during an outage: %v", topics(pub.recs))
	}
}

func TestLatestStatePerElement(t *testing.T) {
	st, pub := &fakeStore{}, &fakePub{}
	a, b := uuid.New(), uuid.New()
	now := time.Now()
	recs := []*kgo.Record{
		record(t, a, "device", `{"value": 1}`, now),
		record(t, a, "device", `{"value": 3}`, now.Add(2*time.Second)),
		record(t, a, "device", `{"value": 2}`, now.Add(time.Second)),
		record(t, a, "user", `{"value": 9}`, now.Add(5*time.Second)), // commands are not state
		record(t, b, "user", `{"value": 9}`, now),
	}
	if err := newIngester(st, pub).process(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	if len(pub.recs) != 1 || string(pub.recs[0].Key) != a.String() || pub.recs[0].Topic != events.TopicElementState {
		t.Fatalf("want one state record for element a, got %v", topics(pub.recs))
	}
	ev, err := ToEvent(pub.recs[0].Value)
	if err != nil || string(ev.Payload) != `{"value":3}` {
		t.Fatalf("latest state = %s (%v)", ev.Payload, err)
	}
}

func TestToEvent(t *testing.T) {
	el := uuid.New()
	r := record(t, el, "device", `{"value": 3, "y": 9}`, time.Now())
	ev, err := ToEvent(r.Value)
	if err != nil || ev.ElementID != el || ev.Value == nil || *ev.Value != 3 || ev.Source != "device" {
		t.Fatalf("event %+v err=%v", ev, err)
	}
	if _, err := ToEvent([]byte(`{"type":"io.quack.control.changed.v1"}`)); err == nil {
		t.Fatal("non-element event converted")
	}
}
