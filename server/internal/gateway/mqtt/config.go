package mqtt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/mqttspec"
	"github.com/taha2samy/quackquack/server/internal/sourcepipe"
)

// connConfig is a compiled mqtt-config.v1 snapshot.
type connConfig struct {
	cfg       events.MQTTConfig
	connHash  string // connection-level settings: a change reconnects
	rules     []*rule
	errors    []string // rules or decoders that didn't compile
	decoders  map[string]*sourcepipe.Decoder
	grants    map[string]uuid.UUID // external id → device
	byDevice  map[uuid.UUID]string // device → external id
	downlinks map[string]*downlink // external id + "\x00" + element
}

type rule struct {
	id           string
	filter       string
	qos          byte
	captureUntil *time.Time
	pipe         *sourcepipe.Rule
}

type downlink struct {
	spec    events.MQTTDownlink
	encoder mqttspec.Encoder
}

func compile(cfg events.MQTTConfig, log *slog.Logger) *connConfig {
	c := &connConfig{cfg: cfg, decoders: map[string]*sourcepipe.Decoder{}, grants: map[string]uuid.UUID{},
		byDevice: map[uuid.UUID]string{}, downlinks: map[string]*downlink{}}
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(cfg.Connection)
	c.connHash = hex.EncodeToString(h.Sum(nil))
	for _, d := range cfg.Decoders {
		dec, err := sourcepipe.Compile(d.Source)
		if err != nil {
			c.errors = append(c.errors, "decoder "+d.Name+": "+err.Error())
			continue
		}
		c.decoders[d.ID] = dec
	}
	for _, u := range cfg.Uplinks {
		if !u.Enabled {
			continue
		}
		p, err := sourcepipe.CompileRule(u, c.decoders)
		if err != nil {
			c.errors = append(c.errors, "uplink "+u.TopicFilter+": "+err.Error())
			continue
		}
		c.rules = append(c.rules, &rule{id: u.ID, filter: u.TopicFilter, qos: byte(u.QoS), captureUntil: u.CaptureUntil, pipe: p})
	}
	for _, g := range cfg.Devices {
		c.grants[g.ExternalID] = g.DeviceID
		c.byDevice[g.DeviceID] = g.ExternalID
	}
	for _, d := range cfg.Downlinks {
		enc, err := mqttspec.ParseEncoder(d.Encoder)
		if err != nil {
			c.errors = append(c.errors, "downlink "+d.TopicTemplate+": "+err.Error())
			continue
		}
		c.downlinks[d.DeviceExternalID+"\x00"+d.Element] = &downlink{spec: d, encoder: enc}
	}
	for _, e := range c.errors {
		log.Warn("mqtt: config problem", "connection", cfg.Connection.ID, "problem", e)
	}
	return c
}

// subscriptions returns topic filter → QoS (the highest QoS of the rules on it).
func (c *connConfig) subscriptions() map[string]byte {
	out := map[string]byte{}
	for _, r := range c.rules {
		if q, ok := out[r.filter]; !ok || r.qos > q {
			out[r.filter] = r.qos
		}
	}
	return out
}
