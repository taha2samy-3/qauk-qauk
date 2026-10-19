package mqtt

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type testCore struct {
	mu         sync.Mutex
	userErrors []userErr
	statuses   map[uuid.UUID]string
}

type userErr struct {
	UserID      string
	Code        string
	Description string
	ElementID   uuid.UUID
}

func (c *testCore) Publish(p Publication, done func(Outcome, error)) error { return nil }
func (c *testCore) OnCommand(fn func(Command)) func()                      { return func() {} }
func (c *testCore) CommandStatus(id uuid.UUID, status, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.statuses == nil {
		c.statuses = map[uuid.UUID]string{}
	}
	c.statuses[id] = status
}

func (c *testCore) status(id uuid.UUID) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statuses[id]
}

func (c *testCore) SendUserError(userID string, code, description string, elementID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userErrors = append(c.userErrors, userErr{
		UserID:      userID,
		Code:        code,
		Description: description,
		ElementID:   elementID,
	})
}

func (c *testCore) getErrors() []userErr {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]userErr, len(c.userErrors))
	copy(out, c.userErrors)
	return out
}

func TestDownlinkRetryAndExhaustionErrorFrame(t *testing.T) {
	core := &testCore{}
	tr := New(Options{
		GatewayID:           "gw-1",
		Core:                core,
		Log:                 quiet,
		MaxRetries:          3,
		RetryInitialBackoff: 2 * time.Millisecond,
		RetryMaxBackoff:     10 * time.Millisecond,
	})

	snap := snapshot()
	cfg := compile(snap, quiet)
	devID := snap.Devices[0].DeviceID

	tr.mu.Lock()
	tr.conns["c1"] = cfg
	tr.devIdx[devID] = []*connConfig{cfg}
	// Slot 0 exists on this gateway, but s.cm is nil (broker disconnected / handover)
	s := &slot{t: tr, key: slotKey{"c1", 0}}
	tr.slots[slotKey{"c1", 0}] = s
	tr.mu.Unlock()

	elID := uuid.New()
	cmd := Command{
		DeviceID:  devID,
		ElementID: elID,
		Element:   "Fan",
		Message:   json.RawMessage(`{"value": true}`),
		UserID:    "42",
		UserName:  "alice",
		Time:      time.Now(),
	}

	tr.downlink(cmd)

	// Wait for the 3 retries (approx 2ms + 4ms + 8ms = ~14ms)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		errs := core.getErrors()
		if len(errs) > 0 {
			if len(errs) != 1 {
				t.Fatalf("expected exactly 1 error frame, got %d", len(errs))
			}
			err := errs[0]
			if err.UserID != "42" {
				t.Errorf("expected UserID 42, got %s", err.UserID)
			}
			if err.Code != "delivery_failed" {
				t.Errorf("expected code delivery_failed, got %s", err.Code)
			}
			if err.ElementID != elID {
				t.Errorf("expected ElementID %s, got %s", elID, err.ElementID)
			}
			if !strings.Contains(err.Description, "not delivered after 4 attempts") || !strings.Contains(err.Description, "not connected") {
				t.Errorf("unexpected description: %s", err.Description)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("timed out waiting for delivery_failed error frame")
}

func TestDownlinkAnonymousCommandNoPanic(t *testing.T) {
	core := &testCore{}
	tr := New(Options{
		GatewayID:           "gw-1",
		Core:                core,
		Log:                 quiet,
		MaxRetries:          2,
		RetryInitialBackoff: 1 * time.Millisecond,
		RetryMaxBackoff:     5 * time.Millisecond,
	})

	snap := snapshot()
	cfg := compile(snap, quiet)
	devID := snap.Devices[0].DeviceID

	tr.mu.Lock()
	tr.conns["c1"] = cfg
	tr.devIdx[devID] = []*connConfig{cfg}
	s := &slot{t: tr, key: slotKey{"c1", 0}}
	tr.slots[slotKey{"c1", 0}] = s
	tr.mu.Unlock()

	cmd := Command{
		DeviceID: devID,
		Element:  "Fan",
		Message:  json.RawMessage(`{"value": false}`),
		UserID:   "", // No user (automated pipeline)
	}

	tr.downlink(cmd)
	time.Sleep(50 * time.Millisecond)

	if len(core.getErrors()) != 0 {
		t.Fatalf("anonymous command should not dispatch user error frame, got: %v", core.getErrors())
	}
}

func TestDownlinkBufferCapacityLimit(t *testing.T) {
	core := &testCore{}
	tr := New(Options{
		GatewayID: "gw-1",
		Core:      core,
		Log:       quiet,
	})

	snap := snapshot()
	cfg := compile(snap, quiet)
	devID := snap.Devices[0].DeviceID

	tr.mu.Lock()
	tr.conns["c1"] = cfg
	tr.devIdx[devID] = []*connConfig{cfg}
	s := &slot{t: tr, key: slotKey{"c1", 0}}
	tr.slots[slotKey{"c1", 0}] = s
	tr.mu.Unlock()

	// Fill buffer to capacity
	tr.inFlight.Store(maxInFlightDownlinks)

	elID := uuid.New()
	cmd := Command{
		DeviceID:  devID,
		ElementID: elID,
		Element:   "Fan",
		Message:   json.RawMessage(`{"value": true}`),
		UserID:    "99",
	}

	tr.downlink(cmd)

	errs := core.getErrors()
	if len(errs) != 1 {
		t.Fatalf("expected immediate delivery_failed for buffer overflow, got %d errors", len(errs))
	}
	if errs[0].Code != "delivery_failed" || errs[0].Description != "MQTT downlink buffer full" {
		t.Fatalf("unexpected error frame: %+v", errs[0])
	}
}

// A newer command for the same element replaces one still being retried:
// only the newest is reported (and would be delivered).
func TestDownlinkNewestCommandWins(t *testing.T) {
	core := &testCore{}
	tr := New(Options{GatewayID: "gw-1", Core: core, Log: quiet, MaxRetries: 3,
		RetryInitialBackoff: 20 * time.Millisecond, RetryMaxBackoff: 20 * time.Millisecond})
	snap := snapshot()
	cfg := compile(snap, quiet)
	devID := snap.Devices[0].DeviceID
	tr.mu.Lock()
	tr.conns["c1"] = cfg
	tr.devIdx[devID] = []*connConfig{cfg}
	tr.slots[slotKey{"c1", 0}] = &slot{t: tr, key: slotKey{"c1", 0}} // not connected
	tr.mu.Unlock()

	on := Command{DeviceID: devID, Element: "Fan", Message: json.RawMessage(`{"value": true}`), UserID: "on"}
	off := Command{DeviceID: devID, Element: "Fan", Message: json.RawMessage(`{"value": false}`), UserID: "off"}
	tr.downlink(on)
	time.Sleep(5 * time.Millisecond) // ON is being retried
	tr.downlink(off)

	deadline := time.Now().Add(2 * time.Second)
	for len(core.getErrors()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	errs := core.getErrors()
	if len(errs) != 1 || errs[0].UserID != "off" {
		t.Fatalf("only the newest command (OFF) may be retried to the end, got %+v", errs)
	}
	if tr.inFlight.Load() != 0 {
		t.Fatalf("workers left: %d", tr.inFlight.Load())
	}
}

// A downlink that can't be built is reported at once, not retried.
func TestDownlinkPermanentErrorIsNotRetried(t *testing.T) {
	core := &testCore{}
	tr := New(Options{GatewayID: "gw-1", Core: core, Log: quiet, MaxRetries: 3,
		RetryInitialBackoff: time.Second, RetryMaxBackoff: time.Second})
	snap := snapshot()
	cfg := compile(snap, quiet)
	devID := snap.Devices[0].DeviceID
	tr.mu.Lock()
	tr.conns["c1"] = cfg
	tr.devIdx[devID] = []*connConfig{cfg}
	tr.slots[slotKey{"c1", 0}] = &slot{t: tr, key: slotKey{"c1", 0}}
	tr.mu.Unlock()

	// the Mode encoder needs a number for bytes
	tr.downlink(Command{DeviceID: devID, Element: "Mode", Message: json.RawMessage(`{"value": "x"}`), UserID: "u"})
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(core.getErrors()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	errs := core.getErrors()
	if len(errs) != 1 || !strings.Contains(errs[0].Description, "can't be built") {
		t.Fatalf("want an immediate 'can't be built' error (no 1 s retries), got %+v", errs)
	}
}

// Tracked commands report what became of them; untracked ones don't.
func TestDownlinkReportsTrackedCommands(t *testing.T) {
	core := &testCore{}
	tr := New(Options{GatewayID: "gw-1", Core: core, Log: quiet, MaxRetries: 3,
		RetryInitialBackoff: 20 * time.Millisecond, RetryMaxBackoff: 20 * time.Millisecond})
	snap := snapshot()
	cfg := compile(snap, quiet)
	devID := snap.Devices[0].DeviceID
	tr.mu.Lock()
	tr.conns["c1"] = cfg
	tr.devIdx[devID] = []*connConfig{cfg}
	tr.slots[slotKey{"c1", 0}] = &slot{t: tr, key: slotKey{"c1", 0}} // no broker: every attempt fails
	tr.mu.Unlock()

	cmd := func(track bool) Command {
		return Command{ID: uuid.New(), Track: track, DeviceID: devID, ElementID: uuid.New(), Element: "Fan",
			Message: json.RawMessage(`{"value": true}`), UserID: "42", Time: time.Now()}
	}
	first, second, untracked := cmd(true), cmd(true), cmd(false)
	tr.downlink(first)
	time.Sleep(5 * time.Millisecond)
	tr.downlink(second) // replaces the first while it is retried

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (core.status(first.ID) == "" || core.status(second.ID) == "") {
		time.Sleep(10 * time.Millisecond)
	}
	if got := core.status(first.ID); got != "superseded" {
		t.Errorf("first: %q, want superseded", got)
	}
	if got := core.status(second.ID); got != "failed" {
		t.Errorf("second: %q, want failed", got)
	}
	tr.downlink(untracked) // the same element: it fails too, silently
	time.Sleep(200 * time.Millisecond)
	if got := core.status(untracked.ID); got != "" {
		t.Errorf("an untracked command reported %q", got)
	}
}
