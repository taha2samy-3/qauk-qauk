package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type WebhookEndpoint struct {
	ID             uuid.UUID       `json:"id"`
	Name           string          `json:"name"`
	URL            string          `json:"url"`
	Format         string          `json:"format"`
	Secret         string          `json:"secret"`
	Severities     []string        `json:"severities"`
	Headers        json.RawMessage `json:"headers"`
	CustomTemplate *string         `json:"custom_template,omitempty"`
	Enabled        bool            `json:"enabled"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type WebhookDelivery struct {
	ID             uuid.UUID       `json:"id"`
	EndpointID     uuid.UUID       `json:"endpoint_id"`
	EventID        uuid.UUID       `json:"event_id"`
	EventType      string          `json:"event_type"`
	Severity       string          `json:"severity"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"max_attempts"`
	LastStatusCode *int            `json:"last_status_code,omitempty"`
	LastError      *string         `json:"last_error,omitempty"`
	LatencyMs      *int            `json:"latency_ms,omitempty"`
	NextRetryAt    time.Time       `json:"next_retry_at"`
	CreatedAt      time.Time       `json:"created_at"`
	DeliveredAt    *time.Time      `json:"delivered_at,omitempty"`
}

const webhookEndpointCols = `id, name, url, format, secret, severities, headers, custom_template, enabled, created_at, updated_at`
const webhookDeliveryCols = `id, endpoint_id, event_id, event_type, severity, payload, status, attempts, max_attempts, last_status_code, last_error, latency_ms, next_retry_at, created_at, delivered_at`

func ListWebhookEndpoints(ctx context.Context, db DBTX) ([]WebhookEndpoint, error) {
	return many[WebhookEndpoint](db.Query(ctx, `SELECT `+webhookEndpointCols+` FROM webhook_endpoints ORDER BY created_at DESC`))
}

func GetWebhookEndpoint(ctx context.Context, db DBTX, id uuid.UUID) (WebhookEndpoint, error) {
	return one[WebhookEndpoint](db.Query(ctx, `SELECT `+webhookEndpointCols+` FROM webhook_endpoints WHERE id = $1`, id))
}

func CreateWebhookEndpoint(ctx context.Context, db DBTX, ep WebhookEndpoint) (WebhookEndpoint, error) {
	if ep.ID == uuid.Nil {
		ep.ID = uuid.New()
	}
	if len(ep.Headers) == 0 {
		ep.Headers = json.RawMessage("{}")
	}
	if len(ep.Severities) == 0 {
		ep.Severities = []string{"info", "warning", "critical"}
	}
	if ep.Format == "" {
		ep.Format = "standard"
	}
	return one[WebhookEndpoint](db.Query(ctx, `
		INSERT INTO webhook_endpoints (id, name, url, format, secret, severities, headers, custom_template, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
		RETURNING `+webhookEndpointCols,
		ep.ID, ep.Name, ep.URL, ep.Format, ep.Secret, ep.Severities, ep.Headers, ep.CustomTemplate, ep.Enabled,
	))
}

func UpdateWebhookEndpoint(ctx context.Context, db DBTX, ep WebhookEndpoint) (WebhookEndpoint, error) {
	if len(ep.Headers) == 0 {
		ep.Headers = json.RawMessage("{}")
	}
	if len(ep.Severities) == 0 {
		ep.Severities = []string{"info", "warning", "critical"}
	}
	return one[WebhookEndpoint](db.Query(ctx, `
		UPDATE webhook_endpoints SET
			name = $2,
			url = $3,
			format = $4,
			secret = $5,
			severities = $6,
			headers = $7,
			custom_template = $8,
			enabled = $9,
			updated_at = now()
		WHERE id = $1
		RETURNING `+webhookEndpointCols,
		ep.ID, ep.Name, ep.URL, ep.Format, ep.Secret, ep.Severities, ep.Headers, ep.CustomTemplate, ep.Enabled,
	))
}

func DeleteWebhookEndpoint(ctx context.Context, db DBTX, id uuid.UUID) error {
	tag, err := db.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func EnqueueWebhookDelivery(ctx context.Context, db DBTX, d WebhookDelivery) (WebhookDelivery, error) {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	if d.MaxAttempts <= 0 {
		d.MaxAttempts = 5
	}
	if d.Status == "" {
		d.Status = "pending"
	}
	return one[WebhookDelivery](db.Query(ctx, `
		INSERT INTO webhook_deliveries (id, endpoint_id, event_id, event_type, severity, payload, status, attempts, max_attempts, next_retry_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, now(), now())
		RETURNING `+webhookDeliveryCols,
		d.ID, d.EndpointID, d.EventID, d.EventType, d.Severity, d.Payload, d.Status, d.MaxAttempts,
	))
}

func FetchPendingDeliveries(ctx context.Context, db DBTX, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	return many[WebhookDelivery](db.Query(ctx, `
		UPDATE webhook_deliveries
		SET status = 'delivering'
		WHERE id IN (
			SELECT id FROM webhook_deliveries
			WHERE status IN ('pending', 'retrying') AND next_retry_at <= now()
			ORDER BY next_retry_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+webhookDeliveryCols, limit))
}

func RecordDeliverySuccess(ctx context.Context, db DBTX, id uuid.UUID, statusCode int, latencyMs int) error {
	_, err := db.Exec(ctx, `
		UPDATE webhook_deliveries SET
			status = 'delivered',
			attempts = attempts + 1,
			last_status_code = $2,
			latency_ms = $3,
			last_error = NULL,
			delivered_at = now()
		WHERE id = $1`, id, statusCode, latencyMs)
	return err
}

func RecordDeliveryFailure(ctx context.Context, db DBTX, id uuid.UUID, statusCode *int, errMsg string, latencyMs int, retryDelay time.Duration) error {
	_, err := db.Exec(ctx, `
		UPDATE webhook_deliveries SET
			status = CASE WHEN attempts + 1 >= max_attempts THEN 'failed' ELSE 'retrying' END,
			attempts = attempts + 1,
			last_status_code = $2,
			last_error = $3,
			latency_ms = $4,
			next_retry_at = now() + ($5 * interval '1 microsecond')
		WHERE id = $1`, id, statusCode, errMsg, latencyMs, retryDelay.Microseconds())
	return err
}

func ListWebhookDeliveries(ctx context.Context, db DBTX, endpointID uuid.UUID, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	return many[WebhookDelivery](db.Query(ctx, `
		SELECT `+webhookDeliveryCols+` FROM webhook_deliveries
		WHERE endpoint_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, endpointID, limit))
}
