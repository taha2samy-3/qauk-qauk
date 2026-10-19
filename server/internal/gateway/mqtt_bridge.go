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
	"github.com/taha2samy/quackquack/server/internal/metrics"
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

// notifyCommand passes users' commands to the MQTT transport (downlinks), in
// device units. Commands were checked against the pipeline where they were
// sent (browserPublish); one that still can't be converted here (a gateway on
// another pipeline version) is not sent.
func (g *Gateway) notifyCommand(m *events.ElementMessage, id uuid.UUID, at time.Time) {
	if m.Source == events.SourceDevice {
		return
	}
	g.cmds.mu.RLock()
	defer g.cmds.mu.RUnlock()
	if len(g.cmds.hooks) == 0 {
		return
	}
	pc := g.pipelineOf(m.DeviceID, m.ElementID)
	msg := m.Message
	if pc.pipe.Invertible() {
		inv, err := inverseMessage(msg, pc.pipe, elementpipe.Context{Element: pc.element, Device: pc.device, Time: at})
		if err != nil {
			metrics.PipelineFailed.WithLabelValues("inverse").Inc()
			g.log.Warn("gateway: command not converted for the device", "element", m.ElementID, "err", err)
			return
		}
		msg = inv
	}
	cmd := mqtt.Command{ID: id, Track: m.Track, DeviceID: m.DeviceID, ElementID: m.ElementID, Element: pc.element, Message: msg,
		UserID: m.Actor.ID, UserName: m.Actor.Name, Time: at}
	for _, h := range g.cmds.hooks {
		h(cmd)
	}
}

// commandFailed tells a user's dashboards, on whichever gateway they are
// connected to, that a command was not delivered: it goes over
// control-events.v1 to every gateway (this one included).
func (g *Gateway) commandFailed(userID, code, reason string, elementID uuid.UUID) {
	ev, err := events.New(events.TypeCommandFailed, events.GatewaySource(g.cfg.GatewayID), elementID.String(),
		events.CommandFailed{ElementID: elementID, UserID: userID, Code: code, Reason: reason, GatewayID: g.cfg.GatewayID})
	if err != nil || g.producer == nil {
		g.hub.SendUserError(userID, code, reason, elementID)
		return
	}
	g.producer.Publish(g.produceCtx(), events.TopicControlEvents, ev)
}

// mqttCore is the gateway as seen by the MQTT transport.
type mqttCore struct{ g *Gateway }

func (c mqttCore) Publish(p mqtt.Publication, done func(mqtt.Outcome, error)) error {
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
	// Done can run before publishDeviceMessage returns (at once for values
	// that aren't published, or for a fast bus ack): the outcome is only
	// known from its result, so an early call is reported after it returns.
	var mu sync.Mutex
	returned, early := false, false
	var earlyErr error
	res, err := g.publishDeviceMessage(dev, viaMQTT+p.ConnectionID, DeviceMessage{Element: p.Element, ByName: true,
		Message: p.Message, ClientTS: p.TS, Done: func(e error) {
			mu.Lock()
			if !returned {
				early, earlyErr = true, e
				mu.Unlock()
				return
			}
			mu.Unlock()
			done(mqtt.Published, e)
		}})
	mu.Lock()
	returned = true
	wasEarly, e := early, earlyErr
	mu.Unlock()
	if err != nil {
		return err
	}
	if wasEarly {
		done(outcomeOf(res.Status), e)
	}
	return nil
}

func outcomeOf(status string) mqtt.Outcome {
	switch status {
	case StatusFiltered:
		return mqtt.Filtered
	case StatusDuplicate:
		return mqtt.Duplicate
	case StatusCoalesced:
		return mqtt.Coalesced
	}
	return mqtt.Published
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

func (c mqttCore) CommandStatus(id uuid.UUID, status, reason string) {
	c.g.commandStatus(id, status, reason)
}

func (c mqttCore) SendUserError(userID string, code, description string, elementID uuid.UUID) {
	c.g.commandFailed(userID, code, description, elementID)
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
	t := mqtt.New(mqtt.Options{
		GatewayID: g.cfg.GatewayID, Brokers: g.cfg.KafkaBrokers, Heartbeat: g.cfg.PresenceHeartbeat,
		AllowInsecureTLS: g.cfg.AllowInsecureTLS, Log: g.log.With("transport", "mqtt"),
		Core: mqttCore{g}, Cluster: mqttCluster{g}, Status: mqttStatus{g}, Records: g.producer,
	})
	g.mqtt.Store(t)
	g.heartbeatMember(ctx) // be a member before the first assignment
	go func() {
		defer close(done)
		if err := t.Run(ctx); err != nil && ctx.Err() == nil {
			g.log.Error("gateway: mqtt transport stopped", "err", err)
		}
		dctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = store.DeleteMember(dctx, g.pool, g.cfg.GatewayID) // leave at once on shutdown
	}()
	return done
}

func (g *Gateway) heartbeatMember(ctx context.Context) {
	if g.mqtt.Load() == nil {
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
func (g *Gateway) MQTT() *mqtt.Transport { return g.mqtt.Load() }
