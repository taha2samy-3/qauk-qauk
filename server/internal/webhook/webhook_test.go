package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sampleEvent() NotificationEvent {
	devID, elemID := uuid.New(), uuid.New()
	return NotificationEvent{
		ID: uuid.New(), Type: "io.quack.alert.threshold", Severity: SeverityCritical, State: StateFiring,
		Title: "High Temperature Alarm", Message: "Temperature exceeded 35°C",
		DeviceID: &devID, DeviceName: "HVAC Unit 1", ElementID: &elemID, ElementName: "Temperature",
		Value: 36.8, Timestamp: time.Now().UTC(),
	}
}

func TestSignAndVerify(t *testing.T) {
	secret := NewSecret()
	if err := ValidateSecret(secret); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"test":"payload"}`)
	now := time.Now()
	ts, sig, err := SignStandardWebhook(secret, "msg_1", now, body)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyStandardWebhook(secret, "msg_1", ts, sig, body, now, SignatureTolerance) {
		t.Fatal("a fresh signature must verify")
	}
	if VerifyStandardWebhook(secret, "msg_1", ts, sig, []byte(`{"tampered":true}`), now, SignatureTolerance) {
		t.Fatal("a tampered body must not verify")
	}
	if VerifyStandardWebhook(secret, "msg_1", ts, sig, body, now.Add(10*time.Minute), SignatureTolerance) {
		t.Fatal("an old timestamp must not verify (replay)")
	}
	if !VerifyStandardWebhook(secret, "msg_1", ts, "v1,old "+sig, body, now, SignatureTolerance) {
		t.Fatal("any of several signatures may match (key rotation)")
	}
}

func TestSecretsMustBeStandard(t *testing.T) {
	for _, s := range []string{"whsec_sample123", "plain-secret", "whsec_" + strings.Repeat("A", 8)} {
		if ValidateSecret(s) == nil {
			t.Errorf("%q must be refused", s)
		}
		if _, _, err := SignStandardWebhook(s, "m", time.Now(), nil); err == nil {
			t.Errorf("%q must not sign with some other key", s)
		}
	}
}

func TestStandardPayloadIsSignedPerAttempt(t *testing.T) {
	evt := sampleEvent()
	evt.Timestamp = time.Now().Add(-time.Hour) // an old event, retried now
	secret := NewSecret()
	body, h, err := FormatPayload(Target{Format: "standard", Secret: secret}, evt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if h.Get("webhook-id") != evt.ID.String() {
		t.Fatal("webhook-id is the event id (stable across retries)")
	}
	if !VerifyStandardWebhook(secret, h.Get("webhook-id"), h.Get("webhook-timestamp"), h.Get("webhook-signature"), body, time.Now(), SignatureTolerance) {
		t.Fatal("a retry of an old event must verify now: it is signed with the attempt's time")
	}
	var ce map[string]any
	if err := json.Unmarshal(body, &ce); err != nil || ce["specversion"] != "1.0" {
		t.Fatalf("not a CloudEvent: %s", body)
	}
}

func TestTelegramEscapesAndNeedsChatID(t *testing.T) {
	evt := sampleEvent()
	evt.DeviceName = `R&D lab <a href="https://evil.example">`
	evt.Message = "Temp < 5"
	if _, _, err := FormatPayload(Target{Format: "telegram"}, evt, time.Now()); err == nil {
		t.Fatal("telegram without chat_id must fail")
	}
	body, _, err := FormatPayload(Target{Format: "telegram", ChatID: "-100123"}, evt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	_ = json.Unmarshal(body, &p)
	if p.ChatID != "-100123" || strings.Contains(p.Text, "<a ") || !strings.Contains(p.Text, "R&amp;D lab &lt;a") || !strings.Contains(p.Text, "Temp &lt; 5") {
		t.Fatalf("telegram body: %+v", p)
	}
}

func TestSlackEscapesMrkdwn(t *testing.T) {
	evt := sampleEvent()
	evt.ElementName = "<!channel> & co"
	body, _, err := FormatPayload(Target{Format: "slack"}, evt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Attachments []struct {
			Blocks []struct {
				Text struct{ Text string } `json:"text"`
			} `json:"blocks"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	fields := p.Attachments[0].Blocks[2].Text.Text
	if strings.Contains(fields, "<!channel>") || !strings.Contains(fields, `&lt;!channel&gt; &amp; co`) {
		t.Fatalf("slack fields: %s", fields)
	}
}

func TestOtherFormatsRender(t *testing.T) {
	for _, f := range []string{"discord", "teams"} {
		body, _, err := FormatPayload(Target{Format: f}, sampleEvent(), time.Now())
		if err != nil || !json.Valid(body) {
			t.Fatalf("%s: %v %s", f, err, body)
		}
	}
}

