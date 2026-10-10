// Package mqtt is the gateway's MQTT transport (role mqtt). Gateways connect
// as MQTT 5 clients — subscribers, never a broker — to the brokers configured
// on mqtt-config.v1. Connections are spread over the live mqtt gateways with
// weighted Rendezvous Hashing (internal/cluster): slot s of a connection is
// owned by the s-th best gateway and uses client id <prefix>-<s>, so the
// broker's session takeover fences out a previous owner, and the persistent
// session hands unacknowledged QoS 1 messages to the new one.
//
// Messages go through the source pipeline, then into the device core
// through the narrow Core interface. This package must not reach the
// database or gateway internals (enforced by depguard), so running it in its
// own deployment later is configuration only.
package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/cluster"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/metrics"
)

// Publication is one element value for the device core.
type Publication struct {
	ConnectionID string
	DeviceID     uuid.UUID
	Element      string // name on the device
	Message      json.RawMessage
	TS           *time.Time
}

// Command is a user's command for an element (any gateway sees all of them).
type Command struct {
	DeviceID  uuid.UUID
	ElementID uuid.UUID
	Element   string
	Message   json.RawMessage
	UserID    string
	UserName  string
	Time      time.Time
}

// Core is all the transport needs from the gateway.
type Core interface {
	// Publish runs a value through the device core (grants by device id are
	// the caller's). A returned error means it was rejected; otherwise done
	// is called once the value is durable on the bus, or failed.
	Publish(p Publication, done func(error)) error
	// OnCommand registers fn for every user command.
	OnCommand(fn func(Command)) (cancel func())
}

// Cluster lists the live gateways that have the mqtt role.
type Cluster interface {
	Members(ctx context.Context) ([]cluster.Member, error)
}

// Status is what a gateway reports about a slot it owns.
type Status struct {
	ConnectionID string
	Slot         int
	Connected    bool
	ReasonCode   *int
	LastError    string
	Counters     map[string]int64
}

type StatusSink interface {
	ReportStatus(ctx context.Context, s Status)
}

// RecordSink publishes diagnostic records (DLQ, capture).
type RecordSink interface {
	PublishRecord(ctx context.Context, rec *kgo.Record)
}

type Options struct {
	GatewayID        string
	Brokers          []string
	Heartbeat        time.Duration // how often members are re-read
	AllowInsecureTLS bool
	Log              *slog.Logger
	Core             Core
	Cluster          Cluster
	Status           StatusSink
	Records          RecordSink
	// Follow replaces bus.Follow (tests).
	Follow func(ctx context.Context, fn func(key, value []byte), caughtUp func()) error
}

type slotKey struct {
	conn string
	n    int
}

type Transport struct {
	o Options

	mu      sync.Mutex
	conns   map[string]*connConfig // by connection id
	members []cluster.Member
	slots   map[slotKey]*slot
	devIdx  map[uuid.UUID][]*connConfig // device → connections serving it

	wake  chan struct{}
	ready atomic.Bool
}

func New(o Options) *Transport {
	if o.Heartbeat <= 0 {
		o.Heartbeat = 10 * time.Second
	}
	if o.Follow == nil {
		o.Follow = func(ctx context.Context, fn func(key, value []byte), caughtUp func()) error {
			return bus.Follow(ctx, o.Brokers, events.TopicMQTTConfig, o.Log, func(r *kgo.Record) { fn(r.Key, r.Value) }, caughtUp)
		}
	}
	return &Transport{o: o, conns: map[string]*connConfig{}, slots: map[slotKey]*slot{},
		devIdx: map[uuid.UUID][]*connConfig{}, wake: make(chan struct{}, 1)}
}

// Ready reports whether the config topic was read and members are known.
func (t *Transport) Ready() bool { return t.ready.Load() }

// Run follows the config, keeps the owned slots connected, and serves
// downlinks, until ctx ends. Then it disconnects (sessions stay on the
// brokers for the next owner).
func (t *Transport) Run(ctx context.Context) error {
	cancelCmd := t.o.Core.OnCommand(t.downlink)
	defer cancelCmd()

	caught := make(chan struct{})
	var once sync.Once
	go func() {
		err := t.o.Follow(ctx, t.applyRecord, func() { once.Do(func() { close(caught) }) })
		if err != nil && ctx.Err() == nil {
			t.o.Log.Error("mqtt: config follow stopped", "err", err)
		}
	}()
	select {
	case <-caught:
	case <-ctx.Done():
		return nil
	}
	tick := time.NewTicker(t.o.Heartbeat)
	defer tick.Stop()
	for {
		t.refreshMembers(ctx)
		t.reconcile(ctx)
		t.ready.Store(true)
		select {
		case <-ctx.Done():
			t.stopAll()
			return nil
		case <-tick.C:
			t.reportAll(ctx)
		case <-t.wake:
		}
	}
}

