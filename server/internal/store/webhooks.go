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
	Options        json.RawMessage `json:"options"`
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
	LockedUntil    *time.Time      `json:"locked_until,omitempty"`
}

const webhookEndpointCols = `id, name, url, format, secret, severities, headers, custom_template, options, enabled, created_at, updated_at`
const webhookDeliveryCols = `id, endpoint_id, event_id, event_type, severity, payload, status, attempts, max_attempts, last_status_code, last_error, latency_ms, next_retry_at, created_at, delivered_at, locked_until`

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
	if len(ep.Options) == 0 {
		ep.Options = json.RawMessage("{}")
	}
	return one[WebhookEndpoint](db.Query(ctx, `
		INSERT INTO webhook_endpoints (id, name, url, format, secret, severities, headers, custom_template, options, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())
		RETURNING `+webhookEndpointCols,
		ep.ID, ep.Name, ep.URL, ep.Format, ep.Secret, ep.Severities, ep.Headers, ep.CustomTemplate, ep.Options, ep.Enabled,
	))
}

func UpdateWebhookEndpoint(ctx context.Context, db DBTX, ep WebhookEndpoint) (WebhookEndpoint, error) {
	if len(ep.Headers) == 0 {
		ep.Headers = json.RawMessage("{}")
	}
	if len(ep.Severities) == 0 {
		ep.Severities = []string{"info", "warning", "critical"}
	}
	if len(ep.Options) == 0 {
		ep.Options = json.RawMessage("{}")
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
			options = $9,
			enabled = $10,
			updated_at = now()
		WHERE id = $1
		RETURNING `+webhookEndpointCols,
		ep.ID, ep.Name, ep.URL, ep.Format, ep.Secret, ep.Severities, ep.Headers, ep.CustomTemplate, ep.Options, ep.Enabled,
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

// EnqueueWebhookDelivery adds a delivery and reports whether it was new: the
// same event is enqueued once per endpoint.
func EnqueueWebhookDelivery(ctx context.Context, db DBTX, d WebhookDelivery) (bool, error) {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	if d.MaxAttempts <= 0 {
		d.MaxAttempts = DefaultDeliveryAttempts
	}
	tag, err := db.Exec(ctx, `
		INSERT INTO webhook_deliveries (id, endpoint_id, event_id, event_type, severity, payload, status, attempts, max_attempts, next_retry_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', 0, $7, now(), clock_timestamp())
		ON CONFLICT (endpoint_id, event_id) DO NOTHING`,
		d.ID, d.EndpointID, d.EventID, d.EventType, d.Severity, d.Payload, d.MaxAttempts)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

// DefaultDeliveryAttempts is how many times a delivery is tried.
const DefaultDeliveryAttempts = 8

// ClaimWebhookDeliveries leases up to limit deliveries that are due, taking
// for each enabled endpoint only its oldest open delivery (so an endpoint
// gets its notifications in order, and one that is down only delays itself).
// A lease that expired (a worker that crashed or stopped) is taken again.
func ClaimWebhookDeliveries(ctx context.Context, db DBTX, limit int, lease time.Duration) ([]WebhookDelivery, error) {
	return many[WebhookDelivery](db.Query(ctx, `
		UPDATE webhook_deliveries d
		SET status = 'delivering', locked_until = now() + ($2 * interval '1 millisecond')
		WHERE d.id IN (
			SELECT x.id FROM webhook_deliveries x
			JOIN webhook_endpoints e ON e.id = x.endpoint_id AND e.enabled
			WHERE ((x.status IN ('pending', 'retrying') AND x.next_retry_at <= now())
			       OR (x.status = 'delivering' AND x.locked_until < now()))
			  AND NOT EXISTS (
				SELECT 1 FROM webhook_deliveries y
				WHERE y.endpoint_id = x.endpoint_id
				  AND y.status IN ('pending', 'retrying', 'delivering')
				  AND (y.created_at, y.id) < (x.created_at, x.id))
			ORDER BY x.next_retry_at
			LIMIT $1
			FOR UPDATE OF x SKIP LOCKED)
		RETURNING `+webhookDeliveryCols, limit, lease.Milliseconds()))
}

// RecordDeliverySuccess marks a delivery done.
func RecordDeliverySuccess(ctx context.Context, db DBTX, id uuid.UUID, statusCode int, latencyMs int) error {
	_, err := db.Exec(ctx, `
		UPDATE webhook_deliveries SET
			status = 'delivered', attempts = attempts + 1, last_status_code = $2, latency_ms = $3,
			last_error = NULL, delivered_at = now(), locked_until = NULL
		WHERE id = $1`, id, statusCode, latencyMs)
	return mapErr(err)
}

// RecordDeliveryFailure records a failed attempt: terminal failures (and the
// last attempt) end the delivery; others are retried after retryDelay.
func RecordDeliveryFailure(ctx context.Context, db DBTX, id uuid.UUID, statusCode *int, errMsg string, latencyMs int, retryDelay time.Duration, terminal bool) error {
	_, err := db.Exec(ctx, `
		UPDATE webhook_deliveries SET
			status = CASE WHEN $6 OR attempts + 1 >= max_attempts THEN 'failed' ELSE 'retrying' END,
			attempts = attempts + 1,
			last_status_code = $2,
			last_error = $3,
			latency_ms = $4,
			next_retry_at = now() + ($5 * interval '1 microsecond'),
			locked_until = NULL
		WHERE id = $1`, id, statusCode, errMsg, latencyMs, retryDelay.Microseconds(), terminal)
	return mapErr(err)
}

// ReleaseWebhookDelivery puts a claimed delivery back without an attempt
// (the worker is stopping).
func ReleaseWebhookDelivery(ctx context.Context, db DBTX, id uuid.UUID) error {
	_, err := db.Exec(ctx, `UPDATE webhook_deliveries SET status = CASE WHEN attempts = 0 THEN 'pending' ELSE 'retrying' END,
		locked_until = NULL WHERE id = $1 AND status = 'delivering'`, id)
	return mapErr(err)
}

// CountOpenWebhookDeliveries counts deliveries not yet delivered or failed.
func CountOpenWebhookDeliveries(ctx context.Context, db DBTX) (int64, error) {
	var n int64
	err := db.QueryRow(ctx, `SELECT count(*) FROM webhook_deliveries WHERE status IN ('pending', 'retrying', 'delivering')`).Scan(&n)
	return n, mapErr(err)
}

// PurgeWebhookDeliveries deletes delivered and failed deliveries older than
// the retention.
func PurgeWebhookDeliveries(ctx context.Context, db DBTX, retention time.Duration) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM webhook_deliveries WHERE status IN ('delivered', 'failed')
		AND created_at < now() - ($1 * interval '1 second')`, int64(retention.Seconds()))
	if err != nil {
		return 0, mapErr(err)
	}
	return tag.RowsAffected(), nil
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
