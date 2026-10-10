// Package config loads runtime configuration from environment variables.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	DatabaseURL  string   `env:"QUACK_DATABASE_URL,required"`
	KafkaBrokers []string `env:"QUACK_KAFKA_BROKERS" envSeparator:"," envDefault:"localhost:19092"`
	// TopicPrefix namespaces every topic (e.g. "staging."), so environments
	// sharing a Redpanda cluster stay apart. Each needs its own value.
	TopicPrefix string `env:"QUACK_TOPIC_PREFIX"`
	// KafkaAcksAll selects acks=all (durable) instead of acks=1 for element events.
	KafkaAcksAll bool `env:"QUACK_KAFKA_ACKS_ALL" envDefault:"true"`

	// IngestGroup is the Redpanda consumer group of the ingester. Every
	// environment writing to a different database needs its own group.
	IngestGroup string `env:"QUACK_INGEST_GROUP" envDefault:"quack-ingest"`

	HTTPAddr string `env:"QUACK_HTTP_ADDR" envDefault:":8080"`
	// Roles enabled in `quack serve`: api, gateway.
	Roles     []string `env:"QUACK_ROLES" envSeparator:"," envDefault:"api,gateway"`
	GatewayID string   `env:"QUACK_GATEWAY_ID"`

	// AllowedOrigins are full origins (scheme://host[:port]) trusted for browser
	// WebSockets and cross-origin API writes. Same-host requests are always allowed.
	AllowedOrigins []string      `env:"QUACK_ALLOWED_ORIGINS" envSeparator:","`
	CookieSecure   bool          `env:"QUACK_COOKIE_SECURE" envDefault:"true"`
	SessionTTL     time.Duration `env:"QUACK_SESSION_TTL" envDefault:"336h"`

	// DeviceJWTMaxLifetime caps exp-iat (or exp-now when iat is absent) for device tokens.
	DeviceJWTMaxLifetime time.Duration `env:"QUACK_DEVICE_JWT_MAX_LIFETIME" envDefault:"24h"`
	// DeviceMsgRate is a fixed guard on all messages of one device (msgs/s),
	// including frames that name no valid element. Element limits are below.
	DeviceMsgRate float64 `env:"QUACK_DEVICE_MSG_RATE" envDefault:"500"`
	// ElementMsgRate is the default rate of an element without its own limit;
	// ElementMsgRateMax is the highest rate an admin may set on an element.
	ElementMsgRate    float64 `env:"QUACK_ELEMENT_MSG_RATE" envDefault:"50"`
	ElementMsgRateMax float64 `env:"QUACK_ELEMENT_MSG_RATE_MAX" envDefault:"1000"`
	// RateLimitDriver selects where rate-limit buckets live: "local" (in
	// memory, per instance). See docs/refactor/DEVICE_ADAPTERS_PLAN.md.
	RateLimitDriver string `env:"QUACK_RATELIMIT_DRIVER" envDefault:"local"`
	// AllowInsecureTLS permits MQTT connections without TLS verification, or
	// with passwords over plain TCP. Development only.
	AllowInsecureTLS bool `env:"QUACK_ALLOW_INSECURE_TLS"`
	// MQTTWeight is this gateway's share of MQTT connections (HRW weight).
	MQTTWeight float64 `env:"QUACK_MQTT_WEIGHT" envDefault:"1"`
	// MQTTConnectionMsgRate bounds the element values one MQTT connection
	// may publish per second (0 = unlimited).
	MQTTConnectionMsgRate float64 `env:"QUACK_MQTT_CONNECTION_MSG_RATE" envDefault:"5000"`
	// SyncMaxWait caps the long-poll wait of GET /device/v1/sync.
	SyncMaxWait time.Duration `env:"QUACK_SYNC_MAX_WAIT" envDefault:"60s"`
	// StreamMaxAge closes long gRPC streams (plus jitter) so clients
	// reconnect and spread over new instances.
	StreamMaxAge time.Duration `env:"QUACK_STREAM_MAX_AGE" envDefault:"30m"`
	// BrowserMsgRate bounds subscribe/command frames per browser socket (each may hit the DB).
	BrowserMsgRate float64 `env:"QUACK_BROWSER_MSG_RATE" envDefault:"100"`

	PresenceHeartbeat time.Duration `env:"QUACK_PRESENCE_HEARTBEAT" envDefault:"10s"`
	PresenceTTL       time.Duration `env:"QUACK_PRESENCE_TTL" envDefault:"30s"`

	// History (time-series) store: see internal/history. Comma-separated
	// drivers write to every store and read from the first (for moving
	// between backends). An empty URL means QUACK_DATABASE_URL.
	HistoryDriver        string        `env:"QUACK_HISTORY_DRIVER" envDefault:"timescale"`
	HistoryURL           string        `env:"QUACK_HISTORY_URL"`
	HistoryRetention     time.Duration `env:"QUACK_HISTORY_RETENTION" envDefault:"8760h"`
	HistoryCompressAfter time.Duration `env:"QUACK_HISTORY_COMPRESS_AFTER" envDefault:"168h"`
	// Guards for the history API: per-query timeout, concurrent queries per
	// instance, and the most buckets one request may ask for.
	HistoryQueryTimeout time.Duration `env:"QUACK_HISTORY_QUERY_TIMEOUT" envDefault:"10s"`
	HistoryMaxQueries   int           `env:"QUACK_HISTORY_MAX_QUERIES" envDefault:"16"`
	HistoryMaxBuckets   int           `env:"QUACK_HISTORY_MAX_BUCKETS" envDefault:"1500"`
	// HistoryReplayWindow bounds how far back the gateway looks for the last
	// values of an element when a dashboard subscribes.
	HistoryReplayWindow time.Duration `env:"QUACK_HISTORY_REPLAY_WINDOW" envDefault:"720h"`

	// WebDir is the built frontend (web/dist) served at "/"; empty disables it.
	WebDir   string `env:"QUACK_WEB_DIR"`
	LogLevel string `env:"QUACK_LOG_LEVEL" envDefault:"info"`
}

func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if c.RateLimitDriver != "local" {
		return nil, fmt.Errorf("config: QUACK_RATELIMIT_DRIVER=%q is not supported (supported: local)", c.RateLimitDriver)
	}
	if c.HasRole("mqtt") && !c.HasRole("gateway") {
		return nil, fmt.Errorf("config: the mqtt role runs inside the gateway: use QUACK_ROLES=gateway,mqtt (a separate deployment is the future split plan)")
	}
	if c.GatewayID == "" {
		host, _ := os.Hostname()
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		c.GatewayID = fmt.Sprintf("gw-%s-%s", host, hex.EncodeToString(b))
	}
	return &c, nil
}

func (c *Config) HasRole(r string) bool { return slices.Contains(c.Roles, r) }
