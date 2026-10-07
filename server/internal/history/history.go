// Package history is the time-series store behind element history: what the
// ingester writes, what the history API and the gateway's replay read.
//
// It is an interface with pluggable drivers (timescale, clickhouse, …), so the
// time-series database is independent of the Postgres that holds users,
// devices, elements and permissions. Drivers register themselves in init();
// Open picks one by name. Every driver must pass historytest.Run.
//
// Two shapes are stored for each event:
//   - the event itself (raw JSON message plus metadata), for replay and the
//     raw history endpoint;
//   - its numeric points, one per numeric attribute (see Points), with a
//     1-minute rollup, so any attribute (temperature, gps.lat, …) can be
//     aggregated over long ranges cheaply in every backend.
package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Source of an event.
const (
	SourceDevice = "device" // telemetry
	SourceUser   = "user"   // a command from a dashboard
)

// Event is one element message.
type Event struct {
	Time      time.Time // server receive time (ms precision)
	ID        uuid.UUID // CloudEvent id (UUIDv7); events are idempotent by ID
	ElementID uuid.UUID
	DeviceID  uuid.UUID
	Source    string
	ActorID   string
	ActorName string
	ClientTS  *time.Time      // the sender's own timestamp, for reference
	Payload   json.RawMessage // the message, unchanged
	Value     *float64        // NumericValue(Payload)
}

// Bucket is one aggregation bucket of a numeric attribute.
type Bucket struct {
	Time time.Time `json:"t"`
	Avg  *float64  `json:"avg"`
	Min  *float64  `json:"min"`
	Max  *float64  `json:"max"`
	N    int64     `json:"n"`
}

// BucketQuery aggregates one attribute of one element. Field is a path as
// produced by Points ("value", "temperature", "gps.lat", "sensors[0].temp").
type BucketQuery struct {
	ElementID uuid.UUID
	Field     string
	From, To  time.Time // [From, To)
	Step      time.Duration
}

// EventQuery reads raw events of one element in [From, To).
type EventQuery struct {
	ElementID uuid.UUID
	From, To  time.Time
	Limit     int
	// Newest returns the newest Limit events instead of the oldest ones.
	// Results are always in ascending time order.
	Newest bool
}

// Store is a time-series backend.
type Store interface {
	// Append stores events and their points. It is idempotent by Event.ID:
	// re-appending an event (at-least-once delivery) changes nothing,
	// including rollups. It returns how many events were new.
	Append(ctx context.Context, evs []Event) (int, error)
	// Last returns, per element, the newest n device events at or after
	// since, oldest first. Elements without events are absent from the map.
	Last(ctx context.Context, elementIDs []uuid.UUID, n int, since time.Time) (map[uuid.UUID][]Event, error)
	// Events returns raw events in ascending time order.
	Events(ctx context.Context, q EventQuery) ([]Event, error)
	// Buckets aggregates one attribute per Step (a multiple of a minute
	// reads the rollup). Empty buckets are omitted.
	Buckets(ctx context.Context, q BucketQuery) ([]Bucket, error)
	// Migrate creates or upgrades the schema and applies settings
	// (retention, compression). It is idempotent.
	Migrate(ctx context.Context) error
	// Reset deletes all history. Dev seeding and tests only.
	Reset(ctx context.Context) error
	Close() error
}

// ErrBadData wraps errors caused by the data itself (not an outage): the
// ingester isolates such events instead of retrying the batch forever.
var ErrBadData = errors.New("history: data rejected by the backend")

// Settings tune a driver. Zero values mean the defaults.
type Settings struct {
	Retention     time.Duration // drop data older than this (default 365 days)
	CompressAfter time.Duration // compress chunks older than this, where supported (default 7 days)
}

func (s Settings) WithDefaults() Settings {
	if s.Retention <= 0 {
		s.Retention = 365 * 24 * time.Hour
	}
	if s.CompressAfter <= 0 {
		s.CompressAfter = 7 * 24 * time.Hour
	}
	return s
}

