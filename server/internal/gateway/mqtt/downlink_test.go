package mqtt

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type testCore struct {
	mu         sync.Mutex
	userErrors []userErr
}

type userErr struct {
	UserID      string
	Code        string
	Description string
	ElementID   uuid.UUID
}

func (c *testCore) Publish(p Publication, done func(error)) error { return nil }
func (c *testCore) OnCommand(fn func(Command)) func()             { return func() {} }
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
			if err.Description != "MQTT broker unreachable after retries" {
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
