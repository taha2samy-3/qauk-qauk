package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/paho"

	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/mqttspec"
	"github.com/taha2samy/quackquack/server/internal/sourcepipe"
)

const (
	defaultMaxRetries     = 3
	defaultInitialBackoff = 50 * time.Millisecond
	defaultMaxBackoff     = 500 * time.Millisecond
	maxInFlightDownlinks  = 1000
)

// downlinkKey is one element of one device on one connection. Its commands
// go out one at a time, and only the newest waiting one is kept: a command
// that is still being retried when a newer one arrives is dropped, so an old
// ON can't land after a newer OFF.
type downlinkKey struct{ conn, dev, element string }

type downlinkQueue struct {
	mu      sync.Mutex
	pending map[downlinkKey]Command
	running map[downlinkKey]bool
}

// permanentError is a downlink failure a retry can't fix (topic template,
// encoder).
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// downlink publishes a user command for a device served over MQTT. Every
// gateway sees every command; only the owner of slot 0 of the connection
// publishes it, so a command goes out once (a rare duplicate during a
// handover is harmless: commands set values).
func (t *Transport) downlink(cmd Command) {
	var keys []downlinkKey
	t.mu.Lock()
	for _, c := range t.devIdx[cmd.DeviceID] {
		ext := c.byDevice[cmd.DeviceID]
		if _, ok := c.downlinks[ext+"\x00"+cmd.Element]; !ok {
			continue
		}
		if t.slots[slotKey{c.cfg.Connection.ID, 0}] == nil {
			continue // another gateway owns slot 0
		}
		keys = append(keys, downlinkKey{c.cfg.Connection.ID, ext, cmd.Element})
	}
	t.mu.Unlock()

	for _, k := range keys {
		q := &t.dl
		q.mu.Lock()
		if _, waiting := q.pending[k]; waiting {
			metrics.MQTTDownlinks.WithLabelValues("superseded").Inc()
		}
		q.pending[k] = cmd
		if q.running[k] {
			q.mu.Unlock()
			continue
		}
		if t.inFlight.Load() >= maxInFlightDownlinks {
			delete(q.pending, k)
			q.mu.Unlock()
			metrics.MQTTDownlinks.WithLabelValues("dropped_capacity").Inc()
			t.o.Log.Warn("mqtt: too many downlinks in flight", "connection", k.conn, "device", k.dev, "element", k.element)
			if cmd.UserID != "" {
				t.o.Core.SendUserError(cmd.UserID, "delivery_failed", "MQTT downlink buffer full", cmd.ElementID)
			}
			continue
		}
		q.running[k] = true
		t.inFlight.Add(1)
		q.mu.Unlock()
		go t.drainDownlinks(k)
	}
}

// drainDownlinks sends the newest waiting command of k until none is left.
func (t *Transport) drainDownlinks(k downlinkKey) {
	defer t.inFlight.Add(-1)
	for {
		t.dl.mu.Lock()
		cmd, ok := t.dl.pending[k]
		if !ok {
			delete(t.dl.running, k)
			t.dl.mu.Unlock()
			return
		}
		delete(t.dl.pending, k)
		t.dl.mu.Unlock()
		t.deliverDownlink(k, cmd)
	}
}

func (t *Transport) superseded(k downlinkKey) bool {
	t.dl.mu.Lock()
	defer t.dl.mu.Unlock()
	_, ok := t.dl.pending[k]
	return ok
}

