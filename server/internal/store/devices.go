package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/events"
)

// --- Keys ---

const keyCols = `id, name, pem, algorithm, key_size, is_active, created_at`

func ListKeys(ctx context.Context, db DBTX) ([]Key, error) {
	return many[Key](db.Query(ctx, `SELECT `+keyCols+` FROM jwt_public_keys ORDER BY created_at DESC`))
}

func GetKey(ctx context.Context, db DBTX, id uuid.UUID) (Key, error) {
	return one[Key](db.Query(ctx, `SELECT `+keyCols+` FROM jwt_public_keys WHERE id = $1`, id))
}

func InsertKey(ctx context.Context, db DBTX, k Key) (Key, error) {
	return one[Key](db.Query(ctx, `INSERT INTO jwt_public_keys (id, name, pem, algorithm, key_size, is_active)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+keyCols, k.ID, k.Name, k.PEM, k.Algorithm, k.KeySize, k.IsActive))
}

func UpdateKey(ctx context.Context, db DBTX, k Key) (Key, error) {
	return one[Key](db.Query(ctx, `UPDATE jwt_public_keys SET name = $2, is_active = $3 WHERE id = $1 RETURNING `+keyCols,
		k.ID, k.Name, k.IsActive))
}

func DeleteKey(ctx context.Context, db DBTX, id uuid.UUID) error {
	return execOne(db.Exec(ctx, `DELETE FROM jwt_public_keys WHERE id = $1`, id))
}

func DevicesUsingKey(ctx context.Context, db DBTX, keyID uuid.UUID) ([]Device, error) {
	return many[Device](db.Query(ctx, `SELECT `+deviceCols+` FROM devices WHERE public_key_id = $1`, keyID))
}

// --- Devices ---

const deviceCols = `id, name, description, public_key_id`

func ListDevices(ctx context.Context, db DBTX) ([]Device, error) {
	return many[Device](db.Query(ctx, `SELECT `+deviceCols+` FROM devices ORDER BY name, id`))
}

func GetDevice(ctx context.Context, db DBTX, id uuid.UUID) (Device, error) {
	return one[Device](db.Query(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = $1`, id))
}

func InsertDevice(ctx context.Context, db DBTX, d Device) (Device, error) {
	return one[Device](db.Query(ctx, `INSERT INTO devices (id, name, description, public_key_id) VALUES ($1, $2, $3, $4)
		RETURNING `+deviceCols, d.ID, d.Name, d.Description, d.PublicKeyID))
}

func UpdateDevice(ctx context.Context, db DBTX, d Device) (Device, error) {
	return one[Device](db.Query(ctx, `UPDATE devices SET name = $2, description = $3, public_key_id = $4 WHERE id = $1
		RETURNING `+deviceCols, d.ID, d.Name, d.Description, d.PublicKeyID))
}

func DeleteDevice(ctx context.Context, db DBTX, id uuid.UUID) error {
	return execOne(db.Exec(ctx, `DELETE FROM devices WHERE id = $1`, id))
}

// DeviceAuth is the device + key view used by device authentication.
type DeviceAuth struct {
	DeviceID   uuid.UUID `db:"device_id"`
	DeviceName string    `db:"device_name"`
	KeyID      uuid.UUID `db:"key_id"`
	PEM        string    `db:"pem"`
	Algorithm  string    `db:"algorithm"`
	KeyActive  bool      `db:"key_active"`
}

// GetDeviceAuth returns ErrNotFound if the device doesn't exist or has no key.
func GetDeviceAuth(ctx context.Context, db DBTX, id uuid.UUID) (DeviceAuth, error) {
	return one[DeviceAuth](db.Query(ctx, `SELECT d.id AS device_id, d.name AS device_name, k.id AS key_id, k.pem, k.algorithm, k.is_active AS key_active
		FROM devices d JOIN jwt_public_keys k ON k.id = d.public_key_id WHERE d.id = $1`, id))
}

// --- Elements ---

const elementCols = `id, device_id, name, points, description, details, created_at, msg_rate, msg_burst, over_limit`

func ListElements(ctx context.Context, db DBTX, deviceID *uuid.UUID) ([]Element, error) {
	return many[Element](db.Query(ctx, `SELECT `+elementCols+` FROM elements
		WHERE ($1::uuid IS NULL OR device_id = $1) ORDER BY created_at, id`, deviceID))
}

func GetElement(ctx context.Context, db DBTX, id uuid.UUID) (Element, error) {
	return one[Element](db.Query(ctx, `SELECT `+elementCols+` FROM elements WHERE id = $1`, id))
}

