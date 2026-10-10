package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
)

func testDevice(dev uuid.UUID, id string) *deviceClient {
	return &deviceClient{client: newClient(context.Background(), nil, id, "device"), deviceID: dev, allowed: map[uuid.UUID]struct{}{}}
}

func testBrowser(user int64, id string) *browserClient {
	return &browserClient{client: newClient(context.Background(), nil, id, "browser"), userID: user, subs: map[uuid.UUID]string{}}
}

func (c *client) drain() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	q := c.queue
	c.queue = nil
	return q
}

func msg(el, dev uuid.UUID, source string, v int) *events.ElementMessage {
	return &events.ElementMessage{ElementID: el, DeviceID: dev, Source: source,
		Actor: events.Actor{ID: "7", Name: "alice"}, Message: json.RawMessage(`{"value":` + itoa(v) + `}`)}
}

func itoa(v int) string { b, _ := json.Marshal(v); return string(b) }

func deliver(h *Hub, m *events.ElementMessage, origin string, at time.Time) {
	dev, br := renderMessage(m, events.Time{Time: at}, nil)
	h.Deliver(m, uuid.New(), at, dev, br, origin)
}

func TestFanoutAndOriginExclusion(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 3}
	d1, d2 := testDevice(dev, "d1"), testDevice(dev, "d2")
	h.AddDevice(d1, []elementInfo{info})
	h.AddDevice(d2, []elementInfo{info})
	b1, b2 := testBrowser(1, "b1"), testBrowser(2, "b2")
	h.AddBrowser(b1)
	h.AddBrowser(b2)
	h.Subscribe(b1, info, []byte("confirm"), []ringEntry{}, true)
	h.Subscribe(b2, info, []byte("confirm"), []ringEntry{}, true)
	b1.drain()
	b2.drain()

	deliver(h, msg(el, dev, events.SourceDevice, 1), "d1", time.Now())
	if n := len(d1.drain()); n != 0 {
		t.Fatalf("origin device got %d frames (echo)", n)
	}
	if n := len(d2.drain()); n != 1 {
		t.Fatalf("other device socket got %d frames", n)
	}
	if len(b1.drain()) != 1 || len(b2.drain()) != 1 {
		t.Fatal("browsers did not both receive")
	}

	deliver(h, msg(el, dev, events.SourceUser, 2), "b1", time.Now())
	if len(b1.drain()) != 0 {
		t.Fatal("browser echo")
	}
	if len(b2.drain()) != 1 || len(d1.drain()) != 1 || len(d2.drain()) != 1 {
		t.Fatal("user command not delivered to all others")
	}
}

func TestHistoryWindowAndReplayOrder(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 3}
	d := testDevice(dev, "d")
	h.AddDevice(d, []elementInfo{info})
	base := time.Now()
	for i := 1; i <= 5; i++ {
		deliver(h, msg(el, dev, events.SourceDevice, i), "d", base.Add(time.Duration(i)*time.Millisecond))
	}
	deliver(h, msg(el, dev, events.SourceUser, 99), "x", base.Add(time.Second)) // user commands are not replayed

	// TSDB history overlaps the in-memory window and adds an older entry.
	old := ringEntry{id: uuid.New(), at: base, frame: []byte("old")}
	b := testBrowser(1, "b")
	h.Subscribe(b, info, []byte("confirm"), []ringEntry{old}, true)
	frames := b.drain()
	if len(frames) != 4 || string(frames[0]) != "confirm" {
		t.Fatalf("want confirm + 3 history frames, got %d", len(frames))
	}
	for i, want := range []string{`"value":3`, `"value":4`, `"value":5`} {
		if !contains(frames[i+1], want) {
			t.Errorf("history[%d] = %s, want %s", i, frames[i+1], want)
		}
	}

	h.UpdateElement(elementInfo{ID: el, DeviceID: dev, Points: 1})
	b2 := testBrowser(2, "b2")
	h.Subscribe(b2, info, []byte("c"), nil, false)
	if got := b2.drain(); len(got) != 2 {
		// the window was trimmed to 1 when points shrank; re-subscribing restores capacity but not dropped entries
		t.Fatalf("after shrinking points: %d frames", len(got))
	}
}

func TestMergeHistoryDedup(t *testing.T) {
	id := uuid.New()
	now := time.Now()
	ring := []ringEntry{{id: id, at: now}}
	hist := []ringEntry{{id: uuid.New(), at: now.Add(-time.Second)}, {id: id, at: now}}
	out := mergeHistory(ring, hist, 10)
	if len(out) != 2 || !out[0].at.Before(out[1].at) {
		t.Fatalf("merge: %+v", out)
	}
}

