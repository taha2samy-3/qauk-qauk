//go:build contract

package contracttest

import (
	"testing"
	"time"
)

func isForcedUnsubscribe(elementID string) func(Frame) bool {
	return func(f Frame) bool {
		return f.Type() == "unsubscribe" && f.Str("element_id") == elementID && f.M["unsubscribe"] == true
	}
}

func TestPermissions(t *testing.T) {
	t.Run("RevokeForcesUnsubscribe", func(t *testing.T) {
		el := elem(t, "sensor")
		u := user(t, "revoke")
		d := connectDevice(t, "main")
		b := connectBrowser(t, "revoke")
		subscribeAndDrain(t, b, el.ID)
		t.Cleanup(func() { hook(t, "set-perm", u.Username, el.ID, "RC") })

		hook(t, "revoke", u.Username, el.ID)
		f := b.Expect("forced unsubscribe", isForcedUnsubscribe(el.ID))
		t.Logf("forced unsubscribe frame: %s", f.Raw)

		v := uniqueValue()
		deviceSend(d, el.ID, v)
		b.ExpectNone("data after the grant was revoked", isElementMsg(el.ID, v))

		// Re-subscribing is now denied.
		b.Send(map[string]any{"type": "subscribe", "element_id": el.ID})
		f = b.Expect("permission_denied", isType("error", el.ID))
		if f.Str("error_code") != "permission_denied" {
			t.Fatalf("error_code = %q, want permission_denied: %s", f.Str("error_code"), f.Raw)
		}
	})

	t.Run("UpgradeSendsPermissionsUpdate", func(t *testing.T) {
		el := elem(t, "switch")
		u := user(t, "upgrade")
		d := connectDevice(t, "main")
		b := connectBrowser(t, "upgrade")
		if f := subscribeAndDrain(t, b, el.ID); f.Str("permissions") != "R" {
			t.Fatalf("precondition: permissions = %v, want R", f.M["permissions"])
		}
		t.Cleanup(func() { hook(t, "set-perm", u.Username, el.ID, "R") })

		hook(t, "set-perm", u.Username, el.ID, "RC")
		f := b.Expect("permissions_update", isType("permissions_update", el.ID))
		if f.Str("permissions") != "RC" {
			t.Fatalf("permissions = %v, want RC: %s", f.M["permissions"], f.Raw)
		}

		// The upgraded socket may now publish without re-subscribing.
		v := uniqueValue()
		browserSend(b, el.ID, v)
		got := d.Expect("message from the upgraded user", isDeviceMsg(el.ID, v))
		if auth, _ := got.M["auth"].(map[string]any); auth["username"] != u.Username {
			t.Errorf("auth.username = %v, want %q", auth["username"], u.Username)
		}
	})

	t.Run("B6_UnsubscribeKeepsOtherListeners", func(t *testing.T) {
		a, bEl := elem(t, "sensor"), elem(t, "switch")
		u := user(t, "b6")
		b := connectBrowser(t, "b6")
		subscribeAndDrain(t, b, a.ID)
		subscribeAndDrain(t, b, bEl.ID)
		t.Cleanup(func() { hook(t, "set-perm", u.Username, bEl.ID, "R") })

		b.Send(map[string]any{"type": "unsubscribe", "element_id": a.ID})
		b.Expect("unsubscribe confirm for A", isType("unsubscribe", a.ID))

		hook(t, "revoke", u.Username, bEl.ID)
		_, ok := b.TryExpect(recvTimeout, isForcedUnsubscribe(bEl.ID))
		checkBug(t, "B6", ok, "no forced unsubscribe for B after unsubscribing A and revoking B")
	})

	t.Run("B8_GroupMembershipRemovalForcesUnsubscribe", func(t *testing.T) {
		el := elem(t, "sensor")
		u := user(t, "b8")
		b := connectBrowser(t, "b8")
		if f := subscribeAndDrain(t, b, el.ID); f.Str("permissions") != "R" {
			t.Fatalf("precondition: permissions = %v, want R (via group)", f.M["permissions"])
		}
		hook(t, "remove-from-group", u.Username, fx.Groups["readers"])
		_, ok := b.TryExpect(recvTimeout, isForcedUnsubscribe(el.ID))
		checkBug(t, "B8", ok, "no forced unsubscribe after removing the user from the only granting group")
	})

	t.Run("NewGrantAllowsSubscribe", func(t *testing.T) {
		// A grant created while the socket is open is honoured on the next subscribe.
		el := elem(t, "history")
		u := user(t, "noperm")
		b := connectBrowser(t, "noperm")
		b.Send(map[string]any{"type": "subscribe", "element_id": el.ID})
		b.Expect("permission_denied", isType("error", el.ID))
		t.Cleanup(func() { hook(t, "revoke", u.Username, el.ID) })

		hook(t, "set-perm", u.Username, el.ID, "R")
		time.Sleep(propagateWait)
		if f := subscribe(t, b, el.ID); f.Str("permissions") != "R" {
			t.Fatalf("permissions = %v, want R", f.M["permissions"])
		}
	})
}
