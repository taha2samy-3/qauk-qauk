package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taha2samy/quackquack/server/internal/store"
)

func sampleEvent() NotificationEvent {
	devID := uuid.New()
	elemID := uuid.New()
	return NotificationEvent{
		ID:          uuid.New(),
		Type:        "io.quack.alert.threshold",
		Severity:    SeverityCritical,
		Title:       "High Temperature Alarm",
		Message:     "Temperature exceeded 35°C threshold",
		DeviceID:    &devID,
		DeviceName:  "HVAC Unit 1",
		ElementID:   &elemID,
		ElementName: "Temperature",
		Value:       36.8,
		Timestamp:   time.Now().UTC(),
	}
}

func TestSignAndVerifyStandardWebhook(t *testing.T) {
	secret := "whsec_MfKQ9r8GKYqrTwjUpuhbFdqqUkjrAwKS"
	msgID := uuid.New().String()
	now := time.Now().UTC()
	body := []byte(`{"test": "payload"}`)

	id, ts, sig := SignStandardWebhook(secret, msgID, now, body)
	if id != msgID {
		t.Fatalf("expected msgID %s, got %s", msgID, id)
	}

	valid := VerifyStandardWebhook(secret, id, ts, sig, body)
	if !valid {
		t.Fatalf("expected valid signature, got invalid")
	}

	// Tampered body should fail verification
	invalid := VerifyStandardWebhook(secret, id, ts, sig, []byte(`{"tampered": true}`))
	if invalid {
		t.Fatalf("expected signature verification to fail on tampered body")
	}
}

func TestFormatStandard(t *testing.T) {
	evt := sampleEvent()
	secret := "whsec_test123456789"
	body, headers, err := FormatPayload("standard", secret, evt, nil, nil)
	if err != nil {
		t.Fatalf("format standard: %v", err)
	}

	if headers.Get("webhook-id") != evt.ID.String() {
		t.Errorf("expected webhook-id %s, got %s", evt.ID.String(), headers.Get("webhook-id"))
	}
	if headers.Get("webhook-signature") == "" {
		t.Errorf("expected webhook-signature header")
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if parsed["specversion"] != "1.0" {
		t.Errorf("expected CloudEvent specversion 1.0, got %v", parsed["specversion"])
	}
}

func TestFormatSlack(t *testing.T) {
	evt := sampleEvent()
	body, headers, err := FormatPayload("slack", "", evt, nil, nil)
	if err != nil {
		t.Fatalf("format slack: %v", err)
	}

	if headers.Get("Content-Type") != "application/json" {
		t.Errorf("unexpected content-type: %s", headers.Get("Content-Type"))
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid slack json: %v", err)
	}
	if parsed["attachments"] == nil {
		t.Errorf("missing attachments in slack payload")
	}
}

func TestFormatDiscord(t *testing.T) {
	evt := sampleEvent()
	body, _, err := FormatPayload("discord", "", evt, nil, nil)
	if err != nil {
		t.Fatalf("format discord: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid discord json: %v", err)
	}
	embeds, ok := parsed["embeds"].([]any)
	if !ok || len(embeds) == 0 {
		t.Fatalf("expected embeds in discord payload")
	}
}

func TestFormatTeams(t *testing.T) {
	evt := sampleEvent()
	body, _, err := FormatPayload("teams", "", evt, nil, nil)
	if err != nil {
		t.Fatalf("format teams: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid teams json: %v", err)
	}
	if parsed["type"] != "message" {
		t.Errorf("expected type message, got %v", parsed["type"])
	}
}

func TestFormatTelegram(t *testing.T) {
	evt := sampleEvent()
	body, _, err := FormatPayload("telegram", "", evt, nil, nil)
	if err != nil {
		t.Fatalf("format telegram: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid telegram json: %v", err)
	}
	if parsed["parse_mode"] != "HTML" {
		t.Errorf("expected HTML parse_mode, got %v", parsed["parse_mode"])
	}
}

func TestFormatCustomTemplate(t *testing.T) {
	evt := sampleEvent()
	tpl := `{"alarm": "{{.Title}}", "temp": {{.Value}}}`
	headers := map[string]string{"X-Custom-Key": "secret123"}
	body, reqHeaders, err := FormatPayload("custom", "", evt, &tpl, headers)
	if err != nil {
		t.Fatalf("format custom: %v", err)
	}

	if reqHeaders.Get("X-Custom-Key") != "secret123" {
		t.Errorf("expected custom header")
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid custom template output: %v", err)
	}
	if parsed["alarm"] != evt.Title {
		t.Errorf("expected alarm %s, got %v", evt.Title, parsed["alarm"])
	}
}

func TestSendTest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()

	d := NewDispatcher()
	ep := store.WebhookEndpoint{
		ID:         uuid.New(),
		Name:       "Test Webhook",
		URL:        server.URL,
		Format:     "standard",
		Secret:     "whsec_test",
		Severities: []string{"info", "warning", "critical"},
		Enabled:    true,
	}

	code, resp, latency, err := d.SendTest(context.Background(), ep, SeverityWarning)
	if err != nil {
		t.Fatalf("send test failed: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", code)
	}
	if resp != `{"ok": true}` {
		t.Errorf("unexpected response body: %s", resp)
	}
	if latency < 0 {
		t.Errorf("invalid latency: %d", latency)
	}
}

func TestComputeBackoff(t *testing.T) {
	d := NewDispatcher()
	// Attempt 0: base 10s * 1 = 10s (+/- 20% jitter -> 8s to 12s)
	b0 := d.ComputeBackoff(0)
	if b0 < 7*time.Second || b0 > 13*time.Second {
		t.Errorf("expected backoff ~10s for attempt 0, got %v", b0)
	}

	// Attempt 2: base 10s * 4 = 40s (+/- 20% jitter -> 32s to 48s)
	b2 := d.ComputeBackoff(2)
	if b2 < 30*time.Second || b2 > 50*time.Second {
		t.Errorf("expected backoff ~40s for attempt 2, got %v", b2)
	}

	// Attempt 10: should cap at maxDelay (1 hour +/- 20%)
	b10 := d.ComputeBackoff(10)
	if b10 > 75*time.Minute {
		t.Errorf("expected backoff capped near 1 hour, got %v", b10)
	}
}

func TestDeliverSuccessAndFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/bad-request", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad request`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	d := NewDispatcher()
	evt := sampleEvent()
	raw, _ := json.Marshal(evt)

	// 1. Successful delivery
	epOK := store.WebhookEndpoint{
		ID:         uuid.New(),
		URL:        server.URL + "/ok",
		Format:     "standard",
		Secret:     "whsec_abc",
		Severities: []string{"critical"},
		Enabled:    true,
	}
	dlv := store.WebhookDelivery{
		ID:        uuid.New(),
		EventID:   evt.ID,
		EventType: evt.Type,
		Severity:  "critical",
		Payload:   raw,
	}

	code, _, err := d.Deliver(context.Background(), epOK, dlv)
	if err != nil {
		t.Fatalf("expected delivery success, got %v", err)
	}
	if code != 200 {
		t.Errorf("expected 200, got %d", code)
	}

	// 2. Failed delivery (400)
	epFail := epOK
	epFail.URL = server.URL + "/bad-request"
	code, _, err = d.Deliver(context.Background(), epFail, dlv)
	if err == nil {
		t.Fatalf("expected delivery error on 400")
	}
	if code != 400 {
		t.Errorf("expected 400, got %d", code)
	}
}
