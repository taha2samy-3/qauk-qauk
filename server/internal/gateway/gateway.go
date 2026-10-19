// Package gateway is the realtime data plane: device and browser WebSockets,
// in-memory fan-out, history windows, presence, and bus integration.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/gateway/mqtt"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/ratelimit"
	"github.com/taha2samy/quackquack/server/internal/registry"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// publisher sends events to the bus (a *bus.Producer; fakes in tests).
type publisher interface {
	Publish(ctx context.Context, topic string, ev *events.Event)
	PublishDurable(ctx context.Context, topic string, ev *events.Event, done func(error))
	PublishRecord(ctx context.Context, rec *kgo.Record)
}

type Gateway struct {
	cfg      *config.Config
	pool     *pgxpool.Pool
	producer publisher
	log      *slog.Logger
	hub      *Hub
	verifier *authn.DeviceVerifier
	origins  *OriginPolicy
	hist     history.Store
	replayCh chan replayReq
	registry *registry.Registry
	limits   ratelimit.Limiter
	coalesce *coalescer
	dedupe   *dedupeCache
	rest     *restPresence
	mqtt     atomic.Pointer[mqtt.Transport] // set by Run with the mqtt role
	cmds     commandHooks

	ctx      context.Context // lifetime of the gateway; parent of every socket
	ctrlCh   chan *events.Event
	seq      atomic.Uint64
	draining atomic.Bool
	drainCh  chan struct{} // closed by StartDrain: long-polls and streams end
	drainMu  sync.Once
}

// New builds a gateway; ctx bounds the lifetime of every socket it accepts.
func New(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, producer *bus.Producer, hist history.Store, log *slog.Logger) *Gateway {
	g := &Gateway{
		cfg: cfg, pool: pool, producer: producer, log: log.With("gateway_id", cfg.GatewayID),
		hist:     hist,
		replayCh: make(chan replayReq, 1024),
		hub:      NewHub(),
		origins:  NewOriginPolicy(cfg.AllowedOrigins),
		ctx:      ctx,
		ctrlCh:   make(chan *events.Event, 1024),
		drainCh:  make(chan struct{}),
	}
	g.registry = registry.New(registryLoader(pool), g.log)
	g.limits, _ = ratelimit.Open(cfg.RateLimitDriver) // validated by config.Load
	if g.limits == nil {
		g.limits = ratelimit.NewLocal()
	}
	g.coalesce = newCoalescer(g)
	g.dedupe = newDedupeCache()
	g.rest = newRESTPresence()
	g.verifier = &authn.DeviceVerifier{
		MaxLifetime: cfg.DeviceJWTMaxLifetime,
		Leeway:      30 * time.Second,
		Load:        g.loadDeviceKey,
	}
	return g
}

// Ready reports whether the device registry has caught up with its topic
// (and, with the mqtt role, the MQTT config and members are known), and the
// gateway is not in draining mode.
func (g *Gateway) Ready() bool {
	return !g.draining.Load() && g.Initialized()
}

// Initialized reports whether the gateway has completed initial synchronization,
// regardless of whether it is currently draining.
func (g *Gateway) Initialized() bool {
	t := g.mqtt.Load()
	return g.registry.IsReady() && (t == nil || t.Ready())
}

// IsDraining reports whether the gateway has entered shutdown drain mode.
func (g *Gateway) IsDraining() bool {
	return g.draining.Load()
}

// produceCtx is the context records are produced with. It isn't cancelled
// when the gateway stops: franz-go fails a buffered record whose context ends,
// and a message a device was told is accepted must still reach the bus (the
// producer's Close flushes it, within a bound).
func (g *Gateway) produceCtx() context.Context { return context.WithoutCancel(g.ctx) }

func (g *Gateway) nextConnID() string { return fmt.Sprintf("c-%06d", g.seq.Add(1)) }

// Run consumes the bus and runs background loops until ctx ends, then closes all sockets.
func (g *Gateway) Run(ctx context.Context) error {
	go g.controlWorker(ctx)
	go g.presenceLoop(ctx)
	go g.replayLoop(ctx)
	go g.warmLatest(ctx)
	go g.restPresenceLoop(ctx)
	var mqttDone <-chan struct{}
	if g.cfg.HasRole("mqtt") {
		mqttDone = g.startMQTT(ctx)
	}
	go func() {
		if err := g.registry.Run(ctx, g.cfg.KafkaBrokers); err != nil && ctx.Err() == nil {
			g.log.Error("gateway: device registry stopped", "err", err)
		}
	}()
	err := bus.Broadcast(ctx, g.cfg.KafkaBrokers,
		[]string{events.TopicElementEvents, events.TopicPresence, events.TopicControlEvents}, g.log, g.onBusEvent)
	g.shutdown()
	if mqttDone != nil {
		<-mqttDone
	}
	// messages held over their element's rate (over_limit=latest) were
	// acknowledged as coalesced: publish them before the producer closes
	g.coalesce.flushAll()
	return err
}

func (g *Gateway) shutdown() {
	for _, d := range g.hub.AllDeviceClients() {
		d.kill(websocket.StatusGoingAway, "server shutting down")
	}
	for _, b := range g.hub.AllBrowserClients() {
		b.kill(websocket.StatusGoingAway, "server shutting down")
	}
}

