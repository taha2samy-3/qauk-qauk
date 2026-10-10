package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MQTTConfig is everything a gateway with the mqtt role needs to serve one
// MQTT connection (a snapshot on mqtt-config.v1). Secrets are references
// (env:NAME, file:/path), never values.
type MQTTConfig struct {
	Connection MQTTConnection      `json:"connection"`
	Uplinks    []MQTTUplink        `json:"uplinks"`
	Downlinks  []MQTTDownlink      `json:"downlinks"`
	Decoders   []MQTTDecoder       `json:"decoders"`
	Devices    []MQTTGrantedDevice `json:"devices"`
	Version    int64               `json:"version"`
}

type MQTTConnection struct {
	ID             string          `json:"id" readOnly:"true"`
	Name           string          `json:"name" minLength:"1" maxLength:"100"`
	BrokerURL      string          `json:"broker_url" doc:"mqtt://, mqtts://, tcp://, tls://, ws:// or wss://"`
	ClientIDPrefix string          `json:"client_id_prefix,omitempty" doc:"Client ids are <prefix>-<slot>; default quack-<short id>"`
	Keepalive      int             `json:"keepalive,omitempty" doc:"Seconds; default 60"`
	SessionExpiry  int             `json:"session_expiry,omitempty" doc:"Seconds the broker keeps the session (and unacknowledged QoS 1 messages); default 3600"`
	ReceiveMaximum int             `json:"receive_maximum,omitempty" doc:"In-flight QoS 1/2 messages the broker may send; default 100"`
	Replicas       int             `json:"replicas,omitempty" doc:"Gateways that open this connection; >1 uses a shared subscription; default 1"`
	Auth           json.RawMessage `json:"auth,omitempty" doc:"{method: none|password|mtls, username, password: env:NAME|file:/path}"`
	TLS            json.RawMessage `json:"tls,omitempty" doc:"{ca, cert, key: env:|file: references, server_name, insecure_skip_verify}"`
	Enabled        bool            `json:"enabled"`
}

type MQTTUplink struct {
	ID           string          `json:"id" readOnly:"true"`
	TopicFilter  string          `json:"topic_filter" doc:"MQTT topic filter, + and # allowed"`
	QoS          int             `json:"qos" minimum:"0" maximum:"1"`
	Format       string          `json:"format" enum:"json,text,number,bytes"`
	DecoderID    *string         `json:"decoder_id,omitempty"`
	Device       json.RawMessage `json:"device" doc:"{segment: n} | {field: path} | {fixed: external id}"`
	FieldMap     json.RawMessage `json:"field_map" doc:"[{element, value: path, wrap: value|raw, when: exists}]"`
	Time         *string         `json:"time,omitempty" doc:"Path to a timestamp (RFC 3339 or epoch ms)"`
	Enabled      bool            `json:"enabled"`
	CaptureUntil *time.Time      `json:"capture_until,omitempty" doc:"Received messages go to mqtt-capture.v1 until then"`
}

type MQTTDownlink struct {
	ID               string          `json:"id" readOnly:"true"`
	DeviceExternalID string          `json:"device_external_id"`
	Element          string          `json:"element" doc:"Element name on that device"`
	TopicTemplate    string          `json:"topic_template" doc:"Placeholders {device} {element} {user}"`
	Encoder          json.RawMessage `json:"encoder" doc:"{template: any JSON, {{value}} replaced} | {decoder_id} (encodeDownlink) | {} = the message as is"`
	QoS              int             `json:"qos" minimum:"0" maximum:"1"`
	Retain           bool            `json:"retain"`
	ContentType      *string         `json:"content_type,omitempty"`
	MessageExpiry    *int            `json:"message_expiry,omitempty"`
	ResponseTopic    *string         `json:"response_topic,omitempty"`
	UserProperties   json.RawMessage `json:"user_properties,omitempty"`
}

type MQTTDecoder struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version int    `json:"version"`
	Source  string `json:"source"`
}

type MQTTGrantedDevice struct {
	DeviceID   uuid.UUID `json:"device_id"`
	ExternalID string    `json:"external_id"`
}

// MQTTRejected is a record on mqtt.dlq.v1: a message (or part of one) the
// source pipeline or the device core refused. Plain JSON, keyed by connection.
type MQTTRejected struct {
	ConnectionID     string    `json:"connection_id"`
	UplinkID         string    `json:"uplink_id,omitempty"`
	Topic            string    `json:"topic"`
	DeviceExternalID string    `json:"device_external_id,omitempty"`
	Payload          []byte    `json:"payload"` // first 4 KiB (base64 in JSON)
	PayloadSize      int       `json:"payload_size"`
	Reasons          []string  `json:"reasons"`
	GatewayID        string    `json:"gateway_id"`
	Time             time.Time `json:"time"`
}

// MQTTCaptured is a record on mqtt-capture.v1: a raw message of an uplink
// rule in capture mode. Plain JSON, keyed by uplink id.
type MQTTCaptured struct {
	ConnectionID   string            `json:"connection_id"`
	UplinkID       string            `json:"uplink_id"`
	Topic          string            `json:"topic"`
	Payload        []byte            `json:"payload"` // base64 in JSON
	ContentType    string            `json:"content_type,omitempty"`
	UserProperties map[string]string `json:"user_properties,omitempty"`
	GatewayID      string            `json:"gateway_id"`
	Time           time.Time         `json:"time"`
}
