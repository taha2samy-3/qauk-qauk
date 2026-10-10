package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/mqttspec"
	"github.com/taha2samy/quackquack/server/internal/sourcepipe"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// MQTT connections are configuration for the gateway's mqtt role. Every
// change locks the connection row and enqueues a full snapshot to
// mqtt-config.v1, exactly like device-config.v1 (see publishDevices).

var (
	clientIDPrefixRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	externalIDRe     = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,64}$`)
)

// publishMQTTConnections enqueues a snapshot (or a tombstone) per connection.
func publishMQTTConnections(ctx context.Context, tx pgx.Tx, ids ...string) error {
	for _, id := range ids {
		if err := store.LockMQTTConnection(ctx, tx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		cfg, err := store.MQTTConfig(ctx, tx, id)
		var payload []byte
		switch {
		case errors.Is(err, store.ErrNotFound):
			// tombstone
		case err != nil:
			return err
		default:
			ev, err := events.New(events.TypeMQTTConfig, events.SourceAPI, id, cfg)
			if err != nil {
				return err
			}
			if payload, err = json.Marshal(ev); err != nil {
				return err
			}
		}
		if err := store.EnqueueOutbox(ctx, tx, events.TopicMQTTConfig, id, payload); err != nil {
			return err
		}
	}
	return nil
}

// SyncMQTTConfigs republishes every connection (backfill, like SyncDeviceConfigs).
func (s *Service) SyncMQTTConfigs(ctx context.Context) (int, error) {
	ids, err := store.MQTTConnectionIDs(ctx, s.Pool)
	if err != nil {
		return 0, err
	}
	return len(ids), s.RepublishMQTTConnections(ctx, ids)
}

func (s *Service) RepublishMQTTConnections(ctx context.Context, ids []string) error {
	const batch = 200
	for start := 0; start < len(ids); start += batch {
		chunk := ids[start:min(start+batch, len(ids))]
		if err := s.tx(ctx, func(tx pgx.Tx) error { return publishMQTTConnections(ctx, tx, chunk...) }); err != nil {
			return err
		}
	}
	return nil
}

// --- connections ---

func (s *Service) validateMQTTConnection(c *events.MQTTConnection) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len(c.Name) > 100 {
		return invalid("name", "must be 1-100 characters")
	}
	u, err := url.Parse(c.BrokerURL)
	if err != nil || u.Host == "" {
		return invalid("broker_url", "must be a URL like mqtts://broker.example.com:8883")
	}
	switch u.Scheme {
	case "mqtt", "tcp", "mqtts", "tls", "ssl", "ws", "wss":
	default:
		return invalid("broker_url", "scheme must be mqtt, mqtts, tcp, tls, ws or wss")
	}
	if c.ClientIDPrefix == "" {
		c.ClientIDPrefix = "quack-" + strings.ReplaceAll(c.ID, "-", "")[:8]
	}
	if !clientIDPrefixRe.MatchString(c.ClientIDPrefix) {
		return invalid("client_id_prefix", "1-40 characters: letters, digits, _ and -")
	}
	def := func(v *int, d, lo, hi int, field string) error {
		if *v == 0 {
			*v = d
		}
		if *v < lo || *v > hi {
			return invalid(field, fmt.Sprintf("must be between %d and %d", lo, hi))
		}
		return nil
	}
	if err := def(&c.Keepalive, 60, 5, 3600, "keepalive"); err != nil {
		return err
	}
	if err := def(&c.SessionExpiry, 3600, 1, 7*24*3600, "session_expiry"); err != nil {
		return err
	}
	if err := def(&c.ReceiveMaximum, 100, 1, 65535, "receive_maximum"); err != nil {
		return err
	}
	if err := def(&c.Replicas, 1, 1, 10, "replicas"); err != nil {
		return err
	}
	auth, err := mqttspec.ParseAuth(c.Auth)
	if err != nil {
		return invalid("auth", err.Error())
	}
	tlsCfg, err := mqttspec.ParseTLS(c.TLS)
	if err != nil {
		return invalid("tls", err.Error())
	}
	if tlsCfg.InsecureSkipVerify && !s.AllowInsecureTLS {
		return invalid("tls", "insecure_skip_verify needs QUACK_ALLOW_INSECURE_TLS=true on the server")
	}
	secure := u.Scheme == "mqtts" || u.Scheme == "tls" || u.Scheme == "ssl" || u.Scheme == "wss"
	if auth.Method == mqttspec.AuthPassword && !secure && !s.AllowInsecureTLS {
		return invalid("auth", "passwords need TLS (mqtts:// or wss://), or QUACK_ALLOW_INSECURE_TLS=true for development")
	}
	if auth.Method == mqttspec.AuthMTLS && !secure {
		return invalid("auth", "mtls needs mqtts://, tls:// or wss://")
	}
	return nil
}

func (s *Service) CreateMQTTConnection(ctx context.Context, a Actor, in events.MQTTConnection) (events.MQTTConnection, error) {
	in.ID = newUUID().String()
	if err := s.validateMQTTConnection(&in); err != nil {
		return events.MQTTConnection{}, err
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if in, err = store.InsertMQTTConnection(ctx, tx, in); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, in.ID); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "mqtt_connection", in.ID, map[string]any{"name": in.Name, "broker_url": in.BrokerURL})
	})
	return in, err
}

func (s *Service) UpdateMQTTConnection(ctx context.Context, a Actor, id string, in events.MQTTConnection) (events.MQTTConnection, error) {
	in.ID = id
	if err := s.validateMQTTConnection(&in); err != nil {
		return events.MQTTConnection{}, err
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.LockMQTTConnection(ctx, tx, id); err != nil {
			return err
		}
		var err error
		if in, err = store.UpdateMQTTConnection(ctx, tx, in); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "mqtt_connection", id, map[string]any{"name": in.Name, "broker_url": in.BrokerURL, "enabled": in.Enabled})
	})
	return in, err
}

func (s *Service) DeleteMQTTConnection(ctx context.Context, a Actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.LockMQTTConnection(ctx, tx, id); err != nil {
			return err
		}
		if err := store.DeleteMQTTConnection(ctx, tx, id); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "mqtt_connection", id, nil)
	})
}

// --- uplinks ---

func (s *Service) validateUplink(ctx context.Context, tx pgx.Tx, u *events.MQTTUplink) error {
	if err := mqttspec.ValidateFilter(u.TopicFilter); err != nil {
		return invalid("topic_filter", err.Error())
	}
	if u.QoS < 0 || u.QoS > 1 {
		return invalid("qos", "must be 0 or 1")
	}
	if u.Format == "" {
		u.Format = "json"
	}
	switch u.Format {
	case "json", "text", "number", "bytes":
	default:
		return invalid("format", "must be json, text, number or bytes")
	}
	if _, err := mqttspec.ParseDevice(u.Device); err != nil {
		return invalid("device", err.Error())
	}
	if _, err := mqttspec.ParseFieldMap(u.FieldMap); err != nil {
		return invalid("field_map", err.Error())
	}
	if u.DecoderID != nil {
		if *u.DecoderID == "" {
			u.DecoderID = nil
		} else if _, err := store.GetDecoder(ctx, tx, *u.DecoderID); err != nil {
			return invalid("decoder_id", "no such decoder")
		}
	}
	if u.Format == "bytes" && u.DecoderID == nil {
		return invalid("decoder_id", "binary payloads need a decoder")
	}
	return nil
}

func (s *Service) CreateMQTTUplink(ctx context.Context, a Actor, connectionID string, in events.MQTTUplink) (events.MQTTUplink, error) {
	in.ID = newUUID().String()
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.LockMQTTConnection(ctx, tx, connectionID); err != nil {
			return err
		}
		if err := s.validateUplink(ctx, tx, &in); err != nil {
			return err
		}
		var err error
		if in, err = store.InsertMQTTUplink(ctx, tx, connectionID, in); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, connectionID); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "mqtt_uplink", in.ID, map[string]any{"connection_id": connectionID, "topic_filter": in.TopicFilter})
	})
	return in, err
}

func (s *Service) DeleteMQTTUplink(ctx context.Context, a Actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		conn, err := store.MQTTUplinkConnection(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.LockMQTTConnection(ctx, tx, conn); err != nil {
			return err
		}
		if err := store.DeleteMQTTUplink(ctx, tx, id); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, conn); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "mqtt_uplink", id, nil)
	})
}

// CaptureMQTTUplink turns capture mode on for up to 10 minutes (0 = off).
func (s *Service) CaptureMQTTUplink(ctx context.Context, a Actor, id string, d time.Duration) (*time.Time, error) {
	var until *time.Time
	if d > 0 {
		t := time.Now().Add(min(d, 10*time.Minute)).UTC()
		until = &t
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		conn, err := store.MQTTUplinkConnection(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.LockMQTTConnection(ctx, tx, conn); err != nil {
			return err
		}
		if err := store.SetMQTTUplinkCapture(ctx, tx, id, until); err != nil {
			return err
		}
		return publishMQTTConnections(ctx, tx, conn)
	})
	return until, err
}

// --- downlinks ---

func (s *Service) CreateMQTTDownlink(ctx context.Context, a Actor, connectionID string, in events.MQTTDownlink) (events.MQTTDownlink, error) {
	in.ID = newUUID().String()
	if !externalIDRe.MatchString(in.DeviceExternalID) {
		return in, invalid("device_external_id", "1-64 characters: letters, digits and _ . : @ -")
	}
	if strings.TrimSpace(in.Element) == "" {
		return in, invalid("element", "required")
	}
	if err := mqttspec.ValidateTopicTemplate(in.TopicTemplate); err != nil {
		return in, invalid("topic_template", err.Error())
	}
	if in.QoS < 0 || in.QoS > 1 {
		return in, invalid("qos", "must be 0 or 1")
	}
	enc, err := mqttspec.ParseEncoder(in.Encoder)
	if err != nil {
		return in, invalid("encoder", err.Error())
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.LockMQTTConnection(ctx, tx, connectionID); err != nil {
			return err
		}
		if enc.DecoderID != "" {
			if _, err := store.GetDecoder(ctx, tx, enc.DecoderID); err != nil {
				return invalid("encoder", "no such decoder")
			}
		}
		var err error
		if in, err = store.InsertMQTTDownlink(ctx, tx, connectionID, in); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, connectionID); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "mqtt_downlink", in.ID, map[string]any{"connection_id": connectionID, "topic_template": in.TopicTemplate})
	})
	return in, err
}

func (s *Service) DeleteMQTTDownlink(ctx context.Context, a Actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		conn, err := store.MQTTDownlinkConnection(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.LockMQTTConnection(ctx, tx, conn); err != nil {
			return err
		}
		if err := store.DeleteMQTTDownlink(ctx, tx, id); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, conn); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "mqtt_downlink", id, nil)
	})
}

// --- granted devices ---

func (s *Service) GrantMQTTDevice(ctx context.Context, a Actor, connectionID string, deviceID uuid.UUID, externalID string) error {
	if !externalIDRe.MatchString(externalID) {
		return invalid("external_id", "1-64 characters: letters, digits and _ . : @ - (no MQTT wildcards or /)")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.LockMQTTConnection(ctx, tx, connectionID); err != nil {
			return err
		}
		if _, err := store.GetDevice(ctx, tx, deviceID); err != nil {
			return err
		}
		if err := store.UpsertMQTTGrant(ctx, tx, connectionID, deviceID, externalID); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, connectionID); err != nil {
			return err
		}
		return record(ctx, tx, a, "grant", "mqtt_connection", connectionID, map[string]any{"device_id": deviceID, "external_id": externalID})
	})
}

func (s *Service) RevokeMQTTDevice(ctx context.Context, a Actor, connectionID string, deviceID uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.LockMQTTConnection(ctx, tx, connectionID); err != nil {
			return err
		}
		if err := store.DeleteMQTTGrant(ctx, tx, connectionID, deviceID); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, connectionID); err != nil {
			return err
		}
		return record(ctx, tx, a, "revoke", "mqtt_connection", connectionID, map[string]any{"device_id": deviceID})
	})
}

// --- decoders ---

func validDecoderSource(src string) error {
	if strings.TrimSpace(src) == "" {
		return invalid("source", "required")
	}
	if len(src) > 40*1024 {
		return invalid("source", "at most 40 KB")
	}
	return nil
}

func (s *Service) CreateDecoder(ctx context.Context, a Actor, name, source string) (events.MQTTDecoder, error) {
	if name = strings.TrimSpace(name); name == "" || len(name) > 100 {
		return events.MQTTDecoder{}, invalid("name", "must be 1-100 characters")
	}
	if err := validDecoderSource(source); err != nil {
		return events.MQTTDecoder{}, err
	}
	if _, err := sourcepipe.Compile(source); err != nil {
		return events.MQTTDecoder{}, invalid("source", err.Error())
	}
	d := events.MQTTDecoder{ID: newUUID().String(), Name: name, Source: source}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if d, err = store.InsertDecoder(ctx, tx, d, a.Name); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "decoder", d.ID, map[string]any{"name": name})
	})
	return d, err
}

// UpdateDecoder stores a new version and republishes every connection using it.
func (s *Service) UpdateDecoder(ctx context.Context, a Actor, id, name, source string) (events.MQTTDecoder, error) {
	if name = strings.TrimSpace(name); name == "" || len(name) > 100 {
		return events.MQTTDecoder{}, invalid("name", "must be 1-100 characters")
	}
	if err := validDecoderSource(source); err != nil {
		return events.MQTTDecoder{}, err
	}
	if _, err := sourcepipe.Compile(source); err != nil {
		return events.MQTTDecoder{}, invalid("source", err.Error())
	}
	var d events.MQTTDecoder
	err := s.tx(ctx, func(tx pgx.Tx) error {
		conns, err := store.ConnectionsUsingDecoder(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, c := range conns {
			if err := store.LockMQTTConnection(ctx, tx, c); err != nil {
				return err
			}
		}
		if d, err = store.UpdateDecoderSource(ctx, tx, id, name, source, a.Name); err != nil {
			return err
		}
		if err := publishMQTTConnections(ctx, tx, conns...); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "decoder", id, map[string]any{"name": name, "version": d.Version})
	})
	return d, err
}

func (s *Service) DeleteDecoder(ctx context.Context, a Actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		conns, err := store.ConnectionsUsingDecoder(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(conns) > 0 {
			return invalid("id", "the decoder is used by uplink rules; remove them first")
		}
		if err := store.DeleteDecoder(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "decoder", id, nil)
	})
}
