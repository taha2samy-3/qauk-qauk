//go:build contract

package contracttest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if len(bytes.TrimSpace(a)) == 0 {
		a = json.RawMessage("null")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		b = json.RawMessage("null")
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

func requireAuthObject(t *testing.T, f Frame) map[string]any {
	t.Helper()
	auth, ok := f.M["auth"].(map[string]any)
	if !ok {
		t.Fatalf("frame has no auth object: %s", f.Raw)
	}
	if _, ok := auth["user_id"]; !ok {
		t.Fatalf("auth has no user_id: %s", f.Raw)
	}
	if _, ok := auth["username"]; !ok {
		t.Fatalf("auth has no username: %s", f.Raw)
	}
	return auth
}

func requireLastEdit(t *testing.T, f Frame) string {
	t.Helper()
	s, ok := f.M["last_edit_at"].(string)
	if !ok || s == "" {
		t.Fatalf("frame has no non-empty last_edit_at: %s", f.Raw)
	}
	return s
}

func TestSubscribe(t *testing.T) {
	t.Run("ConfirmShape", func(t *testing.T) {
		el := elem(t, "sensor")
		b := connectBrowser(t, "rc")
		f := subscribe(t, b, el.ID)
		if f.M["subscribed"] != true {
			t.Errorf("subscribed != true: %s", f.Raw)
		}
		if f.Str("permissions") != "RC" {
			t.Errorf("permissions = %v, want RC: %s", f.M["permissions"], f.Raw)
		}
		raw := map[string]json.RawMessage{}
		_ = json.Unmarshal([]byte(f.Raw), &raw)
		details, ok := raw["details"]
		if !ok {
			t.Errorf("confirm has no details key: %s", f.Raw)
		} else if !jsonEqual(details, el.Details) {
			t.Errorf("details = %s, want %s", details, el.Details)
		}
		if _, ok := f.M["connected"].(bool); !ok {
			t.Errorf("connected is not a bool: %s", f.Raw)
		}
	})

	t.Run("ReadOnlyPermission", func(t *testing.T) {
		b := connectBrowser(t, "r")
		f := subscribe(t, b, elem(t, "sensor").ID)
		if f.Str("permissions") != "R" {
			t.Fatalf("permissions = %v, want R: %s", f.M["permissions"], f.Raw)
		}
	})

	t.Run("PermissionViaGroup", func(t *testing.T) {
		b := connectBrowser(t, "group_r")
		f := subscribe(t, b, elem(t, "sensor").ID)
		if f.Str("permissions") != "R" {
			t.Fatalf("permissions = %v, want R: %s", f.M["permissions"], f.Raw)
		}
	})

	t.Run("PermissionDenied", func(t *testing.T) {
		el := elem(t, "sensor")
		b := connectBrowser(t, "noperm")
		b.Send(map[string]any{"type": "subscribe", "element_id": el.ID})
		f := b.Expect("permission_denied error", isType("error", el.ID))
		if f.Str("error_code") != "permission_denied" {
			t.Fatalf("error_code = %q, want permission_denied: %s", f.Str("error_code"), f.Raw)
		}
	})

	t.Run("UnknownElementDenied", func(t *testing.T) {
		id := "00000000-0000-4000-8000-000000000001"
		b := connectBrowser(t, "rc")
		b.Send(map[string]any{"type": "subscribe", "element_id": id})
		f := b.Expect("permission_denied error", isType("error", id))
		if f.Str("error_code") != "permission_denied" {
			t.Fatalf("error_code = %q, want permission_denied: %s", f.Str("error_code"), f.Raw)
		}
	})

	t.Run("B5_InvalidElementIdNoTraceback", func(t *testing.T) {
		b := connectBrowser(t, "rc")
		b.Send(map[string]any{"type": "subscribe", "element_id": "not-a-uuid"})
		f := b.Expect("an error frame", isType("error", ""))
		checkBug(t, "B5", !strings.Contains(f.Raw, "Traceback"),
			"error frame leaks a Python traceback (error_code=%s, %d bytes)", f.Str("error_code"), len(f.Raw))
	})
}

func TestDeviceToBrowser(t *testing.T) {
	t.Run("Delivered", func(t *testing.T) {
		el := elem(t, "sensor")
		d := connectDevice(t, "main")
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, el.ID)

		v := uniqueValue()
		deviceSend(d, el.ID, v)
		f := b.Expect("device message", isElementMsg(el.ID, v))
		requireAuthObject(t, f)
		requireLastEdit(t, f)
		if m, _ := f.M["message"].(map[string]any); len(m) != 1 {
			t.Errorf("message not forwarded verbatim: %s", f.Raw)
		}
		d.ExpectNone("an echo of its own message", isDeviceMsg(el.ID, v))
	})

	t.Run("AllSubscribersReceive", func(t *testing.T) {
		el := elem(t, "sensor")
		d := connectDevice(t, "main")
		b1 := connectBrowser(t, "rc")
		b2 := connectBrowser(t, "r")
		subscribeAndDrain(t, b1, el.ID)
		subscribeAndDrain(t, b2, el.ID)
		v := uniqueValue()
		deviceSend(d, el.ID, v)
		b1.Expect("device message", isElementMsg(el.ID, v))
		b2.Expect("device message", isElementMsg(el.ID, v))
	})

	t.Run("OtherSocketOfSameDeviceReceives", func(t *testing.T) {
		el := elem(t, "sensor")
		d1 := connectDevice(t, "main")
		d2 := connectDevice(t, "main")
		time.Sleep(settleDelay)
		v := uniqueValue()
		deviceSend(d1, el.ID, v)
		f := d2.Expect("device-shaped frame on the other socket", isDeviceMsg(el.ID, v))
		requireAuthObject(t, f)
		requireLastEdit(t, f)
		d1.ExpectNone("an echo of its own message", isDeviceMsg(el.ID, v))
	})

	t.Run("NotSubscribedNotDelivered", func(t *testing.T) {
		el := elem(t, "sensor")
		d := connectDevice(t, "main")
		b := connectBrowser(t, "rc")
		v := uniqueValue()
		deviceSend(d, el.ID, v)
		b.ExpectNone("data for an element it never subscribed to", isAnyElementMsg(el.ID))
	})

	t.Run("ForeignElementDropped", func(t *testing.T) {
		foreign := elem(t, "foreign")
		d := connectDevice(t, "main")
		owner := connectDevice(t, "second")
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, foreign.ID)

		v := uniqueValue()
		deviceSend(d, foreign.ID, v)
		b.ExpectNone("data sent by a device that does not own the element", isAnyElementMsg(foreign.ID))

		// Positive control: the owning device can publish to it.
		v2 := uniqueValue()
		deviceSend(owner, foreign.ID, v2)
		b.Expect("owner's message", isElementMsg(foreign.ID, v2))
	})

	t.Run("UnknownElementDropped", func(t *testing.T) {
		el := elem(t, "sensor")
		d := connectDevice(t, "main")
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, el.ID)
		deviceSend(d, "00000000-0000-4000-8000-000000000002", uniqueValue())
		d.Send(`{"this is": "not a valid device frame"}`)
		d.Send(`not json at all`)
		// The device socket must survive garbage and keep working.
		v := uniqueValue()
		deviceSend(d, el.ID, v)
		b.Expect("device message after garbage", isElementMsg(el.ID, v))
	})
}

