package gateway

import (
	"bytes"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
)

type ringEntry struct {
	id    uuid.UUID
	at    time.Time
	frame []byte // browser message_element frame
}

// elementState is the local audience and history window of one element.
type elementState struct {
	mu       sync.Mutex
	id       uuid.UUID
	deviceID uuid.UUID
	points   int
	ring     []ringEntry
	merged   bool // TSDB history merged into ring
	devices  map[string]*deviceClient
	browsers map[string]*browserClient
}

func (s *elementState) trimLocked() {
	if over := len(s.ring) - s.points; over > 0 {
		s.ring = slices.Delete(s.ring, 0, over)
	}
}

// Hub routes element messages and presence to local sockets. State only
// exists for elements with local interest (a connected device or a subscriber).
type Hub struct {
	mu       sync.RWMutex
	elements map[uuid.UUID]*elementState
	byDevice map[uuid.UUID]map[uuid.UUID]struct{}   // device -> elements with state
	devices  map[uuid.UUID]map[string]*deviceClient // device -> sockets
	users    map[int64]map[string]*browserClient    // user -> sockets
}

func NewHub() *Hub {
	return &Hub{
		elements: map[uuid.UUID]*elementState{},
		byDevice: map[uuid.UUID]map[uuid.UUID]struct{}{},
		devices:  map[uuid.UUID]map[string]*deviceClient{},
		users:    map[int64]map[string]*browserClient{},
	}
}

func (h *Hub) get(id uuid.UUID) *elementState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.elements[id]
}

// ensure returns the element state, creating it and updating points.
func (h *Hub) ensure(id, deviceID uuid.UUID, points int) *elementState {
	h.mu.Lock()
	st, ok := h.elements[id]
	if !ok {
		st = &elementState{id: id, deviceID: deviceID, points: points,
			devices: map[string]*deviceClient{}, browsers: map[string]*browserClient{}}
		h.elements[id] = st
		if h.byDevice[deviceID] == nil {
			h.byDevice[deviceID] = map[uuid.UUID]struct{}{}
		}
		h.byDevice[deviceID][id] = struct{}{}
	}
	h.mu.Unlock()
	if ok {
		st.mu.Lock()
		st.points = points
		st.trimLocked()
		st.mu.Unlock()
	}
	return st
}

// Deliver fans a message out to every local socket in the element's audience
// except the origin socket, and appends device data to the history window.
func (h *Hub) Deliver(m *events.ElementMessage, eventID uuid.UUID, at time.Time, deviceFrame, browserFrame []byte, localOrigin string) {
	st := h.get(m.ElementID)
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if m.Source == events.SourceDevice && st.points > 0 {
		st.ring = append(st.ring, ringEntry{id: eventID, at: at, frame: browserFrame})
		st.trimLocked()
	}
	for id, d := range st.devices {
		if id != localOrigin {
			d.Send(deviceFrame)
		}
	}
	for id, b := range st.browsers {
		if id != localOrigin {
			b.Send(browserFrame)
		}
	}
}

// --- Devices ---

func (h *Hub) AddDevice(d *deviceClient, elems []elementInfo) {
	h.mu.Lock()
	if h.devices[d.deviceID] == nil {
		h.devices[d.deviceID] = map[string]*deviceClient{}
	}
	h.devices[d.deviceID][d.id] = d
	h.mu.Unlock()
	for _, e := range elems {
		h.attachDevice(d, e)
	}
}

func (h *Hub) attachDevice(d *deviceClient, e elementInfo) {
	st := h.ensure(e.ID, e.DeviceID, e.Points)
	d.Allow(e.ID)
	st.mu.Lock()
	st.devices[d.id] = d
	st.mu.Unlock()
}

func (h *Hub) RemoveDevice(d *deviceClient) {
	for _, id := range d.Elements() {
		if st := h.get(id); st != nil {
			st.mu.Lock()
			delete(st.devices, d.id)
			st.mu.Unlock()
		}
	}
	h.mu.Lock()
	delete(h.devices[d.deviceID], d.id)
	if len(h.devices[d.deviceID]) == 0 {
		delete(h.devices, d.deviceID)
	}
	h.mu.Unlock()
}

func (h *Hub) DeviceClients(deviceID uuid.UUID) []*deviceClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*deviceClient, 0, len(h.devices[deviceID]))
	for _, d := range h.devices[deviceID] {
		out = append(out, d)
	}
	return out
}

func (h *Hub) AllDeviceClients() []*deviceClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []*deviceClient
	for _, m := range h.devices {
		for _, d := range m {
			out = append(out, d)
		}
	}
	return out
}

// --- Browsers ---

func (h *Hub) AddBrowser(b *browserClient) {
	h.mu.Lock()
	if h.users[b.userID] == nil {
		h.users[b.userID] = map[string]*browserClient{}
	}
	h.users[b.userID][b.id] = b
	h.mu.Unlock()
}

