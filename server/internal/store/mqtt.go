package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/events"
)

// --- MQTT connections (config for the gateway's mqtt role) ---

const mqttConnCols = `id, name, broker_url, client_id_prefix, keepalive, session_expiry, receive_maximum, replicas, auth, tls, enabled`

func scanMQTTConn(row pgx.Row) (events.MQTTConnection, error) {
	var c events.MQTTConnection
	err := row.Scan(&c.ID, &c.Name, &c.BrokerURL, &c.ClientIDPrefix, &c.Keepalive, &c.SessionExpiry, &c.ReceiveMaximum,
		&c.Replicas, &c.Auth, &c.TLS, &c.Enabled)
	return c, mapErr(err)
}

func jsonOrEmpty(b json.RawMessage) json.RawMessage {
	if len(b) == 0 || string(b) == "null" {
		return json.RawMessage(`{}`)
	}
	return b
}

func InsertMQTTConnection(ctx context.Context, db DBTX, c events.MQTTConnection) (events.MQTTConnection, error) {
	return scanMQTTConn(db.QueryRow(ctx, `INSERT INTO mqtt_connections (`+mqttConnCols+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING `+mqttConnCols,
		c.ID, c.Name, c.BrokerURL, c.ClientIDPrefix, c.Keepalive, c.SessionExpiry, c.ReceiveMaximum, c.Replicas,
		jsonOrEmpty(c.Auth), jsonOrEmpty(c.TLS), c.Enabled))
}

func UpdateMQTTConnection(ctx context.Context, db DBTX, c events.MQTTConnection) (events.MQTTConnection, error) {
	return scanMQTTConn(db.QueryRow(ctx, `UPDATE mqtt_connections SET name = $2, broker_url = $3, client_id_prefix = $4,
		keepalive = $5, session_expiry = $6, receive_maximum = $7, replicas = $8, auth = $9, tls = $10, enabled = $11,
		updated_at = now() WHERE id = $1 RETURNING `+mqttConnCols,
		c.ID, c.Name, c.BrokerURL, c.ClientIDPrefix, c.Keepalive, c.SessionExpiry, c.ReceiveMaximum, c.Replicas,
		jsonOrEmpty(c.Auth), jsonOrEmpty(c.TLS), c.Enabled))
}

func GetMQTTConnection(ctx context.Context, db DBTX, id string) (events.MQTTConnection, error) {
	return scanMQTTConn(db.QueryRow(ctx, `SELECT `+mqttConnCols+` FROM mqtt_connections WHERE id = $1`, id))
}

func ListMQTTConnections(ctx context.Context, db DBTX) ([]events.MQTTConnection, error) {
	rows, err := db.Query(ctx, `SELECT `+mqttConnCols+` FROM mqtt_connections ORDER BY name, id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []events.MQTTConnection{}
	for rows.Next() {
		c, err := scanMQTTConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, mapErr(rows.Err())
}

func DeleteMQTTConnection(ctx context.Context, db DBTX, id string) error {
	return execOne(db.Exec(ctx, `DELETE FROM mqtt_connections WHERE id = $1`, id))
}

func MQTTConnectionIDs(ctx context.Context, db DBTX) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT id FROM mqtt_connections ORDER BY id`)
	if err != nil {
		return nil, mapErr(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, mapErr(err)
}

// LockMQTTConnection row-locks a connection, so its snapshots are built and
// enqueued in commit order (see LockDevices).
func LockMQTTConnection(ctx context.Context, db DBTX, id string) error {
	var got string
	return mapErr(db.QueryRow(ctx, `SELECT id FROM mqtt_connections WHERE id = $1 FOR UPDATE`, id).Scan(&got))
}

// --- Uplinks ---

const mqttUplinkCols = `id, topic_filter, qos, format, decoder_id, device, field_map, "time", enabled, capture_until`

func scanMQTTUplink(row pgx.Row) (events.MQTTUplink, error) {
	var u events.MQTTUplink
	err := row.Scan(&u.ID, &u.TopicFilter, &u.QoS, &u.Format, &u.DecoderID, &u.Device, &u.FieldMap, &u.Time, &u.Enabled, &u.CaptureUntil)
	return u, mapErr(err)
}

func InsertMQTTUplink(ctx context.Context, db DBTX, connectionID string, u events.MQTTUplink) (events.MQTTUplink, error) {
	return scanMQTTUplink(db.QueryRow(ctx, `INSERT INTO mqtt_uplinks (id, connection_id, topic_filter, qos, format, decoder_id,
		device, field_map, "time", enabled) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+mqttUplinkCols,
		u.ID, connectionID, u.TopicFilter, u.QoS, u.Format, u.DecoderID, u.Device, jsonArrayOrEmpty(u.FieldMap), u.Time, u.Enabled))
}