func TestHistoryReplay(t *testing.T) {
	el := elem(t, "history")
	if el.Points != 5 {
		t.Fatalf("fixture: history element must have points=5, has %d", el.Points)
	}
	d := connectDevice(t, "main")
	watcher := connectBrowser(t, "rc")
	subscribeAndDrain(t, watcher, el.ID)

	base := uniqueValue()
	const sent = 7
	for i := int64(0); i < sent; i++ {
		deviceSend(d, el.ID, base+i)
	}
	// Wait until the server has processed all of them.
	for i := int64(0); i < sent; i++ {
		watcher.Expect("live message", isElementMsg(el.ID, base+i))
	}

	b := connectBrowser(t, "rc")
	b.Send(map[string]any{"type": "subscribe", "element_id": el.ID})

	var got []int64
	confirmed := false
	for {
		f, ok := b.next(func() time.Duration {
			if confirmed {
				return quietWindow
			}
			return recvTimeout
		}())
		if !ok {
			break
		}
		switch {
		case f.Type() == "subscribe" && f.Str("element_id") == el.ID:
			if confirmed {
				t.Fatalf("second subscribe confirm: %s", f.Raw)
			}
			confirmed = true
		case f.Type() == "message_element" && f.Str("element_id") == el.ID:
			if !confirmed {
				t.Fatalf("history frame arrived before the subscribe confirm: %s", f.Raw)
			}
			requireAuthObject(t, f)
			requireLastEdit(t, f)
			v, _ := messageValue(f)
			fv, _ := v.(float64)
			got = append(got, int64(fv))
		default:
			t.Logf("ignoring frame %s", truncate(f.Raw, 200))
		}
	}
	if !confirmed {
		t.Fatalf("no subscribe confirm")
	}
	want := []int64{base + 2, base + 3, base + 4, base + 5, base + 6}
	if len(got) != len(want) {
		t.Fatalf("replayed %d frames %v, want exactly %d (last %d values in order) %v", len(got), got, len(want), el.Points, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replay order: got %v, want %v", got, want)
		}
	}
}

