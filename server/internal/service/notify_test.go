package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/taha2samy/quackquack/server/internal/secrets"
	"github.com/taha2samy/quackquack/server/internal/store"
	"github.com/taha2samy/quackquack/server/internal/webhook"
)

func ptr[T any](v T) *T { return &v }

func TestAlertRuleValidation(t *testing.T) {
	ok := AlertRuleInput{Name: "Hot", Condition: "above", Threshold: 30, Severity: "warning"}
	if err := validateAlertRule(ok); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"value", "humidity", "gps.lat", "sensors[0].t"} {
		in := ok
		in.Field = f
		if err := validateAlertRule(in); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	for name, in := range map[string]AlertRuleInput{
		"no max":              {Name: "x", Condition: "outside_range", Threshold: 10, Severity: "info"},
		"max below min":       {Name: "x", Condition: "outside_range", Threshold: 10, ThresholdMax: ptr(5.0), Severity: "info"},
		"never clears":        {Name: "x", Condition: "outside_range", Threshold: 10, ThresholdMax: ptr(30.0), Hysteresis: 15, Severity: "info"},
		"negative hysteresis": {Name: "x", Condition: "above", Hysteresis: -1, Severity: "info"},
		"unknown condition":   {Name: "x", Condition: "between", Severity: "info"},
		"unknown severity":    {Name: "x", Condition: "above", Severity: "fatal"},
		"max on above":        {Name: "x", Condition: "above", ThresholdMax: ptr(1.0), Severity: "info"},
		"no name":             {Condition: "above", Severity: "info"},
		"field with space":    {Name: "x", Field: "a b", Condition: "above", Severity: "info"},
		"field with wildcard": {Name: "x", Field: "sensors[*].t", Condition: "above", Severity: "info"},
	} {
		if validateAlertRule(in) == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func newBoxService(t *testing.T) *Service {
	t.Helper()
	box, err := secrets.NewBox("ZGV2LW9ubHktcXVhY2stc2VjcmV0cy1rZXktMDAwMDA=")
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Secrets: box}
}

func TestWebhookInputSealsSecretsAndViewHidesThem(t *testing.T) {
	s := newBoxService(t)
	ep := store.WebhookEndpoint{Format: "standard", Severities: []string{"info"}, Headers: json.RawMessage("{}"), Options: json.RawMessage("{}")}
	url := "https://hooks.slack.com/services/T0/B0/tokenSECRET"
	err := s.applyWebhookInput(&ep, WebhookInput{Name: ptr("Ops"), URL: &url, Format: ptr("slack"), NewSecret: true,
		Headers: map[string]string{"Authorization": "Bearer abc"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{ep.URL, ep.Secret, string(ep.Headers)} {
		if strings.Contains(stored, "SECRET") || strings.Contains(stored, "Bearer") || strings.Contains(stored, "whsec_") {
			t.Fatalf("stored in clear: %s", stored)
		}
	}
	v := s.webhookView(ep)
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "token") || strings.Contains(string(b), "Bearer") || !v.SecretSet ||
		v.URL != "https://hooks.slack.com/…CRET" || len(v.HeaderNames) != 1 {
		t.Fatalf("view leaks or misses: %s", b)
	}
	// an update with an empty header value keeps the stored one
	before := ep.Headers
	if err := s.applyWebhookInput(&ep, WebhookInput{Headers: map[string]string{"Authorization": ""}}); err != nil || string(ep.Headers) != string(before) {
		t.Fatalf("header kept? %v", err)
	}
}

func TestWebhookInputValidation(t *testing.T) {
	s := newBoxService(t)
	base := func() store.WebhookEndpoint {
		return store.WebhookEndpoint{Format: "standard", Severities: []string{"info"}, Headers: json.RawMessage("{}"), Options: json.RawMessage("{}")}
	}
	for name, in := range map[string]WebhookInput{
		"private url":        {URL: ptr("https://169.254.169.254/latest")},
		"http url":           {URL: ptr("http://example.com/x")},
		"bad secret":         {Secret: ptr("whsec_sample123")},
		"unknown severity":   {Severities: []string{"urgent"}},
		"reserved header":    {Headers: map[string]string{"Content-Type": "x"}},
		"header line break":  {Headers: map[string]string{"X-A": "a\r\nX-B: b"}},
		"telegram no chat":   {Format: ptr("telegram")},
		"bad template":       {Format: ptr("custom"), CustomTemplate: ptr(`{"a": "{{.Title}}"`)},
		"template with loop": {Format: ptr("custom"), CustomTemplate: ptr(`{{range 3}}{{end}}{}`)},
		"missing reference":  {URL: ptr("env:QUACK_TEST_NO_SUCH_VAR")},
	} {
		ep := base()
		if s.applyWebhookInput(&ep, in) == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	ep := base()
	if err := s.applyWebhookInput(&ep, WebhookInput{Format: ptr("telegram"), Options: &webhook.Options{ChatID: "-100"}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUACK_TEST_HOOK_URL", "https://example.com/hook")
	ep = base()
	if err := s.applyWebhookInput(&ep, WebhookInput{URL: ptr("env:QUACK_TEST_HOOK_URL")}); err != nil || ep.URL != "env:QUACK_TEST_HOOK_URL" {
		t.Fatalf("a reference is stored as written: %q %v", ep.URL, err)
	}
	if v := s.webhookView(ep); v.URL != "env:QUACK_TEST_HOOK_URL" {
		t.Fatal(v.URL)
	}
}