func (t *Transport) poke() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// applyRecord applies one mqtt-config.v1 record (nil value = tombstone).
func (t *Transport) applyRecord(key, value []byte) {
	id := string(key)
	if value == nil {
		t.mu.Lock()
		delete(t.conns, id)
		t.rebuildIndexLocked()
		t.mu.Unlock()
		t.poke()
		return
	}
	var ev events.Event
	var cfg events.MQTTConfig
	if err := json.Unmarshal(value, &ev); err != nil || ev.DecodeData(&cfg) != nil || cfg.Connection.ID != id {
		t.o.Log.Warn("mqtt: undecodable config", "connection", id)
		return
	}
	cc := compile(cfg, t.o.Log)
	t.mu.Lock()
	if cur, ok := t.conns[id]; ok && cur.cfg.Version > cfg.Version {
		t.mu.Unlock()
		return
	}
	t.conns[id] = cc
	t.rebuildIndexLocked()
	for k, s := range t.slots { // running slots pick up new rules at once
		if k.conn == id {
			s.setConfig(cc)
		}
	}
	t.mu.Unlock()
	t.poke()
}

func (t *Transport) rebuildIndexLocked() {
	idx := map[uuid.UUID][]*connConfig{}
	for _, c := range t.conns {
		for dev := range c.byDevice {
			idx[dev] = append(idx[dev], c)
		}
	}
	t.devIdx = idx
}

func (t *Transport) refreshMembers(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ms, err := t.o.Cluster.Members(cctx)
	if err != nil {
		t.o.Log.Warn("mqtt: members", "err", err)
		return // keep the last known members
	}
	t.mu.Lock()
	t.members = ms
	t.mu.Unlock()
}

// reconcile starts the slots this gateway owns and stops the others.
func (t *Transport) reconcile(ctx context.Context) {
	t.mu.Lock()
	want := map[slotKey]*connConfig{}
	for id, c := range t.conns {
		if !c.cfg.Connection.Enabled {
			continue
		}
		owners := cluster.Owners(id, t.members, max(c.cfg.Connection.Replicas, 1))
		for n, gw := range owners {
			if gw == t.o.GatewayID {
				want[slotKey{id, n}] = c
			}
		}
	}
	var stop []*slot
	for k, s := range t.slots {
		c, ok := want[k]
		if !ok || c.connHash != s.connHash {
			stop = append(stop, s)
			delete(t.slots, k)
		}
	}
	var start []*slot
	for k, c := range want {
		if _, ok := t.slots[k]; !ok {
			s := newSlot(t, k, c)
			t.slots[k] = s
			start = append(start, s)
		}
	}
	metrics.MQTTOwnedSlots.Set(float64(len(t.slots)))
	t.mu.Unlock()
	for _, s := range stop {
		t.o.Log.Info("mqtt: releasing slot", "connection", s.key.conn, "slot", s.key.n)
		s.stop()
	}
	for _, s := range start {
		t.o.Log.Info("mqtt: taking slot", "connection", s.key.conn, "slot", s.key.n)
		s.start(ctx)
	}
}

func (t *Transport) stopAll() {
	t.mu.Lock()
	all := make([]*slot, 0, len(t.slots))
	for k, s := range t.slots {
		all = append(all, s)
		delete(t.slots, k)
	}
	t.mu.Unlock()
	for _, s := range all {
		s.stop()
	}
	metrics.MQTTOwnedSlots.Set(0)
}

func (t *Transport) reportAll(ctx context.Context) {
	t.mu.Lock()
	slots := make([]*slot, 0, len(t.slots))
	for _, s := range t.slots {
		slots = append(slots, s)
	}
	t.mu.Unlock()
	for _, s := range slots {
		s.report(ctx)
	}
}

// OwnedSlots lists the slots this gateway owns (tests, admin).
func (t *Transport) OwnedSlots() map[string][]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string][]int{}
	for k := range t.slots {
		out[k.conn] = append(out[k.conn], k.n)
	}
	return out
}

// Connections returns the ids of the connections this gateway knows.
func (t *Transport) Connections() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return sortedKeys(t.conns)
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range maps.Keys(m) {
		ks = append(ks, k)
	}
	return ks
}
