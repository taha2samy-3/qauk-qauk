package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/eclipse/paho.golang/paho"

	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/mqttspec"
	"github.com/taha2samy/quackquack/server/internal/sourcepipe"
)

// downlink publishes a user command for a device served over MQTT. Every
// gateway sees every command; only the owner of slot 0 of the connection
// publishes it, so a command goes out once (a rare duplicate during a
// handover is harmless: commands set values).
func (t *Transport) downlink(cmd Command) {
	type job struct {
		s      *slot
		c      *connConfig
		d      *downlink
		devExt string
	}
	var jobs []job
	t.mu.Lock()
	for _, c := range t.devIdx[cmd.DeviceID] {
		ext := c.byDevice[cmd.DeviceID]
		d, ok := c.downlinks[ext+"\x00"+cmd.Element]
		if !ok {
			continue
		}
		s := t.slots[slotKey{c.cfg.Connection.ID, 0}]
		if s == nil {
			continue // another gateway owns slot 0
		}
		jobs = append(jobs, job{s, c, d, ext})
	}
	t.mu.Unlock()
	for _, j := range jobs {
		go func() {
			if err := j.s.publishCommand(j.c, j.d, j.devExt, cmd); err != nil {
				metrics.MQTTDownlinks.WithLabelValues("error").Inc()
				t.o.Log.Warn("mqtt: downlink failed", "connection", j.c.cfg.Connection.ID, "device", j.devExt,
					"element", cmd.Element, "err", err)
				return
			}
			metrics.MQTTDownlinks.WithLabelValues("ok").Inc()
		}()
	}
}

func (s *slot) publishCommand(c *connConfig, d *downlink, devExt string, cmd Command) error {
	cm := s.cm.Load()
	if cm == nil {
		return errors.New("not connected")
	}
	topic, err := mqttspec.RenderTopic(d.spec.TopicTemplate, map[string]string{
		"device": devExt, "element": cmd.Element, "user": cmd.UserName})
	if err != nil {
		return err
	}
	payload, err := encodeCommand(c, d, devExt, cmd)
	if err != nil {
		return err
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
