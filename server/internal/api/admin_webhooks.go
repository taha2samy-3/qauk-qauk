package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/store"
	"github.com/taha2samy/quackquack/server/internal/webhook"
)

type WebhookListOut struct {
	Body []store.WebhookEndpoint
}

type WebhookOut struct {
	Body store.WebhookEndpoint
}

type WebhookGetIn struct {
	ID uuid.UUID `path:"id"`
}

type WebhookCreateIn struct {
	Body struct {
		Name           string          `json:"name" doc:"Endpoint display name" minLength:"1" maxLength:"100"`
		URL            string          `json:"url" doc:"Destination HTTP/HTTPS URL" format:"uri"`
		Format         string          `json:"format" doc:"Payload format" enum:"standard,slack,discord,teams,telegram,custom"`
		Secret         *string         `json:"secret,omitempty" doc:"Signing secret (auto-generated if empty)"`
		Severities     []string        `json:"severities,omitempty" doc:"Severities to deliver: info, warning, critical"`
		Headers        json.RawMessage `json:"headers,omitempty" doc:"Custom HTTP headers (JSON object)"`
		CustomTemplate *string         `json:"custom_template,omitempty" doc:"Go template for custom format"`
		Enabled        *bool           `json:"enabled,omitempty"`
	}
}

type WebhookUpdateIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name           string          `json:"name" minLength:"1" maxLength:"100"`
		URL            string          `json:"url" format:"uri"`
		Format         string          `json:"format" enum:"standard,slack,discord,teams,telegram,custom"`
		Secret         string          `json:"secret"`
		Severities     []string        `json:"severities"`
		Headers        json.RawMessage `json:"headers"`
		CustomTemplate *string         `json:"custom_template,omitempty"`
		Enabled        bool            `json:"enabled"`
	}
}

type WebhookDeleteIn struct {
	ID uuid.UUID `path:"id"`
}

type WebhookTestIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Severity webhook.Severity `json:"severity" enum:"info,warning,critical"`
	}
}

type WebhookTestOut struct {
	Body struct {
		StatusCode int    `json:"status_code"`
		Response   string `json:"response"`
		LatencyMs  int    `json:"latency_ms"`
		Success    bool   `json:"success"`
		Error      string `json:"error,omitempty"`
	}
}

type WebhookDeliveriesIn struct {
	ID    uuid.UUID `path:"id"`
	Limit int       `query:"limit" default:"50"`
}

type WebhookDeliveriesOut struct {
	Body []store.WebhookDelivery
}

func (a *API) registerAdminWebhooks(api huma.API) {
	dispatcher := webhook.NewDispatcher()

	huma.Register(api, huma.Operation{
		OperationID: "list-webhooks",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/webhooks",
		Summary:     "List webhook endpoints",
		Tags:        []string{"Admin Webhooks"},
	}, func(ctx context.Context, input *struct{}) (*WebhookListOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		list, err := store.ListWebhookEndpoints(ctx, a.pool)
		if err != nil {
			return nil, a.fail(err)
		}
		return &WebhookListOut{Body: list}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-webhook",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/webhooks/{id}",
		Summary:     "Get a webhook endpoint",
		Tags:        []string{"Admin Webhooks"},
	}, func(ctx context.Context, input *WebhookGetIn) (*WebhookOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		ep, err := store.GetWebhookEndpoint(ctx, a.pool, input.ID)
		if err != nil {
			return nil, a.fail(err)
		}
		return &WebhookOut{Body: ep}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-webhook",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/webhooks",
		Summary:     "Create a webhook endpoint",
		Tags:        []string{"Admin Webhooks"},
	}, func(ctx context.Context, input *WebhookCreateIn) (*WebhookOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}

		secret := ""
		if input.Body.Secret != nil && *input.Body.Secret != "" {
			secret = *input.Body.Secret
		} else {
			// Generate 32-byte hex secret with whsec_ prefix
			bytes := make([]byte, 24)
			_, _ = rand.Read(bytes)
			secret = "whsec_" + hex.EncodeToString(bytes)
		}

		enabled := true
		if input.Body.Enabled != nil {
			enabled = *input.Body.Enabled
		}

		format := input.Body.Format
		if format == "" {
			format = "standard"
		}

		severities := input.Body.Severities
		if len(severities) == 0 {
			severities = []string{"info", "warning", "critical"}
		}

		ep, err := store.CreateWebhookEndpoint(ctx, a.pool, store.WebhookEndpoint{
			Name:           input.Body.Name,
			URL:            input.Body.URL,
			Format:         format,
			Secret:         secret,
			Severities:     severities,
			Headers:        input.Body.Headers,
			CustomTemplate: input.Body.CustomTemplate,
			Enabled:        enabled,
		})
		if err != nil {
			return nil, a.fail(err)
		}
		return &WebhookOut{Body: ep}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-webhook",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/webhooks/{id}",
		Summary:     "Update a webhook endpoint",
		Tags:        []string{"Admin Webhooks"},
	}, func(ctx context.Context, input *WebhookUpdateIn) (*WebhookOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		ep, err := store.UpdateWebhookEndpoint(ctx, a.pool, store.WebhookEndpoint{
			ID:             input.ID,
			Name:           input.Body.Name,
			URL:            input.Body.URL,
			Format:         input.Body.Format,
			Secret:         input.Body.Secret,
			Severities:     input.Body.Severities,
			Headers:        input.Body.Headers,
			CustomTemplate: input.Body.CustomTemplate,
			Enabled:        input.Body.Enabled,
		})
		if err != nil {
			return nil, a.fail(err)
		}
		return &WebhookOut{Body: ep}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-webhook",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/webhooks/{id}",
		Summary:     "Delete a webhook endpoint",
		Tags:        []string{"Admin Webhooks"},
	}, func(ctx context.Context, input *WebhookDeleteIn) (*struct{}, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		if err := store.DeleteWebhookEndpoint(ctx, a.pool, input.ID); err != nil {
			return nil, a.fail(err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "test-webhook",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/webhooks/{id}/test",
		Summary:     "Send a test notification to endpoint",
		Tags:        []string{"Admin Webhooks"},
	}, func(ctx context.Context, input *WebhookTestIn) (*WebhookTestOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		ep, err := store.GetWebhookEndpoint(ctx, a.pool, input.ID)
		if err != nil {
			return nil, a.fail(err)
		}

		code, resp, latency, err := dispatcher.SendTest(ctx, ep, input.Body.Severity)
		out := &WebhookTestOut{}
		out.Body.StatusCode = code
		out.Body.Response = resp
		out.Body.LatencyMs = latency
		out.Body.Success = err == nil
		if err != nil {
			out.Body.Error = err.Error()
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-webhook-deliveries",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/webhooks/{id}/deliveries",
		Summary:     "List delivery history for endpoint",
		Tags:        []string{"Admin Webhooks"},
	}, func(ctx context.Context, input *WebhookDeliveriesIn) (*WebhookDeliveriesOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		deliveries, err := store.ListWebhookDeliveries(ctx, a.pool, input.ID, input.Limit)
		if err != nil {
			return nil, a.fail(err)
		}
		return &WebhookDeliveriesOut{Body: deliveries}, nil
	})
}
