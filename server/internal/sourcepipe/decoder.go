// Package sourcepipe turns a raw message from an external source (MQTT today)
// into element values: an optional JavaScript decoder (TTN/ChirpStack
// contract: decodeUplink / encodeDownlink, or legacy Decoder(bytes, port))
// followed by a declarative field map. It is pure: no I/O, safe to call
// concurrently. The device core turns its values into CloudEvents.
package sourcepipe

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/dop251/goja"
)

const (
	MaxSourceBytes  = 40 << 10 // decoder source, like The Things Stack
	MaxInputBytes   = 64 << 10 // one raw message
	MaxValues       = 100      // element values from one message
	MaxValueBytes   = 64 << 10 // one element message, encoded
	DecoderDeadline = 20 * time.Millisecond
)

// Decoder is a compiled JavaScript decoder. Each call runs in a fresh
// runtime (no state survives between messages) with only ECMAScript built-ins:
// no require, no timers, no I/O, no Go objects.
type Decoder struct {
	prog      *goja.Program
	hasDecode bool
	hasLegacy bool
	hasEncode bool
}

// Compile checks and compiles a decoder. It must define decodeUplink,
// Decoder or encodeDownlink.
func Compile(source string) (*Decoder, error) {
	if len(source) > MaxSourceBytes {
		return nil, fmt.Errorf("decoder is over %d KB", MaxSourceBytes>>10)
	}
	prog, err := goja.Compile("decoder.js", source, false)
	if err != nil {
		return nil, fmt.Errorf("syntax: %v", err)
	}
	d := &Decoder{prog: prog}
	vm, stop, err := d.runtime()
	if err != nil {
		return nil, err
	}
	defer stop()
	isFn := func(name string) bool { _, ok := goja.AssertFunction(vm.Get(name)); return ok }
	d.hasDecode, d.hasLegacy, d.hasEncode = isFn("decodeUplink"), isFn("Decoder"), isFn("encodeDownlink")
	if !d.hasDecode && !d.hasLegacy && !d.hasEncode {
		return nil, errors.New("define decodeUplink(input), Decoder(bytes, port) or encodeDownlink(input)")
	}
	return d, nil
}

// CanDecode / CanEncode report the functions the decoder defines.
func (d *Decoder) CanDecode() bool { return d.hasDecode || d.hasLegacy }
func (d *Decoder) CanEncode() bool { return d.hasEncode }

// runtime starts a fresh VM with the program loaded, interrupted after the deadline.
func (d *Decoder) runtime() (*goja.Runtime, func(), error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	timer := time.AfterFunc(DecoderDeadline, func() { vm.Interrupt("decoder took longer than 20ms") })
	stop := func() { timer.Stop() }
	if _, err := vm.RunProgram(d.prog); err != nil {
		stop()
		return nil, nil, jsError("load", err)
	}
	return vm, stop, nil
}

func jsError(stage string, err error) error {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return fmt.Errorf("%s: %s", stage, ex.Error())
	}
	var in *goja.InterruptedError
	if errors.As(err, &in) {
		return fmt.Errorf("%s: %v", stage, in.Value())
	}
	return fmt.Errorf("%s: %v", stage, err)
}

// UplinkInput is what decodeUplink receives (TTN/ChirpStack fields plus MQTT ones).
type UplinkInput struct {
	Bytes          []byte
	FPort          int // LoRaWAN port for TTN codecs; 0 means 1
	Payload        any // parsed JSON for json payloads, the string for text, else nil
	ContentType    string
	Topic          string
	Segments       []string
	UserProperties map[string]string
	Variables      map[string]string
}

