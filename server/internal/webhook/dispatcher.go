package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type Dispatcher struct {
	client     *http.Client
	baseDelay  time.Duration
	maxDelay   time.Duration
	maxRetries int
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseDelay:  10 * time.Second,
		maxDelay:   1 * time.Hour,
		maxRetries: 5,
	}
}

// ComputeBackoff calculates exponential backoff with full jitter.
func (d *Dispatcher) ComputeBackoff(attempt int) time.Duration {
	mult := math.Pow(2, float64(attempt))
	delay := float64(d.baseDelay) * mult
	if delay > float64(d.maxDelay) {
		delay = float64(d.maxDelay)
	}

	// Apply jitter: 80% to 120% of calculated delay
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(delay * jitter)
}

// Deliver sends a single delivery attempt to the endpoint.
func (d *Dispatcher) Deliver(ctx context.Context, ep store.WebhookEndpoint, dlv store.WebhookDelivery) (int, error, int) {
	var evt NotificationEvent
	if err := json.Unmarshal(dlv.Payload, &evt); err != nil {
		// Fallback if payload isn't strict NotificationEvent
		evt = NotificationEvent{
			ID:        dlv.EventID,
			Type:      dlv.EventType,
			Severity:  Severity(dlv.Severity),
			Title:     dlv.EventType,
			Message:   string(dlv.Payload),
			Timestamp: dlv.CreatedAt,
		}
	}

	var customHeaders map[string]string
	if len(ep.Headers) > 0 {
		_ = json.Unmarshal(ep.Headers, &customHeaders)
	}

	body, headers, err := FormatPayload(ep.Format, ep.Secret, evt, ep.CustomTemplate, customHeaders)
	if err != nil {
		return 0, fmt.Errorf("format payload: %w", err), 0
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err), 0
	}

	for k, v := range headers {
		req.Header[k] = v
	}

	start := time.Now()
	resp, err := d.client.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())

	if err != nil {
		return 0, err, latencyMs
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil, latencyMs
	}

	// Read truncated error body for diagnostics
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody)), latencyMs
}

// ProcessDelivery dispatches a delivery and updates its status in the store.
func (d *Dispatcher) ProcessDelivery(ctx context.Context, db store.DBTX, ep store.WebhookEndpoint, dlv store.WebhookDelivery) error {
	statusCode, err, latencyMs := d.Deliver(ctx, ep, dlv)
	if err == nil {
		return store.RecordDeliverySuccess(ctx, db, dlv.ID, statusCode, latencyMs)
	}

	// Determine if error is non-retryable (4xx except 429)
	var sc *int
	if statusCode > 0 {
		sc = &statusCode
	}

	// 429 (rate limit) or 5xx/network errors are retryable
	retryable := statusCode == 0 || statusCode == http.StatusTooManyRequests || statusCode >= 500
	if !retryable {
		// Non-retryable: exhaust attempts immediately
		dlv.MaxAttempts = dlv.Attempts + 1
	}

	backoff := d.ComputeBackoff(dlv.Attempts)
	return store.RecordDeliveryFailure(ctx, db, dlv.ID, sc, err.Error(), latencyMs, backoff)
}

// SendTest sends an immediate test notification to an endpoint and returns the result without enqueueing.
func (d *Dispatcher) SendTest(ctx context.Context, ep store.WebhookEndpoint, severity Severity) (int, string, int, error) {
	if severity == "" {
		severity = SeverityInfo
	}

	evt := NotificationEvent{
		ID:          uuid.New(),
		Type:        "io.quack.test.notification",
		Severity:    severity,
		Title:       "Test Webhook Delivery",
		Message:     "This is a verified test notification sent from Quack Quack IoT platform.",
		DeviceName:  "Demo Greenhouse Gateway",
		ElementName: "Soil Moisture",
		Value:       42.5,
		Timestamp:   time.Now().UTC(),
	}

	var customHeaders map[string]string
	if len(ep.Headers) > 0 {
		_ = json.Unmarshal(ep.Headers, &customHeaders)
	}

	body, headers, err := FormatPayload(ep.Format, ep.Secret, evt, ep.CustomTemplate, customHeaders)
	if err != nil {
		return 0, "", 0, fmt.Errorf("format payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return 0, "", 0, fmt.Errorf("create request: %w", err)
	}

	for k, v := range headers {
		req.Header[k] = v
	}

	start := time.Now()
	resp, err := d.client.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return 0, "", latencyMs, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, string(respBody), latencyMs, nil
	}
	return resp.StatusCode, string(respBody), latencyMs, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
}

// StartWorker starts a resilient background goroutine processing pending deliveries.
func (d *Dispatcher) StartWorker(ctx context.Context, db store.DBTX, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deliveries, err := store.FetchPendingDeliveries(ctx, db, 20)
			if err != nil || len(deliveries) == 0 {
				continue
			}

			for _, dlv := range deliveries {
				ep, err := store.GetWebhookEndpoint(ctx, db, dlv.EndpointID)
				if err != nil {
					if errors.Is(err, store.ErrNotFound) {
						// Endpoint was deleted
						_ = store.RecordDeliveryFailure(ctx, db, dlv.ID, nil, "endpoint not found", 0, 0)
					}
					continue
				}

				if !ep.Enabled {
					continue
				}

				_ = d.ProcessDelivery(ctx, db, ep, dlv)
			}
		}
	}
}
