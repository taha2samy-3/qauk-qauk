package gateway

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
)

type ringEntry struct {
	id      uuid.UUID
	at      time.Time
	frame   []byte // browser message_element frame
	message map[string]any
}

// elementState is the local audience and history window of one element.
type elementState struct {
	mu       sync.Mutex
	id       uuid.UUID
	deviceID uuid.UUID
	points   int
	ring     []ringEntry
	merged   bool // stored history merged into ring
	dropped  bool // removed from the hub (no audience left): callers must re-ensure
	devices  map[string]*deviceClient
	browsers map[string]*browserClient
}

func (s *elementState) trimLocked() {
	if over := len(s.ring) - s.points; over > 0 {
		s.ring = slices.Delete(s.ring, 0, over)
	}
}

// Hub routes element messages and presence to local sockets. State only
// exists for elements with local interest (a connected device or a
// subscriber) and is freed when the last one leaves. Separately, the hub
// remembers the latest device message of every element it has seen (one
// frame each), so a new subscriber gets a value at once.
//
// Lock order: h.mu before st.mu; h.lmu is independent.
type Hub struct {
	mu       sync.RWMutex
	elements map[uuid.UUID]*elementState
	byDevice map[uuid.UUID]map[uuid.UUID]struct{}   // device -> elements with state
	devices  map[uuid.UUID]map[string]*deviceClient // device -> sockets
	users    map[int64]map[string]*browserClient    // user -> sockets

	lmu    sync.RWMutex
	latest map[uuid.UUID]ringEntry // element -> newest device message

	// Commands for connectionless devices (REST sync): the newest message per
	// element written by someone other than its device, as a device frame,
	// and the requests waiting for one.
	cmu     sync.Mutex
	cmds    map[uuid.UUID]ringEntry
	waiters map[uuid.UUID]map[chan struct{}]struct{} // device -> waiters
}

func NewHub() *Hub {
	return &Hub{
		elements: map[uuid.UUID]*elementState{},
		byDevice: map[uuid.UUID]map[uuid.UUID]struct{}{},
		devices:  map[uuid.UUID]map[string]*deviceClient{},
		users:    map[int64]map[string]*browserClient{},
		latest:   map[uuid.UUID]ringEntry{},
		cmds:     map[uuid.UUID]ringEntry{},
		waiters:  map[uuid.UUID]map[chan struct{}]struct{}{},
	}
}

func newer(a, b ringEntry) bool {
	if c := a.at.Compare(b.at); c != 0 {
		return c > 0
	}
	return bytes.Compare(a.id[:], b.id[:]) > 0
}

// Remember records an element's device message if it is the newest seen.
func (h *Hub) Remember(elementID uuid.UUID, e ringEntry) {
	h.lmu.Lock()
	if cur, ok := h.latest[elementID]; !ok || newer(e, cur) {
		h.latest[elementID] = e
	}
	h.lmu.Unlock()
}

// Latest returns the newest device message seen for an element.
func (h *Hub) Latest(elementID uuid.UUID) (ringEntry, bool) {
	h.lmu.RLock()
	defer h.lmu.RUnlock()
	e, ok := h.latest[elementID]
	return e, ok
}

// LatestMessage returns the newest decoded device message map, timestamp, and true,
// or nil, zero time, false if no message has been seen.
func (h *Hub) LatestMessage(elementID uuid.UUID) (map[string]any, time.Time, bool) {
	h.lmu.RLock()
	defer h.lmu.RUnlock()
	e, ok := h.latest[elementID]
	if !ok {
		return nil, time.Time{}, false
	}
	return e.message, e.at, true
}

func (h *Hub) forget(elementID uuid.UUID) {
	h.lmu.Lock()
	delete(h.latest, elementID)
	h.lmu.Unlock()
}

// lockLive returns the element's state, locked and still registered.
func (h *Hub) lockLive(e elementInfo) *elementState {
	for {
		st := h.ensure(e.ID, e.DeviceID, e.Points)
		st.mu.Lock()
		if !st.dropped {
			return st
		}
		st.mu.Unlock()
	}
}

