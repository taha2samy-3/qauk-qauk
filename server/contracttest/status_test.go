//go:build contract

package contracttest

import (
	"testing"
	"time"
)

func isStatus(elementID, status string) func(Frame) bool {
	return func(f Frame) bool {
		return f.Type() == "element_connection_status" && f.Str("element_id") == elementID && f.Str("status") == status
	}
}

// connectStatusDevice connects the "status" device without the usual
// cleanup-settle so the test can close it explicitly and observe the event.
func connectStatusDevice(t *testing.T) *Client {
	t.Helper()
	settleAfterClose(t)
	d := dev(t, "status")
	r := dial(t, "device:status", fx.Paths.Device, deviceHeader(deviceToken(t, d)))
	if r.err != nil {
		t.Fatalf("status device connect failed: %v (%s)", r.err, respStatus(r.resp))
	}
	return r.client
}

func TestConnectionStatus(t *testing.T) {
	el := elem(t, "status")

	t.Run("ConnectAndDisconnectEvents", func(t *testing.T) {
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, el.ID)

		d := connectStatusDevice(t)
		f := b.Expect("status=connected", isStatus(el.ID, "connected"))
		t.Logf("connected frame: %s", f.Raw)

		d.Close()
		f = b.Expect("status=disconnected", isStatus(el.ID, "disconnected"))
		t.Logf("disconnected frame: %s", f.Raw)
	})

	t.Run("ConfirmReflectsState", func(t *testing.T) {
		b1 := connectBrowser(t, "rc")
		f := subscribe(t, b1, el.ID)
		if f.M["connected"] != false {
			t.Fatalf("connected = %v with no device socket open, want false: %s", f.M["connected"], f.Raw)
		}
		b1.Drain()

		d := connectStatusDevice(t)
		b1.Expect("status=connected", isStatus(el.ID, "connected"))

		b2 := connectBrowser(t, "rc")
		f = subscribe(t, b2, el.ID)
		if f.M["connected"] != true {
			t.Fatalf("connected = %v with the device socket open, want true: %s", f.M["connected"], f.Raw)
		}

		d.Close()
		b1.Expect("status=disconnected", isStatus(el.ID, "disconnected"))
		time.Sleep(settleDelay)

		b3 := connectBrowser(t, "rc")
		f = subscribe(t, b3, el.ID)
		if f.M["connected"] != false {
			t.Fatalf("connected = %v after the device closed, want false: %s", f.M["connected"], f.Raw)
		}
	})

	t.Run("NoStatusForOtherElements", func(t *testing.T) {
		// A subscriber of an unrelated element must not see this device's status.
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, elem(t, "sensor").ID)
		d := connectStatusDevice(t)
		b.ExpectNone("a status event for an element it did not subscribe to", isType("element_connection_status", el.ID))
		d.Close()
	})
}
