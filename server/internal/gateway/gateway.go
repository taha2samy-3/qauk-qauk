// Package gateway is the realtime data plane: device and browser WebSockets,
// in-memory fan-out, history windows, presence, and bus integration.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/gateway/mqtt"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/ratelimit"
	"github.com/taha2samy/quackquack/server/internal/registry"
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
	mqtt     *mqtt.Transport
	cmds     commandHooks

	ctx    context.Context // lifetime of the gateway; parent of every socket
	ctrlCh chan *events.Event
	seq    atomic.Uint64
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
// (and, with the mqtt role, the MQTT config and members are known).
func (g *Gateway) Ready() bool {
	return g.registry.IsReady() && (g.mqtt == nil || g.mqtt.Ready())
}

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
		var pipe *elementpipe.Pipeline
		if dev, ok := g.registry.Lookup(m.DeviceID); ok {
			if el, ok := dev.Element(m.ElementID); ok {
				pipe = el.Pipeline
			}
		}
		dev, br := renderMessage(&m, ev.Time, pipe)
		g.hub.Deliver(&m, id, ev.Time.Time, dev, br, "")
		g.notifyCommand(&m, id, ev.Time.Time)
	case events.TypeDevicePresence:
		var p events.DevicePresence
		if err := ev.DecodeData(&p); err != nil || p.GatewayID == g.cfg.GatewayID {
			return
		}
		g.hub.PresenceChanged(p.DeviceID, p.Connected)
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
	if strings.HasPrefix(localOrigin, viaMQTT) {
		ev.QuackVia = localOrigin
	}
	if pipelineVersion > 0 {
		ev.QuackPipeline = &pipelineVersion
	}
	var pipe *elementpipe.Pipeline
	if dev, ok := g.registry.Lookup(m.DeviceID); ok {
		if el, ok := dev.Element(m.ElementID); ok {
			pipe = el.Pipeline
		}
	}
	dev, br := renderMessage(m, ev.Time, pipe)
	id := uuid.MustParse(ev.ID)
	g.hub.Deliver(m, id, ev.Time.Time, dev, br, localOrigin)
	g.notifyCommand(m, id, ev.Time.Time)
	if done != nil {
		g.producer.PublishDurable(g.ctx, events.TopicElementEvents, ev, done)
	} else {
		g.producer.Publish(g.ctx, events.TopicElementEvents, ev)
	}
	return ev.ID
}

// Device returns a device's snapshot from the in-memory registry.
func (g *Gateway) Device(id uuid.UUID) (*registry.Device, bool) { return g.registry.Lookup(id) }