func (t *Transport) deliverDownlink(k downlinkKey, cmd Command) {
	maxRetries := t.o.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	backoff := t.o.RetryInitialBackoff
	if backoff <= 0 {
		backoff = defaultInitialBackoff
	}
	maxBackoff := t.o.RetryMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	ctx := t.runContext()
	fail := func(reason string) {
		metrics.MQTTDownlinks.WithLabelValues("error").Inc()
		t.o.Log.Warn("mqtt: downlink failed", "connection", k.conn, "device", k.dev, "element", k.element, "reason", reason)
		if cmd.UserID != "" {
			t.o.Core.SendUserError(cmd.UserID, "delivery_failed", reason, cmd.ElementID)
		}
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		t.mu.Lock()
		c := t.conns[k.conn]
		var s *slot
		var d *downlink
		if c != nil {
			s = t.slots[slotKey{k.conn, 0}]
			d = c.downlinks[k.dev+"\x00"+k.element]
		}
		t.mu.Unlock()
		if c == nil || d == nil {
			fail("the MQTT downlink for this element was removed")
			return
		}
		if s == nil {
			lastErr = errors.New("this gateway no longer owns the connection's slot 0")
		} else if lastErr = s.publishCommand(c, d, k.dev, cmd); lastErr == nil {
			metrics.MQTTDownlinks.WithLabelValues("ok").Inc()
			return
		}
		var perm permanentError
		if errors.As(lastErr, &perm) {
			fail("the MQTT downlink can't be built: " + perm.Error())
			return
		}
		if attempt == maxRetries {
			break
		}
		select {
		case <-time.After(min(backoff<<attempt, maxBackoff)):
		case <-ctx.Done():
			return // shutting down: the next owner of the slot takes over
		}
		if t.superseded(k) {
			metrics.MQTTDownlinks.WithLabelValues("superseded").Inc()
			return // a newer command for this element is waiting
		}
	}
	fail(fmt.Sprintf("MQTT downlink not delivered after %d attempts: %v", maxRetries+1, lastErr))
}

func (s *slot) publishCommand(c *connConfig, d *downlink, devExt string, cmd Command) error {
	topic, err := mqttspec.RenderTopic(d.spec.TopicTemplate, map[string]string{
		"device": devExt, "element": cmd.Element, "user": cmd.UserName})
	if err != nil {
		return permanentError{fmt.Errorf("topic: %w", err)}
	}
	payload, err := encodeCommand(c, d, devExt, cmd)
	if err != nil {
		return permanentError{fmt.Errorf("payload: %w", err)}
	}
	cm := s.cm.Load()
	if cm == nil {
		return errors.New("not connected to the broker")
	}
	pub := &paho.Publish{Topic: topic, QoS: byte(d.spec.QoS), Retain: d.spec.Retain, Payload: payload,
		Properties: &paho.PublishProperties{}}
	if d.spec.ContentType != nil {
		pub.Properties.ContentType = *d.spec.ContentType
	}
	if d.spec.ResponseTopic != nil {
		pub.Properties.ResponseTopic = *d.spec.ResponseTopic
	}
	if d.spec.MessageExpiry != nil && *d.spec.MessageExpiry > 0 {
		e := uint32(*d.spec.MessageExpiry)
		pub.Properties.MessageExpiry = &e
	}
	var props map[string]string
	if len(d.spec.UserProperties) > 0 && json.Unmarshal(d.spec.UserProperties, &props) == nil {
		for k, v := range props {
			pub.Properties.User = append(pub.Properties.User, paho.UserProperty{Key: k, Value: v})
		}
	}
	pub.Properties.User = append(pub.Properties.User, paho.UserProperty{Key: "quack-user", Value: cmd.UserName})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = cm.Publish(ctx, pub)
	return err
}

// encodeCommand builds the payload: encodeDownlink, a JSON template, or the
// message as is.
func encodeCommand(c *connConfig, d *downlink, devExt string, cmd Command) ([]byte, error) {
	var msg any
	if err := json.Unmarshal(cmd.Message, &msg); err != nil {
		return nil, err
	}
	switch {
	case d.encoder.DecoderID != "":
		dec := c.decoders[d.encoder.DecoderID]
		if dec == nil {
			return nil, errors.New("encoder decoder missing from the snapshot")
		}
		return dec.Encode(sourcepipe.DownlinkInput{Device: devExt, Element: cmd.Element, Data: msg})
	case len(d.encoder.Template) > 0:
		var tpl any
		if err := json.Unmarshal(d.encoder.Template, &tpl); err != nil {
			return nil, err
		}
		value := msg
		if m, ok := msg.(map[string]any); ok {
			if v, ok := m["value"]; ok {
				value = v
			}
		}
		return json.Marshal(fillTemplate(tpl, value, msg))
	}
	return cmd.Message, nil
}

// fillTemplate replaces the strings "{{value}}" and "{{message}}" anywhere in a template.
func fillTemplate(v, value, msg any) any {
	switch x := v.(type) {
	case string:
		switch x {
		case "{{value}}":
			return value
		case "{{message}}":
			return msg
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = fillTemplate(e, value, msg)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fillTemplate(e, value, msg)
		}
		return out
	}
	return v
}