// maybeDrop frees an element's state once no device or browser uses it.
func (h *Hub) maybeDrop(st *elementState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.dropped || len(st.devices) > 0 || len(st.browsers) > 0 {
		return
	}
	st.dropped = true
	if h.elements[st.id] == st {
		delete(h.elements, st.id)
		delete(h.byDevice[st.deviceID], st.id)
		if len(h.byDevice[st.deviceID]) == 0 {
			delete(h.byDevice, st.deviceID)
		}
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
	if m.Source == events.SourceDevice {
		var parsed map[string]any
		_ = json.Unmarshal(m.Message, &parsed)
		h.Remember(m.ElementID, ringEntry{id: eventID, at: at, frame: browserFrame, message: parsed})
	} else {
		h.rememberCommand(m.DeviceID, m.ElementID, ringEntry{id: eventID, at: at, frame: deviceFrame})
	}
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
	d.Allow(e.ID)
	st := h.lockLive(e)
	st.devices[d.id] = d
	st.mu.Unlock()
}

func (h *Hub) RemoveDevice(d *deviceClient) {
	for _, id := range d.Elements() {
		if st := h.get(id); st != nil {
			st.mu.Lock()
			delete(st.devices, d.id)
			st.mu.Unlock()
			h.maybeDrop(st)
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

// SendUserError routes an error frame to all connected browser clients of the given user.
// If the user has disconnected or has no active sockets, it safely returns without panicking.
func (h *Hub) SendUserError(userIDStr string, code, description string, elementID uuid.UUID) {
	uid, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return
	}
	h.mu.RLock()
	m, ok := h.users[uid]
	if !ok || len(m) == 0 {
		h.mu.RUnlock()
		return
	}
	clients := make([]*browserClient, 0, len(m))
	for _, b := range m {
		clients = append(clients, b)
	}
	h.mu.RUnlock()

	var elStr string
	if elementID != uuid.Nil {
		elStr = elementID.String()
	}
	frame := errorFrame(code, description, elStr)
	for _, b := range clients {
		b.Send(frame)
	}
}

// NeedsHistory reports whether stored history must be loaded before replay.
// It never creates state: Subscribe does, once the subscriber is known.
func (h *Hub) NeedsHistory(e elementInfo) bool {
	st := h.get(e.ID)
	if st == nil {
		return true
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.merged || st.dropped
}

// Subscribe atomically queues the confirm frame and the replay, then
// registers the subscriber, so no live frame can overtake the replay.
// loaded reports whether stored history was read successfully; on failure
// the element is re-hydrated by a later subscribe.
//
// The replay is the element's window (`points` newest device messages). An
// element with points = 0 still gets its latest value, so value widgets
// don't stay empty until the device sends again.
func (h *Hub) Subscribe(b *browserClient, e elementInfo, confirm []byte, stored []ringEntry, loaded bool) {
	st := h.lockLive(e)
	defer st.mu.Unlock()
	if loaded && !st.merged {
		if st.points > 0 {
			st.ring = mergeHistory(st.ring, stored, st.points)
		}
		if n := len(stored); n > 0 {
			h.Remember(e.ID, stored[n-1])
		}
		st.merged = true
	}
	latest, hasLatest := h.Latest(e.ID)
	if hasLatest && st.points > 0 {
		// the newest message seen on the bus may not be stored yet (ingest lag)
		st.ring = mergeHistory(st.ring, []ringEntry{latest}, st.points)
	}
	b.Send(confirm)
	switch {
	case len(st.ring) > 0:
		for _, r := range st.ring {
			b.Send(r.frame)
		}
	case hasLatest:
		b.Send(latest.frame)
	}
	st.browsers[b.id] = b
}

func (h *Hub) Unsubscribe(b *browserClient, elementID uuid.UUID) {
	if st := h.get(elementID); st != nil {
		st.mu.Lock()
		delete(st.browsers, b.id)
		st.mu.Unlock()
		h.maybeDrop(st)
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
	h.forget(id)
	h.cmu.Lock()
	delete(h.cmds, id)
	h.cmu.Unlock()
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
	st.dropped = true
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

// --- Commands for connectionless devices ---

func (h *Hub) rememberCommand(deviceID, elementID uuid.UUID, e ringEntry) {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	if cur, ok := h.cmds[elementID]; ok && !newer(e, cur) {
		return
	}
	h.cmds[elementID] = e
	for w := range h.waiters[deviceID] {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// CommandsSince returns, for each element, the newest command newer than
// the cursor's event id for that element (UUIDv7 ids order by time).
func (h *Hub) CommandsSince(elements []uuid.UUID, cursor map[uuid.UUID]uuid.UUID) map[uuid.UUID]ringEntry {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	out := map[uuid.UUID]ringEntry{}
	for _, id := range elements {
		e, ok := h.cmds[id]
		if !ok {
			continue
		}
		if seen, ok := cursor[id]; ok && bytes.Compare(e.id[:], seen[:]) <= 0 {
			continue
		}
		out[id] = e
	}
	return out
}

// WaitCommands returns a channel signalled when a command for one of the
// device's elements arrives; call the cancel function when done.
func (h *Hub) WaitCommands(deviceID uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.cmu.Lock()
	if h.waiters[deviceID] == nil {
		h.waiters[deviceID] = map[chan struct{}]struct{}{}
	}
	h.waiters[deviceID][ch] = struct{}{}
	h.cmu.Unlock()
	return ch, func() {
		h.cmu.Lock()
		delete(h.waiters[deviceID], ch)
		if len(h.waiters[deviceID]) == 0 {
			delete(h.waiters, deviceID)
		}
		h.cmu.Unlock()
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