func TestRemoveElementForcesUnsubscribe(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 1}
	d := testDevice(dev, "d")
	h.AddDevice(d, []elementInfo{info})
	b := testBrowser(1, "b")
	b.SetPerm(el, "R")
	h.Subscribe(b, info, []byte("c"), nil, false)
	b.drain()
	h.RemoveElement(el)
	frames := b.drain()
	if len(frames) != 1 || !contains(frames[0], `"type":"unsubscribe"`) {
		t.Fatalf("frames: %q", frames)
	}
	if d.Owns(el) {
		t.Fatal("device still owns deleted element")
	}
	if _, ok := b.Perm(el); ok {
		t.Fatal("browser still has permission entry")
	}
}

func TestPresenceOnlyToDeviceSubscribers(t *testing.T) {
	h := NewHub()
	devA, devB := uuid.New(), uuid.New()
	a, b := elementInfo{ID: uuid.New(), DeviceID: devA, Points: 0}, elementInfo{ID: uuid.New(), DeviceID: devB, Points: 0}
	ba, bb := testBrowser(1, "ba"), testBrowser(2, "bb")
	h.Subscribe(ba, a, []byte("c"), nil, false)
	h.Subscribe(bb, b, []byte("c"), nil, false)
	ba.drain()
	bb.drain()
	h.PresenceChanged(devA, true)
	if got := ba.drain(); len(got) != 1 || string(got[0]) != string(connStatusFrame(a.ID.String(), true)) {
		t.Fatalf("ba: %q", got)
	}
	if len(bb.drain()) != 0 {
		t.Fatal("presence leaked to other device's subscriber")
	}
}

func TestSlowConsumerIsDisconnected(t *testing.T) {
	b := testBrowser(1, "b")
	for range maxQueuedFrames {
		b.Send([]byte("x"))
	}
	b.Send([]byte("overflow"))
	select {
	case <-b.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("slow consumer not disconnected")
	}
}

func TestWireFramesAreLegacyCompatible(t *testing.T) {
	el := uuid.MustParse("98994c94-71b8-53b0-85f3-d1c6483978de")
	at, _ := time.Parse(time.RFC3339, "2026-10-07T10:00:00.123Z")
	m := msg(el, uuid.New(), events.SourceUser, 5)
	dev, br := renderMessage(m, events.Time{Time: at}, nil)
	wantDev := `{"element_id":"98994c94-71b8-53b0-85f3-d1c6483978de","message":{"value":5},"auth":{"user_id":7,"username":"alice"},"last_edit_at":"2026-10-07T10:00:00.123Z"}`
	if string(dev) != wantDev {
		t.Errorf("device frame:\n got %s\nwant %s", dev, wantDev)
	}
	wantBr := `{"type":"message_element","element_id":"98994c94-71b8-53b0-85f3-d1c6483978de","message":{"value":5},"auth":{"user_id":7,"username":"alice"},"last_edit_at":"2026-10-07T10:00:00.123Z"}`
	if string(br) != wantBr {
		t.Errorf("browser frame:\n got %s\nwant %s", br, wantBr)
	}
	m.Source, m.Actor.ID = events.SourceDevice, el.String()
	_, br = renderMessage(m, events.Time{Time: at}, nil)
	if !contains(br, `"user_id":"98994c94-71b8-53b0-85f3-d1c6483978de"`) {
		t.Errorf("device actor id must be a string: %s", br)
	}
	if got := string(subscribeFrame("e", "RC", nil, true)); got != `{"type":"subscribe","element_id":"e","subscribed":true,"permissions":"RC","details":null,"connected":true}` {
		t.Errorf("subscribe frame: %s", got)
	}
	if got := string(errorFrame("unknown_type", "Unknown message type: x", "")); got != `{"type":"error","error_code":"unknown_type","description":"Unknown message type: x"}` {
		t.Errorf("error frame: %s", got)
	}
}

