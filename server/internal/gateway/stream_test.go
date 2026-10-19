package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

func TestStreamRender(t *testing.T) {
	s := &streamState{}
	dev := mustJSON(browserMsgFrame{Type: "message_element", ElementID: "e1", Message: json.RawMessage(`{"value":2}`),
		Auth: authJSON{UserID: json.RawMessage(`"d-1"`), Username: "Boiler"}, LastEditAt: "2026-10-19T09:00:00.000Z"})
	m := decode(t, s.render(dev, false))
	if m["type"] != "value" || m["source"] != "device" || m["actor"] != "Boiler" || m["time"] != "2026-10-19T09:00:00.000Z" || m["replay"] != nil {
		t.Fatalf("device value: %v", m)
	}
	user := mustJSON(browserMsgFrame{Type: "message_element", ElementID: "e1", Message: json.RawMessage(`1`),
		Auth: authJSON{UserID: json.RawMessage(`7`), Username: "alice"}})
	if m := decode(t, s.render(user, true)); m["source"] != "user" || m["replay"] != true {
		t.Fatalf("user command: %v", m)
	}
	for frame, want := range map[string]string{
		string(connStatusFrame("e1", true)):              `{"type":"presence","element_id":"e1","connected":true}`,
		string(unsubscribeFrame("e1", "Revoked")):        `{"type":"unsubscribed","elements":["e1"],"reason":"Revoked"}`,
		string(permissionsFrame("e1", "R")):              `{"type":"permission","element_id":"e1","permission":"R"}`,
		string(errorFrame("delivery_failed", "x", "e1")): `{"type":"error","code":"delivery_failed","description":"x","element_id":"e1"}`,
	} {
		if got := string(s.render([]byte(frame), false)); got != want {
			t.Errorf("%s\n got %s\nwant %s", frame, got, want)
		}
	}
	if s.render(subscribeFrame("e1", "R", nil, true), false) != nil {
		t.Fatal("per-element confirmations aren't sent on streams")
	}
}

func TestStreamReplay(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	entry := func(i int) ringEntry {
		return ringEntry{id: uuid.New(), at: t0.Add(time.Duration(i) * time.Second), frame: mustJSON(browserMsgFrame{Type: "message_element",
			ElementID: "e1", Message: json.RawMessage(`{"value":` + string(rune('0'+i)) + `}`), Auth: authJSON{UserID: json.RawMessage(`"d"`)}})}
	}
	ring := []ringEntry{entry(1), entry(2), entry(3)}
	s := &streamState{}
	if got := s.replay(ring); len(got) != 1 || decode(t, got[0])["message"].(map[string]any)["value"] != 3.0 {
		t.Fatalf("without since: the newest only, got %d", len(got))
	}
	since := t0.Add(time.Second)
	s.since = &since
	if got := s.replay(ring); len(got) != 2 {
		t.Fatalf("since: the later ones, got %d", len(got))
	}
}
