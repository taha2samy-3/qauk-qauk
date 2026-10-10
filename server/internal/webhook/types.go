package webhook

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// NotificationEvent represents the canonical internal payload passed to webhooks.
type NotificationEvent struct {
	ID          uuid.UUID       `json:"id"`
	Type        string          `json:"type"` // e.g. "io.quack.alert.threshold", "io.quack.device.presence"
	Severity    Severity        `json:"severity"`
	Title       string          `json:"title"`
	Message     string          `json:"message"`
	DeviceID    *uuid.UUID      `json:"device_id,omitempty"`
	DeviceName  string          `json:"device_name,omitempty"`
	ElementID   *uuid.UUID      `json:"element_id,omitempty"`
	ElementName string          `json:"element_name,omitempty"`
	Value       any             `json:"value,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}
