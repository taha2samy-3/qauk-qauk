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
	DeviceMsgRate        float64       `env:"QUACK_DEVICE_MSG_RATE" envDefault:"50"`
	// BrowserMsgRate bounds subscribe/command frames per browser socket (each may hit the DB).
	BrowserMsgRate float64 `env:"QUACK_BROWSER_MSG_RATE" envDefault:"100"`

	PresenceHeartbeat time.Duration `env:"QUACK_PRESENCE_HEARTBEAT" envDefault:"10s"`
	PresenceTTL       time.Duration `env:"QUACK_PRESENCE_TTL" envDefault:"30s"`

	// WebDir is the built frontend (web/dist) served at "/"; empty disables it.
	WebDir   string `env:"QUACK_WEB_DIR"`
	LogLevel string `env:"QUACK_LOG_LEVEL" envDefault:"info"`
}

func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
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
