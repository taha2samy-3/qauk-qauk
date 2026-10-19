package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Command is a tracked command (integration API) and what became of it.
type Command struct {
	ID        uuid.UUID       `db:"id" json:"command_id"`
	ElementID uuid.UUID       `db:"element_id" json:"element_id"`
	UserID    *int64          `db:"user_id" json:"-"`
	Actor     string          `db:"actor" json:"actor"`
	Message   json.RawMessage `db:"message" json:"message"`
	Status    string          `db:"status" json:"status" enum:"accepted,delivered,failed,superseded"`
	Reason    string          `db:"reason" json:"reason,omitempty"`
	CreatedAt time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt time.Time       `db:"updated_at" json:"updated_at"`
}

func InsertCommand(ctx context.Context, db DBTX, c Command) error {
	_, err := db.Exec(ctx, `INSERT INTO commands (id, element_id, user_id, actor, message) VALUES ($1, $2, $3, $4, $5)`,
		c.ID, c.ElementID, c.UserID, c.Actor, c.Message)
	return mapErr(err)
}

func GetCommand(ctx context.Context, db DBTX, id uuid.UUID) (Command, error) {
	return one[Command](db.Query(ctx, `SELECT id, element_id, user_id, actor, message, status, reason, created_at, updated_at
		FROM commands WHERE id = $1`, id))
}

// SetCommandStatus moves a command on from accepted (the first report wins)
// and returns it; ok is false when it isn't tracked or has moved on already.
func SetCommandStatus(ctx context.Context, db DBTX, id uuid.UUID, status, reason string) (c Command, ok bool, err error) {
	c, err = one[Command](db.Query(ctx, `UPDATE commands SET status = $2, reason = $3, updated_at = now()
		WHERE id = $1 AND status = 'accepted'
		RETURNING id, element_id, user_id, actor, message, status, reason, created_at, updated_at`, id, status, reason))
	if errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

// PurgeCommands deletes commands older than d.
func PurgeCommands(ctx context.Context, db DBTX, d time.Duration) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM commands WHERE created_at < now() - make_interval(secs => $1)`, d.Seconds())
	return tag.RowsAffected(), mapErr(err)
}