func TestCustomTemplates(t *testing.T) {
	evt := sampleEvent()
	evt.Title = `Alert: 5" pipe`
	evt.Message = strings.Repeat("m", 1000)
	body, err := RenderTemplate(`{"alarm": {{json .Title}}, "value": {{json .Value}}}`, evt)
	if err != nil || !json.Valid(body) {
		t.Fatalf("%v %s", err, body)
	}
	for src, want := range map[string]string{
		`{"alarm": "{{.Title}}"}`:                              "isn't valid JSON",
		`{{range 200000}}xxxxxxxxxx{{end}}`:                    "range isn't allowed",
		`{{define "x"}}{{end}}{"a":1}`:                         "define",
		`{"a": {{json .Nope}}}`:                                "Nope",
		`{"a": "` + strings.Repeat("{{.Message}}", 100) + `"}`: "more than 64 KiB",
		strings.Repeat("x", 17<<10):                            "over 16 KiB",
		``:                                                     "needs a template",
	} {
		if _, err := RenderTemplate(src, evt); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.40q: %v (want %q)", src, err, want)
		}
	}
}

func TestPublicAddressesOnly(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1/x", "https://127.0.0.1/x", "https://169.254.169.254/latest", "https://10.1.2.3/",
		"https://[::1]/", "https://localhost/", "https://redpanda/", "https://db.internal/", "ftp://example.com/"} {
		if ValidateURL(u, false) == nil {
			t.Errorf("%s must be refused", u)
		}
	}
	for _, u := range []string{"https://hooks.slack.com/services/T/B/x", "https://93.184.216.34/hook"} {
		if err := ValidateURL(u, false); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	if ValidateURL("http://127.0.0.1:9000/x", true) != nil {
		t.Error("QUACK_WEBHOOK_ALLOW_PRIVATE allows local http")
	}
}

// The address is checked on the connection itself: a name that resolves to a
// private address is refused, and the error doesn't carry the URL.
func TestClientRefusesPrivateTargetsAndKeepsTheURLOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("INTERNAL")) }))
	defer srv.Close()
	d := NewDispatcher(nil, false)
	res := d.Send(context.Background(), Target{URL: srv.URL + "/bot123:SECRETTOKEN/sendMessage", Format: "standard"}, sampleEvent())
	if res.Err == nil || res.Body != "" {
		t.Fatalf("a loopback target must be refused, got %+v", res)
	}
	if strings.Contains(res.Err.Error(), "SECRETTOKEN") || !strings.Contains(res.Err.Error(), "not a public address") {
		t.Fatalf("error: %v", res.Err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("INTERNAL")) }))
	defer internal.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer front.Close()
	res := NewDispatcher(nil, true).Send(context.Background(), Target{URL: front.URL, Format: "standard"}, sampleEvent())
	if res.StatusCode != http.StatusFound || strings.Contains(res.Body, "INTERNAL") {
		t.Fatalf("the redirect must be the answer, not followed: %+v", res)
	}
}

func TestResultClassification(t *testing.T) {
	code := http.StatusOK
	retryAfter := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()
	d := NewDispatcher(nil, true)
	send := func() Result {
		return d.Send(context.Background(), Target{URL: srv.URL, Format: "standard"}, sampleEvent())
	}
	if r := send(); r.Err != nil || r.StatusCode != 200 {
		t.Fatalf("2xx: %+v", r)
	}
	for c, terminal := range map[int]bool{400: true, 401: true, 404: true, 410: true, 408: false, 429: false, 500: false, 503: false} {
		code = c
		if r := send(); r.Err == nil || r.Terminal != terminal {
			t.Errorf("%d: terminal=%v, want %v", c, r.Terminal, terminal)
		}
	}
	code, retryAfter = 429, "120"
	if r := send(); r.RetryAfter != 2*time.Minute {
		t.Errorf("Retry-After: %v", r.RetryAfter)
	}
}

func TestBackoff(t *testing.T) {
	for attempt, want := range map[int]time.Duration{0: BaseRetryDelay, 3: 8 * BaseRetryDelay, 20: MaxRetryDelay} {
		d := ComputeBackoff(attempt)
		if d < want*8/10 || d > want*12/10 {
			t.Errorf("attempt %d: %v, want %v ±20%%", attempt, d, want)
		}
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://hooks.slack.com/services/T0/B0/abcdSECRET": "https://hooks.slack.com/…CRET",
		"https://example.com":                               "https://example.com",
		"not a url":                                         "…",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