func jsonArrayOrEmpty(b json.RawMessage) json.RawMessage {
	if len(b) == 0 || string(b) == "null" {
		return json.RawMessage(`[]`)
	}
	return b
}

func ListMQTTUplinks(ctx context.Context, db DBTX, connectionID string) ([]events.MQTTUplink, error) {
	rows, err := db.Query(ctx, `SELECT `+mqttUplinkCols+` FROM mqtt_uplinks WHERE connection_id = $1 ORDER BY created_at, id`, connectionID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []events.MQTTUplink{}
	for rows.Next() {
		u, err := scanMQTTUplink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, mapErr(rows.Err())
}

// MQTTUplinkConnection returns the connection an uplink belongs to.
func MQTTUplinkConnection(ctx context.Context, db DBTX, id string) (string, error) {
	var c string
	return c, mapErr(db.QueryRow(ctx, `SELECT connection_id FROM mqtt_uplinks WHERE id = $1`, id).Scan(&c))
}

func DeleteMQTTUplink(ctx context.Context, db DBTX, id string) error {
	return execOne(db.Exec(ctx, `DELETE FROM mqtt_uplinks WHERE id = $1`, id))
}

func SetMQTTUplinkCapture(ctx context.Context, db DBTX, id string, until *time.Time) error {
	return execOne(db.Exec(ctx, `UPDATE mqtt_uplinks SET capture_until = $2, updated_at = now() WHERE id = $1`, id, until))
}

// --- Downlinks ---

const mqttDownlinkCols = `id, device_external_id, element, topic_template, encoder, qos, retain, content_type, message_expiry, response_topic, user_properties`

func scanMQTTDownlink(row pgx.Row) (events.MQTTDownlink, error) {
	var d events.MQTTDownlink
	err := row.Scan(&d.ID, &d.DeviceExternalID, &d.Element, &d.TopicTemplate, &d.Encoder, &d.QoS, &d.Retain,
		&d.ContentType, &d.MessageExpiry, &d.ResponseTopic, &d.UserProperties)
	return d, mapErr(err)
}

func InsertMQTTDownlink(ctx context.Context, db DBTX, connectionID string, d events.MQTTDownlink) (events.MQTTDownlink, error) {
	return scanMQTTDownlink(db.QueryRow(ctx, `INSERT INTO mqtt_downlinks (id, connection_id, device_external_id, element,
		topic_template, encoder, qos, retain, content_type, message_expiry, response_topic, user_properties)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING `+mqttDownlinkCols,
		d.ID, connectionID, d.DeviceExternalID, d.Element, d.TopicTemplate, jsonOrEmpty(d.Encoder), d.QoS, d.Retain,
		d.ContentType, d.MessageExpiry, d.ResponseTopic, nullJSON(d.UserProperties)))
}

func ListMQTTDownlinks(ctx context.Context, db DBTX, connectionID string) ([]events.MQTTDownlink, error) {
	rows, err := db.Query(ctx, `SELECT `+mqttDownlinkCols+` FROM mqtt_downlinks WHERE connection_id = $1 ORDER BY created_at, id`, connectionID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []events.MQTTDownlink{}
	for rows.Next() {
		d, err := scanMQTTDownlink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

func MQTTDownlinkConnection(ctx context.Context, db DBTX, id string) (string, error) {
	var c string
	return c, mapErr(db.QueryRow(ctx, `SELECT connection_id FROM mqtt_downlinks WHERE id = $1`, id).Scan(&c))
}

func DeleteMQTTDownlink(ctx context.Context, db DBTX, id string) error {
	return execOne(db.Exec(ctx, `DELETE FROM mqtt_downlinks WHERE id = $1`, id))
}

// --- Granted devices ---

func ListMQTTGrantedDevices(ctx context.Context, db DBTX, connectionID string) ([]events.MQTTGrantedDevice, error) {
	rows, err := db.Query(ctx, `SELECT device_id, external_id FROM mqtt_connection_devices WHERE connection_id = $1 ORDER BY external_id`, connectionID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []events.MQTTGrantedDevice{}
	for rows.Next() {
		var d events.MQTTGrantedDevice
		if err := rows.Scan(&d.DeviceID, &d.ExternalID); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

type txStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

func UpsertMQTTGrant(ctx context.Context, db DBTX, connectionID string, deviceID uuid.UUID, externalID string) error {
	if starter, ok := db.(txStarter); ok {
		tx, err := starter.Begin(ctx)
		if err != nil {
			return mapErr(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := upsertMQTTGrantLocked(ctx, tx, connectionID, deviceID, externalID); err != nil {
			return err
		}
		return mapErr(tx.Commit(ctx))
	}
	return upsertMQTTGrantLocked(ctx, db, connectionID, deviceID, externalID)
}

func upsertMQTTGrantLocked(ctx context.Context, db DBTX, connectionID string, deviceID uuid.UUID, externalID string) error {
	var dummy string
	if err := db.QueryRow(ctx, `SELECT id FROM mqtt_connections WHERE id = $1 FOR UPDATE`, connectionID).Scan(&dummy); err != nil {
		return mapErr(err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM mqtt_connection_devices
		WHERE connection_id = $1 AND (device_id = $2 OR external_id = $3)`,
		connectionID, deviceID, externalID); err != nil {
		return mapErr(err)
	}
	_, err := db.Exec(ctx, `INSERT INTO mqtt_connection_devices (connection_id, device_id, external_id) VALUES ($1, $2, $3)
		ON CONFLICT (connection_id, device_id) DO UPDATE SET external_id = EXCLUDED.external_id`, connectionID, deviceID, externalID)
	return mapErr(err)
}

func DeleteMQTTGrant(ctx context.Context, db DBTX, connectionID string, deviceID uuid.UUID) error {
	return execOne(db.Exec(ctx, `DELETE FROM mqtt_connection_devices WHERE connection_id = $1 AND device_id = $2`, connectionID, deviceID))
}

// MQTTConnectionsOfDevice lists connections that serve a device (their
// snapshots change when the device is deleted).
func MQTTConnectionsOfDevice(ctx context.Context, db DBTX, deviceID uuid.UUID) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT connection_id FROM mqtt_connection_devices WHERE device_id = $1`, deviceID)
	if err != nil {
		return nil, mapErr(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, mapErr(err)
}

// --- Decoders ---

func InsertDecoder(ctx context.Context, db DBTX, d events.MQTTDecoder, by string) (events.MQTTDecoder, error) {
	if _, err := db.Exec(ctx, `INSERT INTO decoders (id, name, source, version, updated_by) VALUES ($1, $2, $3, 1, $4)`,
		d.ID, d.Name, d.Source, by); err != nil {
		return d, mapErr(err)
	}
	_, err := db.Exec(ctx, `INSERT INTO decoder_versions (decoder_id, version, source, updated_by) VALUES ($1, 1, $2, $3)`, d.ID, d.Source, by)
	d.Version = 1
	return d, mapErr(err)
}

// UpdateDecoderSource stores a new version and keeps the old one in decoder_versions.
func UpdateDecoderSource(ctx context.Context, db DBTX, id, name, source, by string) (events.MQTTDecoder, error) {
	var d events.MQTTDecoder
	err := db.QueryRow(ctx, `UPDATE decoders SET name = $2, source = $3, version = version + 1, updated_by = $4, updated_at = now()
		WHERE id = $1 RETURNING id, name, version, source`, id, name, source, by).Scan(&d.ID, &d.Name, &d.Version, &d.Source)
	if err != nil {
		return d, mapErr(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO decoder_versions (decoder_id, version, source, updated_by) VALUES ($1, $2, $3, $4)`, id, d.Version, source, by)
	return d, mapErr(err)
}

func GetDecoder(ctx context.Context, db DBTX, id string) (events.MQTTDecoder, error) {
	var d events.MQTTDecoder
	err := db.QueryRow(ctx, `SELECT id, name, version, source FROM decoders WHERE id = $1`, id).Scan(&d.ID, &d.Name, &d.Version, &d.Source)
	return d, mapErr(err)
}

func ListDecoders(ctx context.Context, db DBTX) ([]events.MQTTDecoder, error) {
	rows, err := db.Query(ctx, `SELECT id, name, version, source FROM decoders ORDER BY name, id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []events.MQTTDecoder{}
	for rows.Next() {
		var d events.MQTTDecoder
		if err := rows.Scan(&d.ID, &d.Name, &d.Version, &d.Source); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

func DeleteDecoder(ctx context.Context, db DBTX, id string) error {
	return execOne(db.Exec(ctx, `DELETE FROM decoders WHERE id = $1`, id))
}

// ConnectionsUsingDecoder lists connections whose uplinks use a decoder.
func ConnectionsUsingDecoder(ctx context.Context, db DBTX, decoderID string) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT connection_id FROM mqtt_uplinks WHERE decoder_id = $1
		UNION SELECT connection_id FROM mqtt_downlinks WHERE encoder->>'decoder_id' = $1`, decoderID)
	if err != nil {
		return nil, mapErr(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, mapErr(err)
}

func decodersForConnection(ctx context.Context, db DBTX, connectionID string) ([]events.MQTTDecoder, error) {
	// decoders used by uplinks, and by downlink encoders ({decoder_id: …})
	rows, err := db.Query(ctx, `SELECT d.id, d.name, d.version, d.source FROM decoders d WHERE d.id IN (
			SELECT decoder_id FROM mqtt_uplinks WHERE connection_id = $1 AND decoder_id IS NOT NULL
			UNION SELECT encoder->>'decoder_id' FROM mqtt_downlinks WHERE connection_id = $1 AND encoder ? 'decoder_id')
		ORDER BY d.id`, connectionID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []events.MQTTDecoder{}
	for rows.Next() {
		var d events.MQTTDecoder
		if err := rows.Scan(&d.ID, &d.Name, &d.Version, &d.Source); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

// MQTTConfig builds a connection's snapshot; ErrNotFound if it doesn't exist.
// The version comes from the database clock, like DeviceConfig.
func MQTTConfig(ctx context.Context, db DBTX, id string) (events.MQTTConfig, error) {
	var cfg events.MQTTConfig
	var err error
	if cfg.Connection, err = GetMQTTConnection(ctx, db, id); err != nil {
		return cfg, err
	}
	if cfg.Uplinks, err = ListMQTTUplinks(ctx, db, id); err != nil {
		return cfg, err
	}
	if cfg.Downlinks, err = ListMQTTDownlinks(ctx, db, id); err != nil {
		return cfg, err
	}
	if cfg.Decoders, err = decodersForConnection(ctx, db, id); err != nil {
		return cfg, err
	}
	if cfg.Devices, err = ListMQTTGrantedDevices(ctx, db, id); err != nil {
		return cfg, err
	}
	err = db.QueryRow(ctx, `SELECT (extract(epoch FROM clock_timestamp()) * 1000000)::bigint`).Scan(&cfg.Version)
	return cfg, mapErr(err)
}

// --- Status (written by gateways with the mqtt role) ---

type MQTTStatus struct {
	ConnectionID string          `db:"connection_id" json:"connection_id"`
	Slot         int             `db:"slot" json:"slot"`
	GatewayID    string          `db:"gateway_id" json:"gateway_id"`
	Connected    bool            `db:"connected" json:"connected"`
	ReasonCode   *int            `db:"reason_code" json:"reason_code"`
	LastError    *string         `db:"last_error" json:"last_error"`
	Counters     json.RawMessage `db:"counters" json:"counters"`
	UpdatedAt    time.Time       `db:"updated_at" json:"updated_at"`
}

func UpsertMQTTStatus(ctx context.Context, db DBTX, s MQTTStatus) error {
	_, err := db.Exec(ctx, `INSERT INTO mqtt_connection_status (connection_id, slot, gateway_id, connected, reason_code, last_error, counters, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (connection_id, slot) DO UPDATE SET gateway_id = EXCLUDED.gateway_id, connected = EXCLUDED.connected,
			reason_code = EXCLUDED.reason_code, last_error = EXCLUDED.last_error, counters = EXCLUDED.counters, updated_at = now()`,
		s.ConnectionID, s.Slot, s.GatewayID, s.Connected, s.ReasonCode, s.LastError, jsonOrEmpty(s.Counters))
	if errors.Is(mapErr(err), ErrNotFound) { // connection deleted meanwhile
		return nil
	}
	return mapErr(err)
}

func ListMQTTStatus(ctx context.Context, db DBTX) ([]MQTTStatus, error) {
	return many[MQTTStatus](db.Query(ctx, `SELECT connection_id, slot, gateway_id, connected, reason_code, last_error, counters, updated_at
		FROM mqtt_connection_status ORDER BY connection_id, slot`))
}

// --- Gateway membership ---

type GatewayMember struct {
	GatewayID string    `db:"gateway_id"`
	Roles     []string  `db:"roles"`
	Weight    float64   `db:"weight"`
	LastSeen  time.Time `db:"last_seen"`
}

func HeartbeatMember(ctx context.Context, db DBTX, gatewayID string, roles []string, weight float64) error {
	_, err := db.Exec(ctx, `INSERT INTO gateway_members (gateway_id, roles, weight, last_seen) VALUES ($1, $2, $3, now())
		ON CONFLICT (gateway_id) DO UPDATE SET roles = EXCLUDED.roles, weight = EXCLUDED.weight, last_seen = now()`,
		gatewayID, roles, weight)
	return mapErr(err)
}

func LiveMembers(ctx context.Context, db DBTX, ttl time.Duration) ([]GatewayMember, error) {
	return many[GatewayMember](db.Query(ctx, `SELECT gateway_id, roles, weight::float8 AS weight, last_seen FROM gateway_members
		WHERE last_seen > now() - $1::interval ORDER BY gateway_id`, ttl))
}

func DeleteMember(ctx context.Context, db DBTX, gatewayID string) error {
	_, err := db.Exec(ctx, `DELETE FROM gateway_members WHERE gateway_id = $1`, gatewayID)
	return mapErr(err)
}
