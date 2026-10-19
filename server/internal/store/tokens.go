package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// API token scopes: what a token may do on top of its account's grants.
const (
	ScopeRead    = "read"    // elements, values and history it is granted
	ScopeControl = "control" // commands to elements granted RC
	ScopeAlerts  = "alerts"  // alerts of elements it may read
)

// Scopes lists every scope.
var Scopes = []string{ScopeRead, ScopeControl, ScopeAlerts}

// APIToken is a machine user's token, without its secret.
type APIToken struct {
	ID         uuid.UUID  `db:"id" json:"id"`
	UserID     int64      `db:"user_id" json:"user_id"`
	Name       string     `db:"name" json:"name"`
	Prefix     string     `db:"prefix" json:"prefix" doc:"The token's first characters, to recognize it"`
	Scopes     []string   `db:"scopes" json:"scopes"`
	ExpiresAt  *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at"`
	CreatedBy  string     `db:"created_by" json:"created_by"`
	LastUsedAt *time.Time `db:"last_used_at" json:"last_used_at,omitempty"`
}

const apiTokenCols = `id, user_id, name, prefix, scopes, expires_at, created_at, created_by, last_used_at`

func InsertAPIToken(ctx context.Context, db DBTX, t APIToken, hash []byte) (APIToken, error) {
	return one[APIToken](db.Query(ctx, `INSERT INTO api_tokens (user_id, name, prefix, token_hash, scopes, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+apiTokenCols,
		t.UserID, t.Name, t.Prefix, hash, t.Scopes, t.ExpiresAt, t.CreatedBy))
}

func ListAPITokens(ctx context.Context, db DBTX, userID int64) ([]APIToken, error) {
	return many[APIToken](db.Query(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE user_id = $1 ORDER BY created_at`, userID))
}

// DeleteAPIToken revokes one of a user's tokens.
func DeleteAPIToken(ctx context.Context, db DBTX, userID int64, id uuid.UUID) error {
	return execOne(db.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID))
}

// TokenPrincipal is who a request with a valid API token acts as.
type TokenPrincipal struct {
	User
	TokenID   uuid.UUID `db:"token_id"`
	TokenName string    `db:"token_name"`
	Scopes    []string  `db:"scopes"`
}

// Has reports whether the token carries a scope.
func (p TokenPrincipal) Has(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// TokenUser resolves a token hash to its active machine user, if the token
// exists and hasn't expired. last_used_at is written at most once a minute.
func TokenUser(ctx context.Context, db DBTX, hash []byte) (TokenPrincipal, error) {
	return one[TokenPrincipal](db.Query(ctx, `
		WITH touched AS (
			UPDATE api_tokens SET last_used_at = now()
			WHERE token_hash = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')
			RETURNING id)
		SELECT u.id, u.username, u.email, u.password_hash, u.is_active, u.is_admin, u.kind, u.created_at, u.last_login_at,
			k.id AS token_id, k.name AS token_name, k.scopes
		FROM api_tokens k JOIN users u ON u.id = k.user_id
		WHERE k.token_hash = $1 AND u.is_active AND u.kind = 'service' AND NOT u.is_admin
		  AND (k.expires_at IS NULL OR k.expires_at > now())`, hash))
}

// ListMachineUsers lists the machine users.
func ListMachineUsers(ctx context.Context, db DBTX) ([]User, error) {
	return many[User](db.Query(ctx, `SELECT `+userCols+` FROM users WHERE kind = 'service' ORDER BY id`))
}