func InsertElement(ctx context.Context, db DBTX, e Element) (Element, error) {
	return one[Element](db.Query(ctx, `INSERT INTO elements (id, device_id, name, points, description, details, msg_rate, msg_burst, over_limit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+elementCols,
		e.ID, e.DeviceID, e.Name, e.Points, e.Description, nullJSON(e.Details), e.MsgRate, e.MsgBurst, overLimit(e.OverLimit)))
}

func UpdateElement(ctx context.Context, db DBTX, e Element) (Element, error) {
	return one[Element](db.Query(ctx, `UPDATE elements SET name = $2, points = $3, description = $4, details = $5,
		msg_rate = $6, msg_burst = $7, over_limit = $8
		WHERE id = $1 RETURNING `+elementCols, e.ID, e.Name, e.Points, e.Description, nullJSON(e.Details),
		e.MsgRate, e.MsgBurst, overLimit(e.OverLimit)))
}

func DeleteElement(ctx context.Context, db DBTX, id uuid.UUID) error {
	return execOne(db.Exec(ctx, `DELETE FROM elements WHERE id = $1`, id))
}

// --- Styles ---

const styleCols = `id, element_id, name, details`

func ListStyles(ctx context.Context, db DBTX, elementID uuid.UUID) ([]Style, error) {
	return many[Style](db.Query(ctx, `SELECT `+styleCols+` FROM element_styles WHERE element_id = $1 ORDER BY id`, elementID))
}

func GetStyle(ctx context.Context, db DBTX, id int64) (Style, error) {
	return one[Style](db.Query(ctx, `SELECT `+styleCols+` FROM element_styles WHERE id = $1`, id))
}

func InsertStyle(ctx context.Context, db DBTX, s Style) (Style, error) {
	return one[Style](db.Query(ctx, `INSERT INTO element_styles (element_id, name, details) VALUES ($1, $2, $3) RETURNING `+styleCols,
		s.ElementID, s.Name, nullJSON(s.Details)))
}

func UpdateStyle(ctx context.Context, db DBTX, s Style) (Style, error) {
	return one[Style](db.Query(ctx, `UPDATE element_styles SET name = $2, details = $3 WHERE id = $1 RETURNING `+styleCols,
		s.ID, s.Name, nullJSON(s.Details)))
}

func DeleteStyle(ctx context.Context, db DBTX, id int64) error {
	return execOne(db.Exec(ctx, `DELETE FROM element_styles WHERE id = $1`, id))
}

// Over-limit policies of an element.
const (
	OverLimitDrop   = "drop"
	OverLimitLatest = "latest"
)

func overLimit(p string) string {
	if p == "" {
		return OverLimitDrop
	}
	return p
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}

// --- Device config snapshots (device-config.v1) ---

// LockDevices row-locks the given devices in id order and returns the ones
// that exist. Every transaction that publishes a device snapshot takes this
// lock first, so snapshots of one device are built and enqueued in commit
// order and the newest snapshot always wins on the compacted topic.
func LockDevices(ctx context.Context, db DBTX, ids []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := db.Query(ctx, `SELECT id FROM devices WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	return out, mapErr(err)
}

type deviceConfigRow struct {
	ID        uuid.UUID  `db:"id"`
	Name      string     `db:"name"`
	KeyID     *uuid.UUID `db:"key_id"`
	PEM       *string    `db:"pem"`
	Algorithm *string    `db:"algorithm"`
	KeyActive *bool      `db:"key_active"`
	Version   int64      `db:"version"`
}

// DeviceConfig builds the device's snapshot; ErrNotFound if it doesn't exist.
func DeviceConfig(ctx context.Context, db DBTX, id uuid.UUID) (events.DeviceConfig, error) {
	r, err := one[deviceConfigRow](db.Query(ctx, `SELECT d.id, d.name, k.id AS key_id, k.pem, k.algorithm, k.is_active AS key_active,
			(extract(epoch FROM clock_timestamp()) * 1000000)::bigint AS version
		FROM devices d LEFT JOIN jwt_public_keys k ON k.id = d.public_key_id WHERE d.id = $1`, id))
	if err != nil {
		return events.DeviceConfig{}, err
	}
	els, err := ListElements(ctx, db, &id)
	if err != nil {
		return events.DeviceConfig{}, err
	}
	cfg := events.DeviceConfig{Device: events.DeviceInfo{ID: r.ID, Name: r.Name}, Version: r.Version,
		Elements: make([]events.ElementConfig, len(els))}
	if r.KeyID != nil {
		cfg.Key = &events.DeviceKey{ID: *r.KeyID, PEM: *r.PEM, Algorithm: *r.Algorithm, Active: *r.KeyActive}
	}
	for i, e := range els {
		cfg.Elements[i] = events.ElementConfig{ID: e.ID, Name: e.Name, Points: e.Points, Rate: e.MsgRate,
			Burst: e.MsgBurst, OverLimit: overLimit(e.OverLimit)}
	}
	if len(els) > 0 {
		elementIDs := make([]uuid.UUID, len(els))
		for i, e := range els {
			elementIDs[i] = e.ID
		}
		rows, err := db.Query(ctx, `SELECT element_id, version, steps FROM element_pipelines WHERE element_id = ANY($1)`, elementIDs)
		if err == nil {
			type pipeRow struct {
				ElementID uuid.UUID       `db:"element_id"`
				Version   int             `db:"version"`
				Steps     json.RawMessage `db:"steps"`
			}
			pipes, err := pgx.CollectRows(rows, pgx.RowToStructByName[pipeRow])
			if err == nil {
				pipeMap := make(map[uuid.UUID]pipeRow, len(pipes))
				for _, p := range pipes {
					pipeMap[p.ElementID] = p
				}
				for i, e := range els {
					if pr, ok := pipeMap[e.ID]; ok && len(pr.Steps) > 0 && string(pr.Steps) != "[]" && string(pr.Steps) != "null" {
						cfg.Elements[i].Pipeline = &events.PipelineConfig{Version: pr.Version, Steps: pr.Steps}
					}
				}
			}
		}
	}
	return cfg, nil
}

// DeviceIDs returns every device id (registry backfill).
func DeviceIDs(ctx context.Context, db DBTX) ([]uuid.UUID, error) {
	rows, err := db.Query(ctx, `SELECT id FROM devices ORDER BY id`)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	return out, mapErr(err)
}
