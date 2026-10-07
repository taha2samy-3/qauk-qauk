package history

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Point is one numeric attribute of one event.
type Point struct {
	Time      time.Time
	EventID   uuid.UUID
	ElementID uuid.UUID
	Field     string
	Value     float64
}

// Limits of Points, so one message can't explode into unbounded rows.
const (
	MaxPointsPerEvent = 64
	MaxDepth          = 8
	MaxArrayItems     = 32
	MaxFieldLen       = 200
)

// ValueField is the attribute that aggregates read when no field is given.
const ValueField = "value"

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_@$-]+$`)

// Points extracts the numeric attributes of an event's message, one point per
// attribute path ("temperature", "gps.lat", "sensors[0].temp"). Numbers,
// booleans (1/0) and numeric strings count; NaN, ±Inf and out-of-range numbers
// don't. A bare scalar message is the field "value". If the message has no
// numeric "value" of its own, NumericValue (e.g. a chart's "y") is added as
// "value", so aggregates without a field keep working for every shape.
// Keys that can't be addressed by a path (dots, spaces, …) are skipped.
func Points(ev Event) []Point {
	var root any
	dec := json.NewDecoder(bytes.NewReader(ev.Payload))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil
	}
	var out []Point
	add := func(field string, v float64) {
		if len(out) < MaxPointsPerEvent {
			out = append(out, Point{Time: ev.Time, EventID: ev.ID, ElementID: ev.ElementID, Field: field, Value: v})
		}
	}
	var walk func(path string, v any, depth int)
	walk = func(path string, v any, depth int) {
		if depth > MaxDepth || len(out) >= MaxPointsPerEvent {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if !keyRe.MatchString(k) {
					continue
				}
				p := k
				if path != "" {
					p = path + "." + k
				}
				if len(p) <= MaxFieldLen {
					walk(p, child, depth+1)
				}
			}
		case []any:
			for i, child := range x {
				if i >= MaxArrayItems {
					break
				}
				p := path + "[" + strconv.Itoa(i) + "]"
				if path != "" && len(p) <= MaxFieldLen {
					walk(p, child, depth+1)
				}
			}
		default:
			if path == "" {
				path = ValueField
			}
			if f, ok := numeric(x); ok {
				add(path, f)
			}
		}
	}
	walk("", root, 0)
	hasValue := false
	for _, p := range out {
		if p.Field == ValueField {
			hasValue = true
			break
		}
	}
	if !hasValue && ev.Value != nil && finite(*ev.Value) {
		add(ValueField, *ev.Value)
	}
	sortPoints(out)
	return out
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		return f, err == nil && finite(f)
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		s := strings.TrimSpace(x)
		if s == "" || len(s) > 64 {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil && finite(f) && !strings.ContainsAny(s, "xXpP_") // no hex or "_" literals
	}
	return 0, false
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// sortPoints keeps the output deterministic (map iteration is random).
func sortPoints(ps []Point) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].Field < ps[j-1].Field; j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

// NumericValue extracts message.value, a chart-style message.y, or a bare
// number/bool: the element's main numeric value.
func NumericValue(msg json.RawMessage) *float64 {
	var v any
	if err := json.Unmarshal(msg, &v); err != nil {
		return nil
	}
	if obj, ok := v.(map[string]any); ok {
		if val, ok := obj["value"]; ok {
			v = val
		} else {
			v = obj["y"]
		}
	}
	switch x := v.(type) {
	case float64:
		return &x
	case bool:
		f := 0.0
		if x {
			f = 1
		}
		return &f
	}
	return nil
}

var fieldRe = regexp.MustCompile(`^[A-Za-z0-9_@$-]+(\.[A-Za-z0-9_@$-]+|\[[0-9]+\])*$`)

// ValidField reports whether f is a path that Points can produce.
func ValidField(f string) bool {
	return len(f) <= MaxFieldLen && fieldRe.MatchString(f)
}
