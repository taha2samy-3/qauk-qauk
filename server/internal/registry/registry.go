// Package registry keeps every device's config (identity, key, elements and
// their rate limits) in memory, replicated from the compacted
// device-config.v1 topic. Gateways authenticate devices and check elements
// against it, so the hot path of every transport needs no database read.
//
// A device missing from memory (created a moment ago, or the topic is still
// being read) is loaded from the database once; "not found" is cached
// briefly so made-up ids can't hammer the database.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/keys"
)

// ErrNotFound means the device doesn't exist.
var ErrNotFound = errors.New("registry: device not found")

// negativeTTL is how long a "not found" answer from the database is cached.
const negativeTTL = 10 * time.Second

// Device is an immutable snapshot; a change replaces the whole value.
type Device struct {
	ID       uuid.UUID
	Name     string
	Key      *Key // nil: the device has no key and can't authenticate
	Elements []Element
	Version  int64

	byID   map[uuid.UUID]int
	byName map[string]int
}

type Key struct {
	ID        uuid.UUID
	Algorithm string
	Active    bool
	PEM       string
	Public    any // parsed once; nil if the stored PEM is unusable
}

type Element struct {
	ID     uuid.UUID
	Name   string
	Points int
	// Rate and Burst are 0 when the server default applies.
	Rate      float64
	Burst     int
	OverLimit string
}

// Element finds one of the device's elements by id.
func (d *Device) Element(id uuid.UUID) (Element, bool) {
	i, ok := d.byID[id]
	if !ok {
		return Element{}, false
	}
	return d.Elements[i], true
}

// ElementByName finds one of the device's elements by name.
func (d *Device) ElementByName(name string) (Element, bool) {
	i, ok := d.byName[name]
	if !ok {
		return Element{}, false
	}
	return d.Elements[i], true
}

// FromConfig builds a snapshot from a device-config payload.
func FromConfig(c events.DeviceConfig) *Device {
	d := &Device{ID: c.Device.ID, Name: c.Device.Name, Version: c.Version,
		Elements: make([]Element, len(c.Elements)),
		byID:     make(map[uuid.UUID]int, len(c.Elements)), byName: make(map[string]int, len(c.Elements))}
	if c.Key != nil {
		pub, err := keys.ParsePublic(c.Key.PEM)
		if err != nil {
			pub = nil
		}
		d.Key = &Key{ID: c.Key.ID, Algorithm: c.Key.Algorithm, Active: c.Key.Active, PEM: c.Key.PEM, Public: pub}
	}
	for i, e := range c.Elements {
		el := Element{ID: e.ID, Name: e.Name, Points: e.Points, OverLimit: e.OverLimit}
		if e.Rate != nil {
			el.Rate = *e.Rate
		}
		if e.Burst != nil {
			el.Burst = *e.Burst
		}
		d.Elements[i] = el
		d.byID[e.ID] = i
		if _, dup := d.byName[e.Name]; !dup { // names aren't unique: the oldest wins
			d.byName[e.Name] = i
		}
	}
	return d
}

// Loader reads one device's config from the source of truth (the database).
// It returns an error wrapping ErrNotFound if the device doesn't exist.
type Loader func(ctx context.Context, id uuid.UUID) (events.DeviceConfig, error)

type Registry struct {
	load Loader
	log  *slog.Logger

	mu      sync.RWMutex
	devices map[uuid.UUID]*Device
	missing map[uuid.UUID]time.Time // negative cache: id -> expiry

	readyOnce sync.Once
	ready     chan struct{}
	// OnChange, if set, is called after a device's snapshot changes from the
	// topic (cur is nil when the device was deleted).
	OnChange func(cur *Device)
}

func New(load Loader, log *slog.Logger) *Registry {
	return &Registry{load: load, log: log, devices: map[uuid.UUID]*Device{},
		missing: map[uuid.UUID]time.Time{}, ready: make(chan struct{})}
}

// Run follows device-config.v1 until ctx ends.
func (r *Registry) Run(ctx context.Context, brokers []string) error {
	return bus.Follow(ctx, brokers, events.TopicDeviceConfig, r.log, func(rec *kgo.Record) {
		r.ApplyRecord(rec.Key, rec.Value)
	}, r.markReady)
}

func (r *Registry) markReady() { r.readyOnce.Do(func() { close(r.ready) }) }

// Ready is closed once the topic has been read up to where it was at start.
func (r *Registry) Ready() <-chan struct{} { return r.ready }

// IsReady reports whether the initial read is done.
func (r *Registry) IsReady() bool {
	select {
	case <-r.ready:
		return true
	default:
		return false
	}
}

// ApplyRecord applies one topic record: a CloudEvent with a DeviceConfig, or
// a tombstone (nil value) for a deleted device.
func (r *Registry) ApplyRecord(key, value []byte) {
	id, err := uuid.ParseBytes(key)
	if err != nil {
		r.log.Warn("registry: bad record key", "key", string(key))
		return
	}
	if value == nil {
		r.mu.Lock()
		_, had := r.devices[id]
		delete(r.devices, id)
		r.mu.Unlock()
		if had && r.OnChange != nil {
			r.OnChange(nil)
		}
		return
	}
	var ev events.Event
	var cfg events.DeviceConfig
	if err := json.Unmarshal(value, &ev); err != nil || ev.DecodeData(&cfg) != nil || cfg.Device.ID != id {
		r.log.Warn("registry: undecodable device config", "device", id)
		return
	}
	if d := r.put(FromConfig(cfg)); d != nil && r.OnChange != nil {
		r.OnChange(d)
	}
}

// put stores d unless a newer snapshot is already there; it returns d if stored.
func (r *Registry) put(d *Device) *Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.devices[d.ID]; ok && cur.Version > d.Version {
		return nil
	}
	r.devices[d.ID] = d
	delete(r.missing, d.ID)
	return d
}

// Lookup returns a device from memory only.
func (r *Registry) Lookup(id uuid.UUID) (*Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.devices[id]
	return d, ok
}

// Get returns a device, falling back to the database on a miss.
func (r *Registry) Get(ctx context.Context, id uuid.UUID) (*Device, error) {
	r.mu.RLock()
	d, ok := r.devices[id]
	exp, neg := r.missing[id]
	r.mu.RUnlock()
	if ok {
		return d, nil
	}
	if neg && time.Now().Before(exp) {
		return nil, ErrNotFound
	}
	cfg, err := r.load(ctx, id)
	if errors.Is(err, ErrNotFound) {
		r.mu.Lock()
		if _, ok := r.devices[id]; !ok {
			r.missing[id] = time.Now().Add(negativeTTL)
			r.sweepMissingLocked()
		}
		r.mu.Unlock()
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if d := r.put(FromConfig(cfg)); d != nil {
		return d, nil
	}
	d, _ = r.Lookup(id)
	return d, nil
}

// sweepMissingLocked bounds the negative cache.
func (r *Registry) sweepMissingLocked() {
	if len(r.missing) < 10000 {
		return
	}
	now := time.Now()
	for id, exp := range r.missing {
		if now.After(exp) {
			delete(r.missing, id)
		}
	}
}

// Len returns the number of devices in memory.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.devices)
}
