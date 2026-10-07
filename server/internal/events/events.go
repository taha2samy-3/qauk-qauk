// Package events defines the CloudEvents 1.0 envelope and the event catalog
// used on the Redpanda bus. See docs/refactor/MESSAGE_FORMATS.md.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	TopicElementEvents = "element-events.v1"
	TopicControlEvents = "control-events.v1"
	TopicPresence      = "presence.v1"
	// TopicElementState holds the latest stored device message per element
	// (compacted, keyed by element id): gateways warm up from it on start.
	TopicElementState = "element-state.v1"
	// TopicElementEventsDLQ receives element events the history store
	// rejected, unchanged, with the reason in the "error" header.
	TopicElementEventsDLQ = "element-events.dlq.v1"

	TypeElementMessage = "io.quack.element.message.v1"
	TypeControlChanged = "io.quack.control.changed.v1"
	TypeDevicePresence = "io.quack.device.presence.v1"

	SourceAPI         = "/quack/api"
	ContentTypeHeader = "application/cloudevents+json"
)

func GatewaySource(gatewayID string) string { return "/quack/gateway/" + gatewayID }

// SchemaURN is the dataschema / JSON Schema $id for an event type.
func SchemaURN(eventType string) string { return "urn:quack:schema:" + eventType }

// Time marshals as RFC 3339 UTC with millisecond precision (2026-10-07T10:00:00.123Z).
type Time struct{ time.Time }

const TimeLayout = "2006-01-02T15:04:05.000Z"

func Now() Time { return Time{time.Now().UTC()} }

func (t Time) String() string { return t.UTC().Format(TimeLayout) }

func (t Time) MarshalJSON() ([]byte, error) { return []byte(`"` + t.String() + `"`), nil }

func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	t.Time = parsed.UTC()
	return nil
}

// Event is a CloudEvents 1.0 event in structured JSON mode.
type Event struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            Time            `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	DataSchema      string          `json:"dataschema"`
	PartitionKey    string          `json:"partitionkey"`
	Data            json.RawMessage `json:"data"`
}

// New builds an event with a UUIDv7 id. The subject doubles as the partition key.
func New(eventType, source, subject string, data any) (*Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("events: marshal data: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	return &Event{
		SpecVersion:     "1.0",
		ID:              id.String(),
		Source:          source,
		Type:            eventType,
		Subject:         subject,
		Time:            Now(),
		DataContentType: "application/json",
		DataSchema:      SchemaURN(eventType),
		PartitionKey:    subject,
		Data:            raw,
	}, nil
}

func (e *Event) DecodeData(v any) error { return json.Unmarshal(e.Data, v) }

// --- Data payloads -----------------------------------------------------------

const (
	SourceDevice = "device"
	SourceUser   = "user"
)

type Actor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Origin struct {
	GatewayID string `json:"gateway_id"`
	ConnID    string `json:"conn_id"`
}

type ElementMessage struct {
	ElementID uuid.UUID       `json:"element_id"`
	DeviceID  uuid.UUID       `json:"device_id"`
	Source    string          `json:"source"`
	Actor     Actor           `json:"actor"`
	Origin    Origin          `json:"origin"`
	Message   json.RawMessage `json:"message"`
	ClientTS  *Time           `json:"client_ts"`
}

// Control kinds and ops.
const (
	KindDevice          = "device"
	KindElement         = "element"
	KindPermission      = "permission"
	KindGroupMembership = "group_membership"
	KindGroup           = "group"
	KindUser            = "user"
	KindJWTKey          = "jwt_key"

	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"
)

type ControlChanged struct {
	Kind      string     `json:"kind"`
	Op        string     `json:"op"`
	ID        string     `json:"id"`
	ElementID *uuid.UUID `json:"element_id,omitempty"`
	DeviceID  *uuid.UUID `json:"device_id,omitempty"`
	UserID    *int64     `json:"user_id,omitempty"`
	GroupID   *int64     `json:"group_id,omitempty"`
}

type DevicePresence struct {
	DeviceID  uuid.UUID `json:"device_id"`
	Connected bool      `json:"connected"`
	GatewayID string    `json:"gateway_id"`
}

// Control is a convenience constructor for control-change events from the API.
func Control(c ControlChanged) (*Event, error) {
	return New(TypeControlChanged, SourceAPI, c.ID, c)
}
