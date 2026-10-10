package sourcepipe

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/mqttspec"
)

// Rule is a compiled uplink rule.
type Rule struct {
	Uplink   events.MQTTUplink
	Device   mqttspec.DeviceSpec
	Mappings []mqttspec.Mapping
	Time     mqttspec.Path // nil: no timestamp in the message
	Decoder  *Decoder      // nil: the payload is the data
}

// CompileRule validates an uplink and compiles its decoder (from the
// connection's snapshot decoders, by id).
func CompileRule(u events.MQTTUplink, decoders map[string]*Decoder) (*Rule, error) {
	r := &Rule{Uplink: u}
	var err error
	if r.Device, err = mqttspec.ParseDevice(u.Device); err != nil {
		return nil, fmt.Errorf("device: %w", err)
	}
	if r.Mappings, err = mqttspec.ParseFieldMap(u.FieldMap); err != nil {
		return nil, fmt.Errorf("field_map: %w", err)
	}
	if u.Time != nil && *u.Time != "" {
		if r.Time, err = mqttspec.ParsePath(*u.Time); err != nil {
			return nil, fmt.Errorf("time: %w", err)
		}
	}
	if u.DecoderID != nil {
		if r.Decoder = decoders[*u.DecoderID]; r.Decoder == nil {
			return nil, fmt.Errorf("decoder %s missing from the snapshot", *u.DecoderID)
		}
		if !r.Decoder.CanDecode() {
			return nil, fmt.Errorf("decoder %s has no decodeUplink", *u.DecoderID)
		}
	}
	return r, nil
}

// Message is one raw MQTT message.
type Message struct {
	Topic          string
	Payload        []byte
	ContentType    string
	UserProperties map[string]string
}

// Value is one element value, ready for the device core.
type Value struct {
	DeviceExternalID string
	Element          string // element name on that device
	Message          json.RawMessage
	TS               *time.Time
}

// Process runs a message through the rule. The values share the message's
// device; rejections explain what was skipped (a rejection with Fatal set
// means nothing was produced).
func Process(r *Rule, m Message) ([]Value, []Rejection) {
	if len(m.Payload) > MaxInputBytes {
		return nil, []Rejection{{Reason: fmt.Sprintf("payload is over %d KiB", MaxInputBytes>>10), Fatal: true}}
	}
	segments := strings.Split(m.Topic, "/")
	var payload any
	switch r.Uplink.Format {
	case "json", "":
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			if r.Decoder == nil {
				return nil, []Rejection{{Reason: "payload is not JSON", Fatal: true}}
			}
			payload = nil // the decoder still gets the bytes
		}
	case "text":
		payload = string(m.Payload)
	case "number":
		f, err := strconv.ParseFloat(strings.TrimSpace(string(m.Payload)), 64)
		if err != nil {
			return nil, []Rejection{{Reason: "payload is not a number", Fatal: true}}
		}
		payload = f
	}

	var rej []Rejection
	data := payload
	if r.Decoder != nil {
		fport, _ := strconv.Atoi(m.UserProperties["fPort"]) // LoRaWAN bridges can pass it as an MQTT 5 user property
		out, err := r.Decoder.Decode(UplinkInput{Bytes: m.Payload, FPort: fport, Payload: payload, ContentType: m.ContentType,
			Topic: m.Topic, Segments: segments, UserProperties: m.UserProperties})
		if err != nil {
			return nil, []Rejection{{Reason: err.Error(), Fatal: true}}
		}
		for _, w := range out.Warnings {
			rej = append(rej, Rejection{Reason: "decoder warning: " + w})
		}
		if len(out.Errors) > 0 {
			return nil, append(rej, Rejection{Reason: "decoder errors: " + strings.Join(out.Errors, "; "), Fatal: true})
		}
		data = out.Data
	}

	device, ok := r.resolveDevice(segments, data)
	if !ok {
		return nil, append(rej, Rejection{Reason: "no device id in the message (see the rule's device setting)", Fatal: true})
	}
	var ts *time.Time
	if r.Time != nil {
		if v, ok := r.Time.Get(data); ok {
			if t, ok := parseTime(v); ok {
				ts = &t
			} else {
				rej = append(rej, Rejection{Reason: "time is not RFC 3339 or epoch milliseconds"})
			}
		}
	}

	var values []Value
	for _, mp := range r.Mappings {
		v, ok := mp.Path().Get(data)
		if !ok {
			if mp.When != "exists" {
				rej = append(rej, Rejection{Reason: fmt.Sprintf("%q missing for element %s", mp.Value, mp.Element)})
			}
			continue
		}
		var msg any = map[string]any{"value": v}
		if mp.Wrap == "raw" {
			msg = v
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			rej = append(rej, Rejection{Reason: fmt.Sprintf("element %s: %v", mp.Element, err)})
			continue
		}
		if len(raw) > MaxValueBytes {
			rej = append(rej, Rejection{Reason: fmt.Sprintf("element %s: value over 64 KiB", mp.Element)})
			continue
		}
		if len(values) == MaxValues {
			rej = append(rej, Rejection{Reason: fmt.Sprintf("more than %d values from one message", MaxValues)})
			break
		}
		values = append(values, Value{DeviceExternalID: device, Element: mp.Element, Message: raw, TS: ts})
	}
	if len(values) == 0 {
		rej = append(rej, Rejection{Reason: "no element values in the message", Fatal: true})
	}
	return values, rej
}

// Rejection explains a skipped part (or, with Fatal, the whole message).
type Rejection struct {
	Reason string
	Fatal  bool
}

func (r *Rule) resolveDevice(segments []string, data any) (string, bool) {
	switch {
	case r.Device.Fixed != "":
		return r.Device.Fixed, true
	case r.Device.Segment != nil:
		if *r.Device.Segment < len(segments) && segments[*r.Device.Segment] != "" {
			return segments[*r.Device.Segment], true
		}
	case r.Device.Field != "":
		p, _ := mqttspec.ParsePath(r.Device.Field)
		if v, ok := p.Get(data); ok {
			if s := fmt.Sprint(v); s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func parseTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case string:
		t, err := time.Parse(time.RFC3339Nano, x)
		return t.UTC(), err == nil
	case float64:
		return epoch(x), true
	case int64:
		return epoch(float64(x)), true
	}
	return time.Time{}, false
}

// epoch reads seconds or milliseconds (values above 1e11 are milliseconds).
func epoch(n float64) time.Time {
	if n > 1e11 {
		return time.UnixMilli(int64(n)).UTC()
	}
	return time.Unix(int64(n), 0).UTC()
}
