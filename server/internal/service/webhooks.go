package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/secrets"
	"github.com/taha2samy/quackquack/server/internal/store"
	"github.com/taha2samy/quackquack/server/internal/webhook"
)

// WebhookInput creates or changes an endpoint. On update, nil fields (and an
// empty url or secret) keep the stored value.
type WebhookInput struct {
	Name           *string
	URL            *string // literal, or an env:/file: reference
	Format         *string
	Secret         *string // whsec_…, or a reference; "" on create generates one
	NewSecret      bool    // generate a new secret
	Severities     []string
	Headers        map[string]string // replaces the headers; an empty value keeps that header's stored value
	CustomTemplate *string
	Options        *webhook.Options
	Enabled        *bool
}

// WebhookView is an endpoint as the API shows it: secret values never leave
// the server (references are shown, they aren't secrets).
type WebhookView struct {
	ID             uuid.UUID       `json:"id"`
	Name           string          `json:"name"`
	URL            string          `json:"url" doc:"The reference, or the URL redacted to scheme://host/…last 4 characters"`
	Format         string          `json:"format"`
	Severities     []string        `json:"severities"`
	HeaderNames    []string        `json:"header_names"`
	CustomTemplate *string         `json:"custom_template,omitempty"`
	Options        webhook.Options `json:"options"`
	Enabled        bool            `json:"enabled"`
	SecretSet      bool            `json:"secret_set"`
	SecretRef      string          `json:"secret_ref,omitempty" doc:"The secret's env:/file: reference, when it is one"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

var (
	severities      = []string{"info", "warning", "critical"}
	headerNameRe    = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,64}$")
	reservedHeaders = []string{"host", "content-length", "content-type", "transfer-encoding", "connection",
		"webhook-id", "webhook-timestamp", "webhook-signature"}
)

func (s *Service) webhookView(ep store.WebhookEndpoint) WebhookView {
	v := WebhookView{ID: ep.ID, Name: ep.Name, Format: ep.Format, Severities: ep.Severities, CustomTemplate: ep.CustomTemplate,
		Enabled: ep.Enabled, SecretSet: ep.Secret != "", CreatedAt: ep.CreatedAt, UpdatedAt: ep.UpdatedAt, HeaderNames: []string{}}
	if secrets.IsRef(ep.URL) {
		v.URL = ep.URL
	} else if u, err := s.Secrets.Open(ep.URL); err == nil {
		v.URL = webhook.RedactURL(u)
	} else {
		v.URL = "(can't be decrypted: " + err.Error() + ")"
	}
	if secrets.IsRef(ep.Secret) {
		v.SecretRef = ep.Secret
	}
	var h map[string]string
	_ = json.Unmarshal(ep.Headers, &h)
	for k := range h {
		v.HeaderNames = append(v.HeaderNames, k)
	}
	slices.Sort(v.HeaderNames)
	_ = json.Unmarshal(ep.Options, &v.Options)
	return v
}

func (s *Service) ListWebhooks(ctx context.Context) ([]WebhookView, error) {
	eps, err := store.ListWebhookEndpoints(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	out := make([]WebhookView, len(eps))
	for i, ep := range eps {
		out[i] = s.webhookView(ep)
	}
	return out, nil
}

func (s *Service) GetWebhook(ctx context.Context, id uuid.UUID) (WebhookView, error) {
	ep, err := store.GetWebhookEndpoint(ctx, s.Pool, id)
	if err != nil {
		return WebhookView{}, err
	}
	return s.webhookView(ep), nil
}

// WebhookEndpoint returns a stored endpoint (secrets in their stored form).
func (s *Service) WebhookEndpoint(ctx context.Context, id uuid.UUID) (store.WebhookEndpoint, error) {
	return store.GetWebhookEndpoint(ctx, s.Pool, id)
}

func (s *Service) CreateWebhook(ctx context.Context, a Actor, in WebhookInput) (WebhookView, error) {
	ep := store.WebhookEndpoint{ID: uuid.New(), Format: "standard", Severities: severities, Enabled: true,
		Headers: json.RawMessage("{}"), Options: json.RawMessage("{}")}
	if in.Secret == nil || *in.Secret == "" {
		in.NewSecret = true
	}
	if in.URL == nil || *in.URL == "" {
		return WebhookView{}, invalid("url", "required")
	}
	if err := s.applyWebhookInput(&ep, in); err != nil {
		return WebhookView{}, err
	}
	var out store.WebhookEndpoint
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if out, err = store.CreateWebhookEndpoint(ctx, tx, ep); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "webhook", out.ID.String(), s.webhookView(out))
	})
	return s.webhookView(out), err
}

func (s *Service) UpdateWebhook(ctx context.Context, a Actor, id uuid.UUID, in WebhookInput) (WebhookView, error) {
	var out store.WebhookEndpoint
	err := s.tx(ctx, func(tx pgx.Tx) error {
		ep, err := store.GetWebhookEndpoint(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.applyWebhookInput(&ep, in); err != nil {
			return err
		}
		if out, err = store.UpdateWebhookEndpoint(ctx, tx, ep); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "webhook", id.String(), s.webhookView(out))
	})
	return s.webhookView(out), err
}

func (s *Service) DeleteWebhook(ctx context.Context, a Actor, id uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		ep, err := store.GetWebhookEndpoint(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.DeleteWebhookEndpoint(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "webhook", id.String(), map[string]string{"name": ep.Name})
	})
}

// ListWebhookDeliveries lists an endpoint's newest deliveries (at most 500).
func (s *Service) ListWebhookDeliveries(ctx context.Context, id uuid.UUID, limit int) ([]store.WebhookDelivery, error) {
	if _, err := store.GetWebhookEndpoint(ctx, s.Pool, id); err != nil {
		return nil, err
	}
	return store.ListWebhookDeliveries(ctx, s.Pool, id, min(max(limit, 1), 500))
}

// applyWebhookInput validates in and writes it to ep (secrets sealed).
func (s *Service) applyWebhookInput(ep *store.WebhookEndpoint, in WebhookInput) error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 100 {
			return invalid("name", "1-100 characters")
		}
		ep.Name = n
	}
	if in.Format != nil {
		if !slices.Contains(webhook.Formats, *in.Format) {
			return invalid("format", "one of "+strings.Join(webhook.Formats, ", "))
		}
		ep.Format = *in.Format
	}
	if in.URL != nil && *in.URL != "" {
		u := strings.TrimSpace(*in.URL)
		resolved, err := s.openValue(u)
		if err != nil {
			return invalid("url", err.Error())
		}
		if err := webhook.ValidateURL(resolved, s.WebhookAllowPrivate); err != nil {
			return invalid("url", err.Error())
		}
		if ep.URL, err = s.Secrets.Seal(u); err != nil {
			return err
		}
	}
	switch {
	case in.NewSecret:
		sealed, err := s.Secrets.Seal(webhook.NewSecret())
		if err != nil {
			return err
		}
		ep.Secret = sealed
	case in.Secret != nil && *in.Secret != "":
		sec := strings.TrimSpace(*in.Secret)
		resolved, err := s.openValue(sec)
		if err == nil {
			err = webhook.ValidateSecret(resolved)
		}
		if err != nil {
			return invalid("secret", err.Error())
		}
		if ep.Secret, err = s.Secrets.Seal(sec); err != nil {
			return err
		}
	}
	if in.Severities != nil {
		var sev []string
		for _, x := range in.Severities {
			if !slices.Contains(severities, x) {
				return invalid("severities", "each one of info, warning, critical")
			}
			if !slices.Contains(sev, x) {
				sev = append(sev, x)
			}
		}
		if len(sev) == 0 {
			return invalid("severities", "at least one")
		}
		ep.Severities = sev
	}
	if in.Headers != nil {
		if err := s.applyHeaders(ep, in.Headers); err != nil {
			return err
		}
	}
	if in.CustomTemplate != nil {
		t := *in.CustomTemplate
		ep.CustomTemplate = &t
		if strings.TrimSpace(t) == "" {
			ep.CustomTemplate = nil
		}
	}
	if in.Options != nil {
		o := *in.Options
		o.ChatID = strings.TrimSpace(o.ChatID)
		if len(o.ChatID) > 64 {
			return invalid("options.chat_id", "at most 64 characters")
		}
		b, _ := json.Marshal(o)
		ep.Options = b
	}
	if in.Enabled != nil {
		ep.Enabled = *in.Enabled
	}
	// checks across fields
	switch ep.Format {
	case "custom":
		if ep.CustomTemplate == nil {
			return invalid("custom_template", "the custom format needs a template")
		}
		if _, err := webhook.RenderTemplate(*ep.CustomTemplate, webhook.SampleEvent(webhook.SeverityWarning)); err != nil {
			return invalid("custom_template", err.Error())
		}
	case "telegram":
		var o webhook.Options
		_ = json.Unmarshal(ep.Options, &o)
		if o.ChatID == "" {
			return invalid("options.chat_id", "a telegram endpoint needs the chat id to post to")
		}
	}
	return nil
}

func (s *Service) applyHeaders(ep *store.WebhookEndpoint, in map[string]string) error {
	if len(in) > 20 {
		return invalid("headers", "at most 20 headers")
	}
	var old map[string]string
	_ = json.Unmarshal(ep.Headers, &old)
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch {
		case !headerNameRe.MatchString(k):
			return invalid("headers", fmt.Sprintf("%q is not a valid header name", k))
		case slices.Contains(reservedHeaders, strings.ToLower(k)):
			return invalid("headers", fmt.Sprintf("%s is set by the server", k))
		case strings.ContainsAny(v, "\r\n"):
			return invalid("headers", fmt.Sprintf("the value of %s has a line break", k))
		case v == "":
			prev, ok := old[k]
			if !ok {
				return invalid("headers", fmt.Sprintf("%s needs a value", k))
			}
			out[k] = prev
			continue
		}
		if secrets.IsRef(v) {
			if _, err := s.openValue(v); err != nil {
				return invalid("headers", err.Error())
			}
		}
		sealed, err := s.Secrets.Seal(v)
		if err != nil {
			return err
		}
		out[k] = sealed
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	ep.Headers = b
	return nil
}

// openValue resolves a reference here (the dispatcher runs in this process,
// so it must resolve here too); literal values are returned as they are.
func (s *Service) openValue(v string) (string, error) {
	if !secrets.IsRef(v) {
		return v, nil
	}
	r, err := s.Secrets.Open(v)
	if err != nil {
		return "", errors.New("the reference can't be read on this server: " + err.Error())
	}
	return r, nil
}