func (g *Gateway) onBusEvent(ctx context.Context, _ string, ev *events.Event) {
	switch ev.Type {
	case events.TypeElementMessage:
		var m events.ElementMessage
		if err := ev.DecodeData(&m); err != nil {
			g.log.Warn("gateway: bad element event", "id", ev.ID, "err", err)
			return
		}
		if m.Origin.GatewayID == g.cfg.GatewayID {
			return // already delivered via the local fast path
		}
		id, err := uuid.Parse(ev.ID)
		if err != nil {
			return
		}
		dev, br := renderMessage(&m, ev.Time, g.pipelineOf(m.DeviceID, m.ElementID))
		sent, refused := g.hub.Deliver(&m, id, ev.Time.Time, dev, br, "")
		g.trackDelivery(&m, id, sent, refused)
		g.notifyCommand(&m, id, ev.Time.Time)
	case events.TypeDevicePresence:
		var p events.DevicePresence
		if err := ev.DecodeData(&p); err != nil || p.GatewayID == g.cfg.GatewayID {
			return
		}
		g.hub.PresenceChanged(p.DeviceID, p.Connected)
	case events.TypeCommandStatus:
		var cs events.CommandStatus
		if err := ev.DecodeData(&cs); err == nil {
			g.streamCommandStatus(cs)
		}
	case events.TypeAlertChanged:
		var a events.AlertChanged
		if err := ev.DecodeData(&a); err == nil {
			g.streamAlert(a)
		}
	case events.TypeCommandFailed:
		var f events.CommandFailed
		if err := ev.DecodeData(&f); err == nil {
			g.hub.SendUserError(f.UserID, f.Code, f.Reason, f.ElementID)
		}
	case events.TypeControlChanged:
		select {
		case g.ctrlCh <- ev:
		case <-ctx.Done():
		}
	}
}

// publishElement is the single path for new element messages: deliver locally
// first (fast path), then publish to the bus for other gateways and the
// ingester. It returns the event id.
func (g *Gateway) publishElement(m *events.ElementMessage, localOrigin string) string {
	return g.publishElementDone(m, localOrigin, nil, 0)
}

// publishElementDone is publishElement with a durability callback (nil: none).
func (g *Gateway) publishElementDone(m *events.ElementMessage, localOrigin string, done func(error), pipelineVersion int) string {
	ev, err := events.New(events.TypeElementMessage, events.GatewaySource(g.cfg.GatewayID), m.ElementID.String(), m)
	if err != nil {
		g.log.Error("gateway: build event", "err", err)
		if done != nil {
			done(err)
		}
		return ""
	}
	g.publishElementEvent(ev, m, localOrigin, done, pipelineVersion)
	return ev.ID
}

// publishElementEvent delivers locally, then publishes an element event
// built by the caller (who may need its id first: tracked commands).
func (g *Gateway) publishElementEvent(ev *events.Event, m *events.ElementMessage, localOrigin string, done func(error), pipelineVersion int) {
	if strings.HasPrefix(localOrigin, viaMQTT) {
		ev.QuackVia = localOrigin
	}
	if pipelineVersion > 0 {
		ev.QuackPipeline = &pipelineVersion
	}
	dev, br := renderMessage(m, ev.Time, g.pipelineOf(m.DeviceID, m.ElementID))
	id := uuid.MustParse(ev.ID)
	sent, refused := g.hub.Deliver(m, id, ev.Time.Time, dev, br, localOrigin)
	g.trackDelivery(m, id, sent, refused)
	g.notifyCommand(m, id, ev.Time.Time)
	if done != nil {
		g.producer.PublishDurable(g.produceCtx(), events.TopicElementEvents, ev, done)
	} else {
		g.producer.Publish(g.produceCtx(), events.TopicElementEvents, ev)
	}
}

// trackDelivery reports a tracked command handed to device sockets here (or
// that the element's pipeline couldn't convert for them).
func (g *Gateway) trackDelivery(m *events.ElementMessage, id uuid.UUID, sent, refused int) {
	if !m.Track || m.Source != events.SourceUser {
		return
	}
	switch {
	case sent > 0:
		g.commandStatus(id, events.CommandStatusDelivered, "handed to a device connection")
	case refused > 0:
		g.commandStatus(id, events.CommandStatusFailed, "the element's pipeline can't convert it for the device")
	}
}

// commandStatus records what became of a tracked command (the first report
// wins) and tells its sender's streams, on every gateway. It doesn't block.
func (g *Gateway) commandStatus(id uuid.UUID, status, reason string) {
	if g.pool == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(g.produceCtx(), 10*time.Second)
		defer cancel()
		c, ok, err := store.SetCommandStatus(ctx, g.pool, id, status, reason)
		if err != nil {
			g.log.Warn("gateway: command status", "command", id, "err", err)
			return
		}
		if !ok {
			return // not tracked, or already reported
		}
		user := ""
		if c.UserID != nil {
			user = strconv.FormatInt(*c.UserID, 10)
		}
		ev, err := events.New(events.TypeCommandStatus, events.GatewaySource(g.cfg.GatewayID), c.ElementID.String(), events.CommandStatus{
			CommandID: id, ElementID: c.ElementID, UserID: user, Status: status, Reason: reason, GatewayID: g.cfg.GatewayID})
		if err == nil && g.producer != nil {
			g.producer.Publish(g.produceCtx(), events.TopicControlEvents, ev)
		}
	}()
}

// pipelineOf returns an element's pipeline with the names its steps see.
func (g *Gateway) pipelineOf(deviceID, elementID uuid.UUID) pipelineContext {
	dev, ok := g.registry.Lookup(deviceID)
	if !ok {
		return pipelineContext{}
	}
	el, ok := dev.Element(elementID)
	if !ok {
		return pipelineContext{}
	}
	return pipelineContext{pipe: el.Pipeline, element: el.Name, device: dev.Name}
}

// Device returns a device's snapshot from the in-memory registry.
func (g *Gateway) Device(id uuid.UUID) (*registry.Device, bool) { return g.registry.Lookup(id) }
