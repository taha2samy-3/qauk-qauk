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

// Alert states.
const (
	StateFiring   = "firing"
	StateResolved = "resolved"
)

// NotificationEvent represents the canonical internal payload passed to webhooks.
type NotificationEvent struct {
	ID          uuid.UUID       `json:"id"`
	Type        string          `json:"type"` // "io.quack.alert.threshold", "io.quack.test.notification"
	Severity    Severity        `json:"severity"`
	State       string          `json:"state,omitempty"` // alerts: firing or resolved
	RuleID      *uuid.UUID      `json:"rule_id,omitempty"`
	RuleName    string          `json:"rule_name,omitempty"`
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

// Target is an endpoint with its secrets opened, ready to send to.
type Target struct {
	URL      string
	Format   string
	Secret   string            // signs every request when set
	Headers  map[string]string // added to every request
	Template string            // custom format
	ChatID   string            // telegram
}

// Options are per-format endpoint settings (stored as JSON).
type Options struct {
	ChatID string `json:"chat_id,omitempty" doc:"Telegram: the chat the bot posts to"`
}
