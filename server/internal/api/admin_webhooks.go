package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
	"github.com/taha2samy/quackquack/server/internal/webhook"
)

type WebhookListOut struct {
	Body []service.WebhookView
}

type WebhookOut struct {
	Body service.WebhookView
}

type WebhookGetIn struct {
	ID uuid.UUID `path:"id"`
}

// WebhookBody is what an admin sends. Secrets (url, secret, header values)
// are write-only: responses never contain them.
type WebhookBody struct {
	Name           *string           `json:"name,omitempty" doc:"Endpoint display name" maxLength:"100"`
	URL            *string           `json:"url,omitempty" doc:"Destination https URL, or an env:/file: reference to one. On update, empty keeps the stored URL."`
	Format         *string           `json:"format,omitempty" doc:"Payload format" enum:"standard,slack,discord,teams,telegram,custom"`
	Secret         *string           `json:"secret,omitempty" doc:"Signing secret whsec_<base64>, or a reference. Empty on create generates one; on update empty keeps it."`
	NewSecret      bool              `json:"new_secret,omitempty" doc:"Generate a new signing secret"`
	Severities     []string          `json:"severities,omitempty" doc:"Severities to deliver: info, warning, critical"`
	Headers        map[string]string `json:"headers,omitempty" doc:"Extra HTTP headers; values may be references. On update the map replaces the headers, and an empty value keeps that header's stored value."`
	CustomTemplate *string           `json:"custom_template,omitempty" doc:"Go template for the custom format; use {{json .Field}} for values"`
	Options        *webhook.Options  `json:"options,omitempty"`
	Enabled        *bool             `json:"enabled,omitempty"`
}

func (b WebhookBody) input() service.WebhookInput {
	return service.WebhookInput{Name: b.Name, URL: b.URL, Format: b.Format, Secret: b.Secret, NewSecret: b.NewSecret,
		Severities: b.Severities, Headers: b.Headers, CustomTemplate: b.CustomTemplate, Options: b.Options, Enabled: b.Enabled}
}

type WebhookCreateIn struct {
	Body WebhookBody
}

type WebhookUpdateIn struct {
	ID   uuid.UUID `path:"id"`
	Body WebhookBody
}

type WebhookCreatedOut struct {
	Body struct {
		service.WebhookView
		Secret string `json:"secret,omitempty" doc:"The generated signing secret, shown once"`
	}
}

type WebhookDeleteIn struct {
	ID uuid.UUID `path:"id"`
}

type WebhookTestIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Severity webhook.Severity `json:"severity,omitempty" enum:"info,warning,critical"`
	}
}

type WebhookTestOut struct {
	Body struct {
		StatusCode int    `json:"status_code"`
		Response   string `json:"response" doc:"The start of the response body (2 KiB)"`
		LatencyMs  int    `json:"latency_ms"`
		Success    bool   `json:"success"`
		Error      string `json:"error,omitempty"`
	}
}

type WebhookDeliveriesIn struct {
	ID    uuid.UUID `path:"id"`
	Limit int       `query:"limit" default:"50" minimum:"1" maximum:"500"`
}

type WebhookDeliveriesOut struct {
	Body []store.WebhookDelivery
}

func (a *API) registerAdminWebhooks(api huma.API) {
	tags := []string{"Admin Webhooks"}
	dispatcher := webhook.NewDispatcher(a.svc.Secrets, a.svc.WebhookAllowPrivate)

	huma.Register(api, huma.Operation{OperationID: "list-webhooks", Method: http.MethodGet, Path: "/api/v1/admin/webhooks",
		Summary: "List webhook endpoints", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*WebhookListOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			list, err := a.svc.ListWebhooks(ctx)
			if err != nil {
				return nil, a.fail(err)
			}
			return &WebhookListOut{Body: list}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-webhook", Method: http.MethodGet, Path: "/api/v1/admin/webhooks/{id}",
		Summary: "Get a webhook endpoint", Tags: tags},
		func(ctx context.Context, in *WebhookGetIn) (*WebhookOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			v, err := a.svc.GetWebhook(ctx, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &WebhookOut{Body: v}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-webhook", Method: http.MethodPost, Path: "/api/v1/admin/webhooks",
		Summary: "Create a webhook endpoint", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *WebhookCreateIn) (*WebhookCreatedOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			generated := in.Body.Secret == nil || *in.Body.Secret == ""
			v, err := a.svc.CreateWebhook(ctx, service.UserActor(admin), in.Body.input())
			if err != nil {
				return nil, a.fail(err)
			}
			out := &WebhookCreatedOut{}
			out.Body.WebhookView = v
			if generated { // shown once so the receiver can be set up
				if ep, err := a.svc.WebhookEndpoint(ctx, v.ID); err == nil {
					out.Body.Secret, _ = a.svc.Secrets.Open(ep.Secret)
				}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-webhook", Method: http.MethodPut, Path: "/api/v1/admin/webhooks/{id}",
		Summary: "Update a webhook endpoint", Tags: tags},
		func(ctx context.Context, in *WebhookUpdateIn) (*WebhookCreatedOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			v, err := a.svc.UpdateWebhook(ctx, service.UserActor(admin), in.ID, in.Body.input())
			if err != nil {
				return nil, a.fail(err)
			}
			out := &WebhookCreatedOut{}
			out.Body.WebhookView = v
			if in.Body.NewSecret {
				if ep, err := a.svc.WebhookEndpoint(ctx, v.ID); err == nil {
					out.Body.Secret, _ = a.svc.Secrets.Open(ep.Secret)
				}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-webhook", Method: http.MethodDelete, Path: "/api/v1/admin/webhooks/{id}",
		Summary: "Delete a webhook endpoint", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *WebhookDeleteIn) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteWebhook(ctx, service.UserActor(admin), in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "test-webhook", Method: http.MethodPost, Path: "/api/v1/admin/webhooks/{id}/test",
		Summary: "Send a test notification to an endpoint", Tags: tags},
		func(ctx context.Context, in *WebhookTestIn) (*WebhookTestOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			ep, err := a.svc.WebhookEndpoint(ctx, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			res := dispatcher.SendTest(ctx, ep, in.Body.Severity)
			out := &WebhookTestOut{}
			out.Body.StatusCode, out.Body.Response, out.Body.LatencyMs = res.StatusCode, res.Body, res.LatencyMs
			out.Body.Success = res.Err == nil
			if res.Err != nil {
				out.Body.Error = res.Err.Error()
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-webhook-deliveries", Method: http.MethodGet, Path: "/api/v1/admin/webhooks/{id}/deliveries",
		Summary: "List an endpoint's newest deliveries", Tags: tags},
		func(ctx context.Context, in *WebhookDeliveriesIn) (*WebhookDeliveriesOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			list, err := a.svc.ListWebhookDeliveries(ctx, in.ID, in.Limit)
			if err != nil {
				return nil, a.fail(err)
			}
			return &WebhookDeliveriesOut{Body: list}, nil
		})
}