type DecodeOutput struct {
	Data     any      `json:"data"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
}

// Decode runs decodeUplink (or legacy Decoder).
func (d *Decoder) Decode(in UplinkInput) (DecodeOutput, error) {
	var out DecodeOutput
	if !d.CanDecode() {
		return out, errors.New("the decoder has no decodeUplink")
	}
	vm, stop, err := d.runtime()
	if err != nil {
		return out, err
	}
	defer stop()
	bytes := make([]any, len(in.Bytes)) // a plain JS array of numbers, as TTN passes it
	for i, b := range in.Bytes {
		bytes[i] = int64(b)
	}
	var res goja.Value
	if d.hasDecode {
		fn, _ := goja.AssertFunction(vm.Get("decodeUplink"))
		obj := map[string]any{
			"bytes": bytes, "fPort": port(in.FPort), "payload": in.Payload, "contentType": in.ContentType,
			"topic": in.Topic, "segments": toAny(in.Segments), "userProperties": in.UserProperties,
			"variables": in.Variables, "recvTime": time.Now().UTC().Format(time.RFC3339Nano),
		}
		res, err = fn(goja.Undefined(), vm.ToValue(obj))
	} else {
		fn, _ := goja.AssertFunction(vm.Get("Decoder"))
		res, err = fn(goja.Undefined(), vm.ToValue(bytes), vm.ToValue(port(in.FPort)))
	}
	if err != nil {
		return out, jsError("decodeUplink", err)
	}
	exported := res.Export()
	if d.hasDecode {
		m, ok := exported.(map[string]any)
		if !ok {
			return out, errors.New("decodeUplink must return an object {data, warnings, errors}")
		}
		out.Data = m["data"]
		out.Warnings = stringList(m["warnings"])
		out.Errors = stringList(m["errors"])
	} else {
		out.Data = exported // legacy: the decoded object itself
	}
	if err := checkFinite(out.Data, 0); err != nil {
		return out, err
	}
	return out, nil
}

// DownlinkInput is what encodeDownlink receives.
type DownlinkInput struct {
	Device    string
	Element   string
	Data      any
	Variables map[string]string
}

// Encode runs encodeDownlink; it returns the bytes to publish.
func (d *Decoder) Encode(in DownlinkInput) ([]byte, error) {
	if !d.hasEncode {
		return nil, errors.New("the decoder has no encodeDownlink")
	}
	vm, stop, err := d.runtime()
	if err != nil {
		return nil, err
	}
	defer stop()
	fn, _ := goja.AssertFunction(vm.Get("encodeDownlink"))
	res, err := fn(goja.Undefined(), vm.ToValue(map[string]any{
		"device": in.Device, "element": in.Element, "data": in.Data, "variables": in.Variables,
	}))
	if err != nil {
		return nil, jsError("encodeDownlink", err)
	}
	m, ok := res.Export().(map[string]any)
	if !ok {
		return nil, errors.New("encodeDownlink must return {bytes} or {payload}")
	}
	if errs := stringList(m["errors"]); len(errs) > 0 {
		return nil, fmt.Errorf("encodeDownlink: %v", errs)
	}
	if raw, ok := m["bytes"].([]any); ok {
		b := make([]byte, len(raw))
		for i, v := range raw {
			n, ok := toFloat(v)
			if !ok || n < 0 || n > 255 || n != math.Trunc(n) {
				return nil, fmt.Errorf("encodeDownlink: bytes[%d] is not a byte", i)
			}
			b[i] = byte(n)
		}
		return b, nil
	}
	if p, ok := m["payload"]; ok {
		if s, ok := p.(string); ok {
			return []byte(s), nil
		}
		if err := checkFinite(p, 0); err != nil {
			return nil, err
		}
		return json.Marshal(p)
	}
	return nil, errors.New("encodeDownlink must return {bytes} or {payload}")
}

func port(p int) int {
	if p <= 0 {
		return 1
	}
	return p
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func stringList(v any) []string {
	a, _ := v.([]any)
	out := make([]string, 0, len(a))
	for _, x := range a {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// checkFinite rejects NaN and ±Infinity anywhere in a value (JSON can't carry them).
func checkFinite(v any, depth int) error {
	if depth > 32 {
		return errors.New("decoded value is nested too deeply")
	}
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errors.New("decoded value contains NaN or Infinity")
		}
	case map[string]any:
		for _, e := range x {
			if err := checkFinite(e, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := checkFinite(e, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