func TestOriginPolicy(t *testing.T) {
	p := NewOriginPolicy([]string{"https://app.example.com", " http://localhost:5173/ "})
	for origin, want := range map[string]bool{
		"": true, "https://app.example.com": true, "http://localhost:5173": true, "http://quack.local": true,
		"https://evil.example": false, "http://app.example.com": false, "null": false,
	} {
		r := httptest.NewRequest("GET", "http://quack.local/browser/simple/", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if got := p.Allowed(r); got != want {
			t.Errorf("origin %q: got %v want %v", origin, got, want)
		}
	}
}

func contains(b []byte, s string) bool {
	return len(s) == 0 || (len(b) >= len(s) && string(b) != "" && indexOf(string(b), s) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestMergeHistoryTieBreakByEventID(t *testing.T) {
	at := time.Now().Truncate(time.Millisecond)
	var hist []ringEntry
	for i := range 8 {
		id := uuid.Must(uuid.NewV7())
		hist = append(hist, ringEntry{id: id, at: at, frame: []byte{byte('0' + i)}})
	}
	// TSDB may return same-millisecond rows in any order.
	shuffled := []ringEntry{hist[5], hist[1], hist[7], hist[0], hist[3], hist[6], hist[2], hist[4]}
	out := mergeHistory(nil, shuffled, 5)
	for i, r := range out {
		if want := byte('0' + 3 + i); r.frame[0] != want {
			t.Fatalf("pos %d: got %c want %c", i, r.frame[0], want)
		}
	}
}

func TestBrowserRateLimit(t *testing.T) {
	b := testBrowser(1, "b")
	b.rate = 3
	now := time.Now()
	n := 0
	for range 10 {
		if b.allowMessage(now) {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("allowed %d, want 3", n)
	}
}

func TestLatestValueWithoutAWindow(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 0}
	// seen on the bus before anyone subscribed (no state on this gateway yet)
	deliver(h, msg(el, dev, events.SourceDevice, 1), "x", time.Now().Add(-time.Second))
	deliver(h, msg(el, dev, events.SourceDevice, 2), "x", time.Now())
	deliver(h, msg(el, dev, events.SourceUser, 9), "x", time.Now().Add(time.Second)) // a command is not a value
	b := testBrowser(1, "b")
	h.Subscribe(b, info, []byte("c"), nil, true)
	frames := b.drain()
	if len(frames) != 2 || !contains(frames[1], `"value":2`) {
		t.Fatalf("points=0 must still replay the latest value: %q", frames)
	}
}

func TestReplayClosesTheIngestGap(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 5}
	base := time.Now()
	// stored: 1, 2; the bus already carried 3, which the ingester hasn't written yet
	stored := []ringEntry{
		{id: uuid.New(), at: base, frame: []byte(`{"value":1}`)},
		{id: uuid.New(), at: base.Add(time.Millisecond), frame: []byte(`{"value":2}`)},
	}
	deliver(h, msg(el, dev, events.SourceDevice, 3), "x", base.Add(2*time.Millisecond))
	b := testBrowser(1, "b")
	h.Subscribe(b, info, []byte("c"), stored, true)
	frames := b.drain()
	if len(frames) != 4 || !contains(frames[3], `"value":3`) {
		t.Fatalf("want confirm + 1, 2, 3: %q", frames)
	}
}

func TestFailedLoadIsRetriedLater(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 3}
	b := testBrowser(1, "b")
	h.Subscribe(b, info, []byte("c"), nil, false)
	if !h.NeedsHistory(info) {
		t.Fatal("after a failed load the element must be hydrated again")
	}
	h.Subscribe(testBrowser(2, "b2"), info, []byte("c"), []ringEntry{}, true)
	if h.NeedsHistory(info) {
		t.Fatal("hydrated")
	}
}

func TestStateIsFreedWhenNobodyIsLeft(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 3}
	d := testDevice(dev, "d")
	h.AddDevice(d, []elementInfo{info})
	b := testBrowser(1, "b")
	h.AddBrowser(b)
	b.SetPerm(el, "R")
	h.Subscribe(b, info, []byte("c"), []ringEntry{}, true)
	deliver(h, msg(el, dev, events.SourceDevice, 1), "x", time.Now())

	h.Unsubscribe(b, el)
	if h.get(el) == nil {
		t.Fatal("state freed while the device is still connected")
	}
	h.RemoveDevice(d)
	if h.get(el) != nil {
		t.Fatal("state kept after the last device and browser left")
	}
	if len(h.byDevice) != 0 {
		t.Fatalf("byDevice index kept: %v", h.byDevice)
	}
	// the latest value survives, and a new subscriber re-hydrates
	if !h.NeedsHistory(info) {
		t.Fatal("re-subscribe must hydrate again")
	}
	b2 := testBrowser(2, "b2")
	h.Subscribe(b2, info, []byte("c"), []ringEntry{}, true)
	if got := b2.drain(); len(got) != 2 {
		t.Fatalf("re-subscribe: %q", got)
	}
	deliver(h, msg(el, dev, events.SourceDevice, 2), "x", time.Now().Add(time.Second))
	if got := b2.drain(); len(got) != 1 {
		t.Fatalf("live delivery after re-creation: %q", got)
	}
}

// Subscribing and leaving concurrently must never leave a subscriber attached
// to a freed state (it would stop receiving messages).
func TestConcurrentSubscribeAndLeave(t *testing.T) {
	h := NewHub()
	dev, el := uuid.New(), uuid.New()
	info := elementInfo{ID: el, DeviceID: dev, Points: 2}
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				b := testBrowser(int64(w), fmt.Sprintf("b%d-%d", w, i))
				h.Subscribe(b, info, []byte("c"), []ringEntry{}, true)
				h.Unsubscribe(b, el)
			}
		}()
	}
	wg.Wait()
	stay := testBrowser(99, "stay")
	h.Subscribe(stay, info, []byte("c"), []ringEntry{}, true)
	stay.drain()
	deliver(h, msg(el, dev, events.SourceDevice, 7), "x", time.Now())
	if got := stay.drain(); len(got) != 1 {
		t.Fatalf("subscriber attached to a freed state: %q", got)
	}
}
