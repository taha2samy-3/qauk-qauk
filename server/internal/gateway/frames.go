package gateway

import (
	"encoding/json"
	"strconv"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
)

// Wire frames are byte-compatible with the legacy Django consumers
// (node_red/consumers/{nodered,browser}.py), including field order.

type authJSON struct {
	UserID   json.RawMessage `json:"user_id"`
	Username string          `json:"username"`
}

type deviceMsgFrame struct {
	ElementID  string          `json:"element_id"`
	Message    json.RawMessage `json:"message"`
	Auth       authJSON        `json:"auth"`
	LastEditAt string          `json:"last_edit_at"`
}

type browserMsgFrame struct {
	Type       string          `json:"type"`
	ElementID  string          `json:"element_id"`
	Message    json.RawMessage `json:"message"`
	Auth       authJSON        `json:"auth"`
	LastEditAt string          `json:"last_edit_at"`
}

// actorUserID renders auth.user_id: a number for users (Django user pk), a
// string for devices.
func actorUserID(source, id string) json.RawMessage {
	if source == events.SourceUser {
		if _, err := strconv.ParseInt(id, 10, 64); err == nil {
			return json.RawMessage(id)
		}
	}
	b, _ := json.Marshal(id)
	return b
}

// renderMessage serializes an element message once per audience.
func renderMessage(m *events.ElementMessage, at events.Time, pipe *elementpipe.Pipeline) (device, browser []byte) {
	auth := authJSON{UserID: actorUserID(m.Source, m.Actor.ID), Username: m.Actor.Name}
	msg := m.Message
	if len(msg) == 0 {
		msg = json.RawMessage("null")
	}
	devMsg := msg
	if m.Source == events.SourceUser && pipe != nil {
		var parsed map[string]any
		if err := json.Unmarshal(msg, &parsed); err == nil {
			if inv, err := pipe.Inverse(parsed); err == nil {
				if b, err := json.Marshal(inv); err == nil {
					devMsg = b
				}
			}
		}
	}
	ts := at.String()
	device = mustJSON(deviceMsgFrame{ElementID: m.ElementID.String(), Message: devMsg, Auth: auth, LastEditAt: ts})
	browser = mustJSON(browserMsgFrame{Type: "message_element", ElementID: m.ElementID.String(), Message: msg, Auth: auth, LastEditAt: ts})
	return device, browser
}

func subscribeFrame(elementID, perm string, details json.RawMessage, connected bool) []byte {
	if len(details) == 0 {
		details = json.RawMessage("null")
	}
	return mustJSON(struct {
		Type        string          `json:"type"`
		ElementID   string          `json:"element_id"`
		Subscribed  bool            `json:"subscribed"`
		Permissions string          `json:"permissions"`
		Details     json.RawMessage `json:"details"`
		Connected   bool            `json:"connected"`
	}{"subscribe", elementID, true, perm, details, connected})
}

func unsubscribeFrame(elementID, reason string) []byte {
	return mustJSON(struct {
		Type        string `json:"type"`
		ElementID   string `json:"element_id"`
		Unsubscribe bool   `json:"unsubscribe"`
		Reason      string `json:"reason,omitempty"`
	}{"unsubscribe", elementID, true, reason})
}

func permissionsFrame(elementID, perm string) []byte {
	return mustJSON(struct {
		Type        string `json:"type"`
		ElementID   string `json:"element_id"`
		Permissions string `json:"permissions"`
	}{"permissions_update", elementID, perm})
}

func connStatusFrame(elementID string, connected bool) []byte {
	status := "disconnected"
	if connected {
		status = "connected"
	}
	return mustJSON(struct {
		Type      string `json:"type"`
		Status    string `json:"status"`
		ElementID string `json:"element_id"`
	}{"element_connection_status", status, elementID})
}

func errorFrame(code, description, elementID string) []byte {
	return mustJSON(struct {
		Type        string `json:"type"`
		ErrorCode   string `json:"error_code"`
		Description string `json:"description"`
		ElementID   string `json:"element_id,omitempty"`
	}{"error", code, description, elementID})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only static, always-encodable types are passed here
	}
	return b
}