func TestBrowserToDevice(t *testing.T) {
	el := elem(t, "switch")
	rc := user(t, "rc")

	setup := func(t *testing.T) (d, sender, other *Client) {
		d = connectDevice(t, "main")
		sender = connectBrowser(t, "rc")
		other = connectBrowser(t, "r")
		subscribeAndDrain(t, sender, el.ID)
		subscribeAndDrain(t, other, el.ID)
		return
	}

	t.Run("DeliveredToDevice", func(t *testing.T) {
		d, sender, _ := setup(t)
		v := uniqueValue()
		browserSend(sender, el.ID, v)
		f := d.Expect("browser message on the device socket", isDeviceMsg(el.ID, v))
		if _, has := f.M["type"]; has {
			t.Errorf("device frame must not carry a type field: %s", f.Raw)
		}
		auth := requireAuthObject(t, f)
		if auth["username"] != rc.Username {
			t.Errorf("auth.username = %v, want %q", auth["username"], rc.Username)
		}
		if idString(auth["user_id"]) != rc.ID {
			t.Errorf("auth.user_id = %v, want %s", auth["user_id"], rc.ID)
		}
		requireLastEdit(t, f)
	})

	t.Run("DeliveredToOtherBrowser", func(t *testing.T) {
		_, sender, other := setup(t)
		v := uniqueValue()
		browserSend(sender, el.ID, v)
		f := other.Expect("browser message on another subscriber", isElementMsg(el.ID, v))
		auth := requireAuthObject(t, f)
		if auth["username"] != rc.Username {
			t.Errorf("auth.username = %v, want %q", auth["username"], rc.Username)
		}
	})

	t.Run("NoEchoToSender", func(t *testing.T) {
		d, sender, _ := setup(t)
		v := uniqueValue()
		browserSend(sender, el.ID, v)
		d.Expect("browser message on the device socket", isDeviceMsg(el.ID, v))
		sender.ExpectNone("an echo of its own message", isElementMsg(el.ID, v))
	})

	t.Run("ReadOnlyUnauthorized", func(t *testing.T) {
		d := connectDevice(t, "main")
		b := connectBrowser(t, "r")
		subscribeAndDrain(t, b, el.ID)
		v := uniqueValue()
		browserSend(b, el.ID, v)
		f := b.Expect("unauthorized error", isType("error", el.ID))
		if f.Str("error_code") != "unauthorized" {
			t.Fatalf("error_code = %q, want unauthorized: %s", f.Str("error_code"), f.Raw)
		}
		d.ExpectNone("a message from a read-only user", isDeviceMsg(el.ID, v))
	})

	t.Run("NotSubscribedUnauthorized", func(t *testing.T) {
		d := connectDevice(t, "main")
		b := connectBrowser(t, "rc")
		v := uniqueValue()
		browserSend(b, el.ID, v)
		f := b.Expect("unauthorized error", isType("error", el.ID))
		if f.Str("error_code") != "unauthorized" {
			t.Fatalf("error_code = %q, want unauthorized: %s", f.Str("error_code"), f.Raw)
		}
		d.ExpectNone("a message from an unsubscribed socket", isDeviceMsg(el.ID, v))
	})

	t.Run("UnknownType", func(t *testing.T) {
		b := connectBrowser(t, "rc")
		b.Send(map[string]any{"type": "definitely_not_a_type", "element_id": el.ID})
		f := b.Expect("unknown_type error", isType("error", ""))
		if f.Str("error_code") != "unknown_type" {
			t.Fatalf("error_code = %q, want unknown_type: %s", f.Str("error_code"), f.Raw)
		}
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		b := connectBrowser(t, "rc")
		b.Send("{not json")
		f := b.Expect("invalid_format error", isType("error", ""))
		if f.Str("error_code") != "invalid_format" {
			t.Fatalf("error_code = %q, want invalid_format: %s", f.Str("error_code"), f.Raw)
		}
		// Socket stays usable.
		subscribe(t, b, el.ID)
	})

	t.Run("ExplicitUnsubscribe", func(t *testing.T) {
		d := connectDevice(t, "main")
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, el.ID)
		b.Send(map[string]any{"type": "unsubscribe", "element_id": el.ID})
		f := b.Expect("unsubscribe confirm", isType("unsubscribe", el.ID))
		if f.M["unsubscribe"] != true {
			t.Fatalf("unsubscribe != true: %s", f.Raw)
		}
		v := uniqueValue()
		deviceSend(d, el.ID, v)
		b.ExpectNone("data after unsubscribing", isElementMsg(el.ID, v))
	})
}

func TestSpoofing(t *testing.T) {
	t.Run("B3_DeviceCannotSetActorOrTimestamp", func(t *testing.T) {
		el := elem(t, "sensor")
		d := connectDevice(t, "main")
		b := connectBrowser(t, "rc")
		subscribeAndDrain(t, b, el.ID)

		const spoofTS = "2000-01-01T00:00:00.000Z"
		v := uniqueValue()
		d.Send(map[string]any{
			"element_id":   el.ID,
			"message":      map[string]any{"value": v},
			"auth":         map[string]any{"user_id": "spoof", "username": "admin"},
			"last_edit_at": spoofTS,
		})
		f := b.Expect("device message", isElementMsg(el.ID, v))
		auth := requireAuthObject(t, f)
		spoofedUser := auth["username"] == "admin"
		spoofedTS := f.Str("last_edit_at") == spoofTS
		checkBug(t, "B3", !spoofedUser && !spoofedTS,
			"browser received device-supplied auth.username=%v last_edit_at=%v", auth["username"], f.M["last_edit_at"])
	})
}
