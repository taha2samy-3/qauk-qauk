package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/cluster"
	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/gateway/mqtt"
	"github.com/taha2samy/quackquack/server/internal/ratelimit"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// viaMQTT prefixes the origin of values that came over an MQTT connection;
// it becomes the CloudEvent extension quackvia=mqtt/<connection id>.
const viaMQTT = "mqtt/"

// ErrUnknownDevice: the device isn't in the registry (deleted, or never existed).
var ErrUnknownDevice = errors.New("unknown device")

type commandHooks struct {
	mu    sync.RWMutex
	next  int
	hooks map[int]func(mqtt.Command)
}

// notifyCommand passes users' commands to the MQTT transport (downlinks).
func (g *Gateway) notifyCommand(m *events.ElementMessage, id uuid.UUID, at time.Time) {
	if m.Source == events.SourceDevice {
		return
	}
	g.cmds.mu.RLock()
	defer g.cmds.mu.RUnlock()
	if len(g.cmds.hooks) == 0 {
		return
	}
	name := ""
	var pipe *elementpipe.Pipeline
	if dev, ok := g.registry.Lookup(m.DeviceID); ok {
		if el, ok := dev.Element(m.ElementID); ok {
			name = el.Name
			pipe = el.Pipeline
		}
	}
	msg := m.Message
	if pipe != nil {
		var parsed map[string]any
		if err := json.Unmarshal(m.Message, &parsed); err == nil {
			if inv, err := pipe.Inverse(parsed); err == nil {
				if b, err := json.Marshal(inv); err == nil {
					msg = b
				}
			}
		}
	}
	cmd := mqtt.Command{DeviceID: m.DeviceID, ElementID: m.ElementID, Element: name, Message: msg,
		UserID: m.Actor.ID, UserName: m.Actor.Name, Time: at}
	for _, h := range g.cmds.hooks {
		h(cmd)
	}
}

// mqttCore is the gateway as seen by the MQTT transport.
type mqttCore struct{ g *Gateway }

func (c mqttCore) Publish(p mqtt.Publication, done func(error)) error {
	g := c.g
	dev, ok := g.registry.Lookup(p.DeviceID)
	if !ok {
		return ErrUnknownDevice
	}
	if ok, wait := g.limits.Allow("m:"+p.ConnectionID, ratelimit.Limit{Rate: g.cfg.MQTTConnectionMsgRate}, 1); !ok {
		return &RateLimitedError{Scope: "connection", RetryAfter: wait}
	}
	if err := g.allowDevice(dev.ID, 1); err != nil {
		return err
	}
	// MQTT devices never connect to us: like REST, a device is online while
	// its messages keep arriving (QUACK_PRESENCE_TTL)
	g.rest.touch(g, dev.ID, "mqtt", viaMQTT+p.ConnectionID, "")
	_, err := g.publishDeviceMessage(dev, viaMQTT+p.ConnectionID, DeviceMessage{Element: p.Element, ByName: true,
		Message: p.Message, ClientTS: p.TS, Done: done})
	return err
}

func (c mqttCore) OnCommand(fn func(mqtt.Command)) func() {
	h := &c.g.cmds
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hooks == nil {
		h.hooks = map[int]func(mqtt.Command){}
	}
	id := h.next
	h.next++
	h.hooks[id] = fn
	return func() {
		h.mu.Lock()
		delete(h.hooks, id)
		h.mu.Unlock()
	}
}

func (c mqttCore) SendUserError(userID string, code, description string, elementID uuid.UUID) {
	c.g.SendUserError(userID, code, description, elementID)
}

// mqttCluster reads live gateways with the mqtt role from gateway_members.
type mqttCluster struct{ g *Gateway }

func (c mqttCluster) Members(ctx context.Context) ([]cluster.Member, error) {
	ms, err := store.LiveMembers(ctx, c.g.pool, c.g.cfg.PresenceTTL)
	if err != nil {
		return nil, err
	}
	out := make([]cluster.Member, 0, len(ms))
	for _, m := range ms {
		if slices.Contains(m.Roles, "mqtt") {
			out = append(out, cluster.Member{ID: m.GatewayID, Weight: m.Weight})
		}
	}
	return out, nil
}

type mqttStatus struct{ g *Gateway }

func (s mqttStatus) ReportStatus(ctx context.Context, st mqtt.Status) {
	counters, _ := json.Marshal(st.Counters)
	var lastErr *string
	if st.LastError != "" {
		lastErr = &st.LastError
	}
	if err := store.UpsertMQTTStatus(ctx, s.g.pool, store.MQTTStatus{ConnectionID: st.ConnectionID, Slot: st.Slot,
		GatewayID: s.g.cfg.GatewayID, Connected: st.Connected, ReasonCode: st.ReasonCode, LastError: lastErr,
		Counters: counters}); err != nil && ctx.Err() == nil {
		s.g.log.Warn("gateway: mqtt status", "connection", st.ConnectionID, "slot", strconv.Itoa(st.Slot), "err", err)
	}
}

// startMQTT runs the MQTT transport (role mqtt); the channel closes once it
// has stopped and left the member list.
func (g *Gateway) startMQTT(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	g.mqtt = mqtt.New(mqtt.Options{
		GatewayID: g.cfg.GatewayID, Brokers: g.cfg.KafkaBrokers, Heartbeat: g.cfg.PresenceHeartbeat,
		AllowInsecureTLS: g.cfg.AllowInsecureTLS, Log: g.log.With("transport", "mqtt"),
		Core: mqttCore{g}, Cluster: mqttCluster{g}, Status: mqttStatus{g}, Records: g.producer,
	})
	g.heartbeatMember(ctx) // be a member before the first assignment
	go func() {
		defer close(done)
		if err := g.mqtt.Run(ctx); err != nil && ctx.Err() == nil {
			g.log.Error("gateway: mqtt transport stopped", "err", err)
		}
		dctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = store.DeleteMember(dctx, g.pool, g.cfg.GatewayID) // leave at once on shutdown
	}()
	return done
}

func (g *Gateway) heartbeatMember(ctx context.Context) {
	if g.mqtt == nil {
		return
	}
	w := g.cfg.MQTTWeight
	if w <= 0 {
		w = 1
	}
	if err := store.HeartbeatMember(ctx, g.pool, g.cfg.GatewayID, g.cfg.Roles, w); err != nil && ctx.Err() == nil {
		g.log.Warn("gateway: member heartbeat", "err", err)
	}
}

// MQTT exposes the transport (nil without the mqtt role).
func (g *Gateway) MQTT() *mqtt.Transport { return g.mqtt }