func (h *Hub) RemoveBrowser(b *browserClient) {
	for _, id := range b.Subscriptions() {
		h.Unsubscribe(b, id)
	}
	h.mu.Lock()
	delete(h.users[b.userID], b.id)
	if len(h.users[b.userID]) == 0 {
		delete(h.users, b.userID)
	}
	h.mu.Unlock()
}

func (h *Hub) UserClients(userID int64) []*browserClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*browserClient, 0, len(h.users[userID]))
	for _, b := range h.users[userID] {
		out = append(out, b)
	}
	return out
}

func (h *Hub) AllBrowserClients() []*browserClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []*browserClient
	for _, m := range h.users {
		for _, b := range m {
			out = append(out, b)
		}
	}
	return out
}

// NeedsHistory reports whether the TSDB history must be merged before replay.
func (h *Hub) NeedsHistory(e elementInfo) bool {
	st := h.ensure(e.ID, e.DeviceID, e.Points)
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.merged && st.points > 0
}

// Subscribe atomically queues the confirm frame and the history replay, then
// registers the subscriber, so no live frame can overtake the replay.
func (h *Hub) Subscribe(b *browserClient, e elementInfo, confirm []byte, history []ringEntry) {
	st := h.ensure(e.ID, e.DeviceID, e.Points)
	st.mu.Lock()
	defer st.mu.Unlock()
	if history != nil && !st.merged {
		st.ring = mergeHistory(st.ring, history, st.points)
		st.merged = true
	}
	b.Send(confirm)
	for _, r := range st.ring {
		b.Send(r.frame)
	}
	st.browsers[b.id] = b
}

func (h *Hub) Unsubscribe(b *browserClient, elementID uuid.UUID) {
	if st := h.get(elementID); st != nil {
		st.mu.Lock()
		delete(st.browsers, b.id)
		st.mu.Unlock()
	}
}

// Subscribers returns local browser sockets subscribed to an element.
func (h *Hub) Subscribers(elementID uuid.UUID) []*browserClient {
	st := h.get(elementID)
	if st == nil {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]*browserClient, 0, len(st.browsers))
	for _, b := range st.browsers {
		out = append(out, b)
	}
	return out
}

// --- Control-plane changes ---

func (h *Hub) AddElement(e elementInfo) {
	for _, d := range h.DeviceClients(e.DeviceID) {
		h.attachDevice(d, e)
	}
	if st := h.get(e.ID); st != nil {
		h.ensure(e.ID, e.DeviceID, e.Points)
	}
}

func (h *Hub) UpdateElement(e elementInfo) {
	if h.get(e.ID) != nil {
		h.ensure(e.ID, e.DeviceID, e.Points)
	}
}

// RemoveElement drops all local state; subscribers get a forced unsubscribe.
func (h *Hub) RemoveElement(id uuid.UUID) {
	h.mu.Lock()
	st := h.elements[id]
	if st != nil {
		delete(h.elements, id)
		delete(h.byDevice[st.deviceID], id)
		if len(h.byDevice[st.deviceID]) == 0 {
			delete(h.byDevice, st.deviceID)
		}
	}
	h.mu.Unlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, d := range st.devices {
		d.Disallow(id)
	}
	frame := unsubscribeFrame(id.String(), "Element deleted")
	for _, b := range st.browsers {
		b.DropPerm(id)
		b.Send(frame)
	}
}

// PresenceChanged notifies subscribers of every element of the device.
func (h *Hub) PresenceChanged(deviceID uuid.UUID, connected bool) {
	h.mu.RLock()
	var states []*elementState
	for id := range h.byDevice[deviceID] {
		states = append(states, h.elements[id])
	}
	h.mu.RUnlock()
	for _, st := range states {
		frame := connStatusFrame(st.id.String(), connected)
		st.mu.Lock()
		for _, b := range st.browsers {
			b.Send(frame)
		}
		st.mu.Unlock()
	}
}

// mergeHistory unions TSDB rows with the in-memory window (dedup by event id),
// orders by time, and keeps the newest `points`.
func mergeHistory(ring, history []ringEntry, points int) []ringEntry {
	seen := make(map[uuid.UUID]struct{}, len(ring)+len(history))
	out := make([]ringEntry, 0, len(ring)+len(history))
	for _, r := range slices.Concat(history, ring) {
		if _, dup := seen[r.id]; dup {
			continue
		}
		seen[r.id] = struct{}{}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b ringEntry) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return bytes.Compare(a.id[:], b.id[:]) // UUIDv7: monotonic within a millisecond
	})
	if over := len(out) - points; over > 0 {
		out = out[over:]
	}
	return out
}

type elementInfo struct {
	ID       uuid.UUID
	DeviceID uuid.UUID
	Points   int
}
