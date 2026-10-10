package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/taha2samy/quackquack/server/internal/store"
	"github.com/taha2samy/quackquack/server/internal/webhook"
)

// Evaluator evaluates telemetry against active element alert rules and enqueues webhook deliveries.
type Evaluator struct {
	mu           sync.RWMutex
	activeStates map[uuid.UUID]bool // rule.ID -> currently in alarm
}

func NewEvaluator() *Evaluator {
	return &Evaluator{
		activeStates: make(map[uuid.UUID]bool),
	}
}

// Evaluate checks if a numeric value matches the rule's threshold condition.
func (e *Evaluator) CheckCondition(rule store.ElementAlertRule, val float64) (triggered bool, recovered bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	inAlarm := e.activeStates[rule.ID]

	switch rule.Condition {
	case "above":
		if val > rule.Threshold {
			if !inAlarm {
				e.activeStates[rule.ID] = true
				return true, false
			}
		} else if val <= rule.Threshold-rule.Hysteresis {
			if inAlarm {
				e.activeStates[rule.ID] = false
				return false, true
			}
		}

	case "below":
		if val < rule.Threshold {
			if !inAlarm {
				e.activeStates[rule.ID] = true
				return true, false
			}
		} else if val >= rule.Threshold+rule.Hysteresis {
			if inAlarm {
				e.activeStates[rule.ID] = false
				return false, true
			}
		}

	case "outside_range":
		maxThresh := rule.Threshold
		if rule.ThresholdMax != nil {
			maxThresh = *rule.ThresholdMax
		}
		if val < rule.Threshold || val > maxThresh {
			if !inAlarm {
				e.activeStates[rule.ID] = true
				return true, false
			}
		} else if val >= rule.Threshold+rule.Hysteresis && val <= maxThresh-rule.Hysteresis {
			if inAlarm {
				e.activeStates[rule.ID] = false
				return false, true
			}
		}

	case "equals":
		if val == rule.Threshold {
			if !inAlarm {
				e.activeStates[rule.ID] = true
				return true, false
			}
		} else {
			if inAlarm {
				e.activeStates[rule.ID] = false
				return false, true
			}
		}
	}

	return false, false
}

// TriggerAndEnqueue creates notification events and enqueues deliveries to matching webhooks.
func (e *Evaluator) TriggerAndEnqueue(
	ctx context.Context,
	db store.DBTX,
	rule store.ElementAlertRule,
	val float64,
	deviceName string,
	elementName string,
	isRecovery bool,
) error {
	severity := webhook.Severity(rule.Severity)
	title := fmt.Sprintf("Alert Triggered: %s", rule.Name)
	msg := rule.Message
	if msg == "" {
		msg = fmt.Sprintf("Value %v triggered condition '%s' (threshold: %v)", val, rule.Condition, rule.Threshold)
	}

	if isRecovery {
		severity = webhook.SeverityInfo
		title = fmt.Sprintf("Alert Cleared: %s", rule.Name)
		msg = fmt.Sprintf("Element has returned to normal range (current value: %v)", val)
	}

	evt := webhook.NotificationEvent{
		ID:          uuid.New(),
		Type:        "io.quack.alert.threshold",
		Severity:    severity,
		Title:       title,
		Message:     msg,
		DeviceName:  deviceName,
		ElementName: elementName,
		ElementID:   &rule.ElementID,
		Value:       val,
		Timestamp:   time.Now().UTC(),
	}

	payloadRaw, err := json.Marshal(evt)
	if err != nil {
		return err
	}

	// Fetch all enabled webhook endpoints
	endpoints, err := store.ListWebhookEndpoints(ctx, db)
	if err != nil {
		return err
	}

	for _, ep := range endpoints {
		if !ep.Enabled {
			continue
		}

		// Check if endpoint is subscribed to this severity
		subscribed := false
		for _, s := range ep.Severities {
			if s == string(severity) {
				subscribed = true
				break
			}
		}
		if !subscribed {
			continue
		}

		// Enqueue delivery
		_, _ = store.EnqueueWebhookDelivery(ctx, db, store.WebhookDelivery{
			EndpointID: ep.ID,
			EventID:    evt.ID,
			EventType:  evt.Type,
			Severity:   string(severity),
			Payload:    payloadRaw,
			Status:     "pending",
		})
	}

	return nil
}