// Factory opens a driver. url is driver-specific.
type Factory func(ctx context.Context, url string, s Settings) (Store, error)

var (
	regMu    sync.RWMutex
	registry = map[string]Factory{}
)

// Register makes a driver available to Open. It panics on duplicates.
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("history: driver registered twice: " + name)
	}
	registry[name] = f
}

// Drivers lists the registered driver names.
func Drivers() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Target is one backend to open.
type Target struct {
	Driver string
	URL    string
}

// Open opens one store, or several: with more than one target, writes go to
// all of them and reads come from the first (use it to move between
// backends: write to both, backfill with Copy, then switch).
func Open(ctx context.Context, targets []Target, s Settings) (Store, error) {
	if len(targets) == 0 {
		return nil, errors.New("history: no driver configured")
	}
	s = s.WithDefaults()
	stores := make([]Store, 0, len(targets))
	for _, t := range targets {
		regMu.RLock()
		f := registry[t.Driver]
		regMu.RUnlock()
		if f == nil {
			closeAll(stores)
			return nil, fmt.Errorf("history: unknown driver %q (available: %s)", t.Driver, strings.Join(Drivers(), ", "))
		}
		st, err := f(ctx, t.URL, s)
		if err != nil {
			closeAll(stores)
			return nil, fmt.Errorf("history: open %s: %w", t.Driver, err)
		}
		stores = append(stores, st)
	}
	if len(stores) == 1 {
		return stores[0], nil
	}
	return &multi{stores: stores}, nil
}

func closeAll(stores []Store) {
	for _, s := range stores {
		_ = s.Close()
	}
}

// multi writes to every store and reads from the first.
type multi struct{ stores []Store }

func (m *multi) Append(ctx context.Context, evs []Event) (int, error) {
	n, err := m.stores[0].Append(ctx, evs)
	if err != nil {
		return n, err
	}
	for _, s := range m.stores[1:] {
		if _, err := s.Append(ctx, evs); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (m *multi) Last(ctx context.Context, ids []uuid.UUID, n int, since time.Time) (map[uuid.UUID][]Event, error) {
	return m.stores[0].Last(ctx, ids, n, since)
}

func (m *multi) Events(ctx context.Context, q EventQuery) ([]Event, error) {
	return m.stores[0].Events(ctx, q)
}

func (m *multi) Buckets(ctx context.Context, q BucketQuery) ([]Bucket, error) {
	return m.stores[0].Buckets(ctx, q)
}

func (m *multi) Migrate(ctx context.Context) error {
	for _, s := range m.stores {
		if err := s.Migrate(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *multi) Reset(ctx context.Context) error {
	for _, s := range m.stores {
		if err := s.Reset(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *multi) Close() error {
	var errs []error
	for _, s := range m.stores {
		errs = append(errs, s.Close())
	}
	return errors.Join(errs...)
}

// ParseTargets parses QUACK_HISTORY_DRIVER / QUACK_HISTORY_URL: comma-separated
// driver names and URLs, matched by position. A missing URL falls back to
// fallbackURL (the main database, for the timescale driver).
func ParseTargets(drivers, urls, fallbackURL string) []Target {
	ds := splitTrim(drivers)
	var us []string // positional: keep empty entries
	if strings.TrimSpace(urls) != "" {
		for u := range strings.SplitSeq(urls, ",") {
			us = append(us, strings.TrimSpace(u))
		}
	}
	out := make([]Target, 0, len(ds))
	for i, d := range ds {
		u := fallbackURL
		if i < len(us) && us[i] != "" {
			u = us[i]
		}
		out = append(out, Target{Driver: d, URL: u})
	}
	return out
}

func splitTrim(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SortEvents orders events by time, then ID (UUIDv7 breaks ms ties).
func SortEvents(evs []Event) {
	slices.SortFunc(evs, func(a, b Event) int {
		if c := a.Time.Compare(b.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
}
