package gateway

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
)

// Replay hydration: when a dashboard subscribes to an element this gateway
// has no window for (first subscriber, after a restart), the newest stored
// messages are read from the history store. Requests arriving within a few
// milliseconds are batched into one Store.Last call, so opening a dashboard
// with 50 widgets, or a reconnect storm after a deploy, costs a handful of
// queries instead of one per element and subscriber.

const (
	replayBatchWait  = 5 * time.Millisecond
	replayBatchMax   = 200
	replayConcurrent = 4
	replayTimeout    = 10 * time.Second
)

type replayReq struct {
	id  uuid.UUID
	n   int
	out chan replayRes
}

type replayRes struct {
	rows []history.Event
	err  error
}

func (g *Gateway) replayLoop(ctx context.Context) {
	sem := make(chan struct{}, replayConcurrent)
	for {
		var batch []replayReq
		select {
		case <-ctx.Done():
			return
		case r := <-g.replayCh:
			batch = append(batch, r)
		}
		timer := time.NewTimer(replayBatchWait)
	collect:
		for len(batch) < replayBatchMax {
			select {
			case r := <-g.replayCh:
				batch = append(batch, r)
			case <-timer.C:
				break collect
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func() {
			defer func() { <-sem }()
			g.runReplay(ctx, batch)
		}()
	}
}

func (g *Gateway) runReplay(ctx context.Context, batch []replayReq) {
	ids := make([]uuid.UUID, 0, len(batch))
	seen := map[uuid.UUID]bool{}
	n := 1
	for _, r := range batch {
		if !seen[r.id] {
			seen[r.id] = true
			ids = append(ids, r.id)
		}
		n = max(n, r.n)
	}
	qctx, cancel := context.WithTimeout(ctx, replayTimeout)
	defer cancel()
	res, err := g.hist.Last(qctx, ids, n, time.Now().Add(-g.cfg.HistoryReplayWindow))
	for _, r := range batch {
		rows := res[r.id]
		if over := len(rows) - r.n; over > 0 {
			rows = rows[over:]
		}
		r.out <- replayRes{rows: rows, err: err}
	}
}

// loadHistory reads the newest stored device messages of an element
// (max(points, 1): the latest value is replayed even with points = 0).
// ok is false if the history store couldn't answer.
func (g *Gateway) loadHistory(ctx context.Context, el elementInfo) ([]ringEntry, bool) {
	req := replayReq{id: el.ID, n: max(el.Points, 1), out: make(chan replayRes, 1)}
	select {
	case g.replayCh <- req:
	case <-ctx.Done():
		return nil, false
	}
	var res replayRes
	select {
	case res = <-req.out:
	case <-ctx.Done():
		return nil, false
	}
	if res.err != nil {
		g.log.Warn("gateway: history hydrate failed", "element", el.ID, "err", res.err)
		return nil, false
	}
	out := make([]ringEntry, 0, len(res.rows))
	for _, r := range res.rows {
		out = append(out, entryFromEvent(r))
	}
	return out, true
}

func entryFromEvent(r history.Event) ringEntry {
	m := &events.ElementMessage{ElementID: r.ElementID, DeviceID: r.DeviceID, Source: r.Source,
		Actor: events.Actor{ID: r.ActorID, Name: r.ActorName}, Message: r.Payload}
	_, br := renderMessage(m, events.Time{Time: r.Time}, nil)
	var parsed map[string]any
	_ = json.Unmarshal(r.Payload, &parsed)
	return ringEntry{id: r.ID, at: r.Time, frame: br, message: parsed}
}

// warmLatest loads the latest stored value of every element from the
// compacted element-state topic, so the first subscriber of an element gets
// a value even before any history is read (and while the history store is
// unavailable).
func (g *Gateway) warmLatest(ctx context.Context) {
	start := time.Now()
	n := 0
	err := bus.ReadAll(ctx, g.cfg.KafkaBrokers, events.TopicElementState, func(r *kgo.Record) {
		var ev events.Event
		if json.Unmarshal(r.Value, &ev) != nil || ev.Type != events.TypeElementMessage {
			return
		}
		var m events.ElementMessage
		id, err := uuid.Parse(ev.ID)
		if err != nil || ev.DecodeData(&m) != nil {
			return
		}
		_, br := renderMessage(&m, ev.Time, nil)
		var parsed map[string]any
		_ = json.Unmarshal(m.Message, &parsed)
		g.hub.Remember(m.ElementID, ringEntry{id: id, at: ev.Time.Time, frame: br, message: parsed})
		n++
	})
	if err != nil && ctx.Err() == nil {
		g.log.Warn("gateway: warm-up from element-state failed", "err", err)
		return
	}
	g.log.Info("gateway: latest values loaded", "elements", n, "took", time.Since(start).Round(time.Millisecond))
}
