package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
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

const elementCols = `id, device_id, name, points, description, details, created_at`

func ListElements(ctx context.Context, db DBTX, deviceID *uuid.UUID) ([]Element, error) {
	return many[Element](db.Query(ctx, `SELECT `+elementCols+` FROM elements
		WHERE ($1::uuid IS NULL OR device_id = $1) ORDER BY created_at, id`, deviceID))
}

func GetElement(ctx context.Context, db DBTX, id uuid.UUID) (Element, error) {
	return one[Element](db.Query(ctx, `SELECT `+elementCols+` FROM elements WHERE id = $1`, id))
}

func InsertElement(ctx context.Context, db DBTX, e Element) (Element, error) {
	return one[Element](db.Query(ctx, `INSERT INTO elements (id, device_id, name, points, description, details)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+elementCols, e.ID, e.DeviceID, e.Name, e.Points, e.Description, nullJSON(e.Details)))
}

func UpdateElement(ctx context.Context, db DBTX, e Element) (Element, error) {
	return one[Element](db.Query(ctx, `UPDATE elements SET name = $2, points = $3, description = $4, details = $5
		WHERE id = $1 RETURNING `+elementCols, e.ID, e.Name, e.Points, e.Description, nullJSON(e.Details)))
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

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
