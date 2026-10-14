package gateway

import (
	"bytes"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/metrics"
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

// renderMessage serializes an element message once per audience. The device
// frame is built on first use (often no device needs it) and carries the
// message in device units: when the element's pipeline transforms commands,
// its inverse runs on the message. If that fails, the device frame is nil and
// the device gets nothing, never the unconverted value.
func renderMessage(m *events.ElementMessage, at events.Time, pc pipelineContext) (device func() []byte, browser []byte) {
	auth := authJSON{UserID: actorUserID(m.Source, m.Actor.ID), Username: m.Actor.Name}
	msg := m.Message
	if len(msg) == 0 {
		msg = json.RawMessage("null")
	}
	ts := at.String()
	browser = mustJSON(browserMsgFrame{Type: "message_element", ElementID: m.ElementID.String(), Message: msg, Auth: auth, LastEditAt: ts})
	device = sync.OnceValue(func() []byte {
		devMsg := msg
		if pc.pipe.Invertible() {
			inv, err := inverseMessage(msg, pc.pipe, elementpipe.Context{Element: pc.element, Device: pc.device, Time: at.Time})
			if err != nil {
				metrics.PipelineFailed.WithLabelValues("inverse").Inc()
				return nil
			}
			devMsg = inv
		}
		return mustJSON(deviceMsgFrame{ElementID: m.ElementID.String(), Message: devMsg, Auth: auth, LastEditAt: ts})
	})
	return device, browser
}

// pipelineContext is an element's pipeline and the names its steps see.
type pipelineContext struct {
	pipe    *elementpipe.Pipeline
	element string
	device  string
}

// inverseMessage converts a message (engineering units) to device units. A
// bare JSON value is run as {"value": x} and unwrapped again.
func inverseMessage(raw json.RawMessage, pipe *elementpipe.Pipeline, ctx elementpipe.Context) (json.RawMessage, error) {
	v, err := decodeMessage(raw)
	if err != nil {
		return nil, err
	}
	msg, wrapped := elementpipe.WrapScalar(v)
	out, err := pipe.InverseWithContext(msg, ctx)
	if err != nil {
		return nil, err
	}
	var res any = out
	if inner, ok := out["value"]; wrapped && ok && len(out) == 1 {
		res = inner
	}
	return encodeMessage(res)
}

// decodeMessage decodes an element message keeping numbers exact.
func decodeMessage(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// encodeMessage encodes a message without HTML escaping (<, >, & stay as sent).
func encodeMessage(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
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
