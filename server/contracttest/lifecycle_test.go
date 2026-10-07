//go:build contract

package contracttest

import (
	"regexp"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// createElement creates an element for the main device and grants RC to the
// rc user, then waits for the change to reach live sockets.
func createElement(t *testing.T, points string) string {
	t.Helper()
	id := hook(t, "element-create", dev(t, "main").ID, points)
	if !uuidRe.MatchString(id) {
		t.Fatalf("element-create printed %q, want a UUID", id)
	}
	hook(t, "set-perm", user(t, "rc").Username, id, "RC")
	time.Sleep(propagateWait)
	return id
}

// requireForcedCloseCode checks the close code of a server-forced device
// close. The plan (§6.4) says 4000, but Django actually closes these sockets
// with 1000: both device-delete (via the Connections cascade) and key-touch
// go through the "element_connection_status: disconnected" handler in
// NodeRedConsumer, which calls close() without a code. Both are accepted; see
// README "Adjustments to Django's real behaviour".
func requireForcedCloseCode(t *testing.T, code websocket.StatusCode) {
	t.Helper()
	switch code {
	case closeCode4000, websocket.StatusNormalClosure:
		t.Logf("socket closed with %v", code)
	default:
		t.Fatalf("socket closed with %v, want 4000 (or 1000, Django's actual code)", code)
	}
}

func TestLifecycle(t *testing.T) {
	t.Run("ElementCreatePropagatesToDevice", func(t *testing.T) {
		d := connectDevice(t, "main")
		d2 := connectDevice(t, "main")
		time.Sleep(settleDelay)
		id := createElement(t, "3")

		b := connectBrowser(t, "rc")
		if f := subscribeAndDrain(t, b, id); f.Str("permissions") != "RC" {
			t.Fatalf("permissions = %v, want RC", f.M["permissions"])
		}
		v := uniqueValue()
		deviceSend(d, id, v)
		b.Expect("message for the new element", isElementMsg(id, v))
		d2.Expect("message on the other socket of the device", isDeviceMsg(id, v))

		// Browser -> device works for the new element too.
		v2 := uniqueValue()
		browserSend(b, id, v2)
		d.Expect("browser message for the new element", isDeviceMsg(id, v2))
	})

	t.Run("ElementDeletePropagatesToDevice", func(t *testing.T) {
		d1 := connectDevice(t, "main")
		d2 := connectDevice(t, "main")
		time.Sleep(settleDelay)
		id := createElement(t, "3")

		v := uniqueValue()
		deviceSend(d1, id, v)
		d2.Expect("message for the new element (control)", isDeviceMsg(id, v))

		hook(t, "element-delete", id)
		time.Sleep(propagateWait)

		v2 := uniqueValue()
		deviceSend(d1, id, v2)
		d2.ExpectNone("data for a deleted element", isDeviceMsg(id, v2))

		// Subscribing to the deleted element is denied.
		b := connectBrowser(t, "rc")
		b.Send(map[string]any{"type": "subscribe", "element_id": id})
		f := b.Expect("permission_denied", isType("error", id))
		if f.Str("error_code") != "permission_denied" {
			t.Fatalf("error_code = %q, want permission_denied: %s", f.Str("error_code"), f.Raw)
		}
	})

	t.Run("ElementDeleteForcesSubscriberUnsubscribe", func(t *testing.T) {
		id := createElement(t, "3")
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, id)
		hook(t, "element-delete", id)
		f := b.Expect("forced unsubscribe for the deleted element", isForcedUnsubscribe(id))
		t.Logf("frame: %s", f.Raw)
	})

	t.Run("DeviceDeleteClosesSocket", func(t *testing.T) {
		d := connectDevice(t, "disposable")
		time.Sleep(settleDelay)
		hook(t, "device-delete", dev(t, "disposable").ID)
		code, closed := d.WaitClosed(recvTimeout)
		if !closed {
			t.Fatalf("device socket still open %s after device-delete", recvTimeout)
		}
		requireForcedCloseCode(t, code)

		// And it cannot reconnect.
		o := attemptDevice(t, deviceHeader(deviceToken(t, dev(t, "disposable"))))
		requireRejected(t, o)
	})

	t.Run("KeyTouchClosesSocket", func(t *testing.T) {
		rot := dev(t, "rotate")
		d := connectDevice(t, "rotate")
		time.Sleep(settleDelay)
		hook(t, "key-touch", rot.KeyID)
		code, closed := d.WaitClosed(recvTimeout)
		if !closed {
			t.Fatalf("device socket still open %s after key-touch", recvTimeout)
		}
		requireForcedCloseCode(t, code)

		// The (unchanged) key still authenticates a new connection.
		time.Sleep(settleDelay)
		requireAccepted(t, attemptDevice(t, deviceHeader(deviceToken(t, rot))))
	})
}
