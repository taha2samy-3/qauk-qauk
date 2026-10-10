// Package elementpipe compiles and runs element pipelines (station 2 of the
// processing chain). A pipeline is an ordered list of steps applied to a
// device message before it reaches the rate limiter and the bus. It is pure:
// no I/O, no database, no bus, no gateway; it can run in any role.
//
// Steps that cannot apply (field missing, wrong type for a numeric step) fail
// the whole message, except deadband and drop_if, which pass non-numbers
// through.
package elementpipe

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/taha2samy/quackquack/server/internal/mqttspec"
)

// MaxSteps is the maximum steps a pipeline may have.
const MaxSteps = 32

// MaxScriptSource is the maximum JS source length for a script step.
const MaxScriptSource = 40 << 10

// MaxScriptDeadline is the time budget for a script step execution.
const MaxScriptDeadline = 20 * time.Millisecond

// RunResult is the outcome of applying a pipeline to one message.
type RunResult int

const (
	// RunOK: the message passed and was (possibly) transformed.
	RunOK RunResult = iota
	// RunFiltered: deadband, drop_if or a script returning null suppressed the message.
	RunFiltered
)

// FilterReason describes why a message was filtered (for metrics).
type FilterReason string

const (
	FilterDeadband FilterReason = "deadband"
	FilterDropIf   FilterReason = "drop_if"
	FilterScript   FilterReason = "script"
)

// ErrPipeline is returned when a step fails (field missing/wrong type, script
// throws or times out). It is distinct from filter: the caller should dead-
// letter the message and report an error.
type ErrPipeline struct {
	Step   string
	Reason string
}

func (e *ErrPipeline) Error() string {
	return fmt.Sprintf("pipeline step %s: %s", e.Step, e.Reason)
}

// ------- step configs (plain JSON structs) -------

// StepKind enumerates the step types.
type StepKind string

const (
	KindPick     StepKind = "pick"
	KindScale    StepKind = "scale"
	KindUnit     StepKind = "unit"
	KindRound    StepKind = "round"
	KindClamp    StepKind = "clamp"
	KindMap      StepKind = "map"
	KindDeadband StepKind = "deadband"
	KindDropIf   StepKind = "drop_if"
	KindScript   StepKind = "script"
)

// StepConfig is the raw JSON config of one step (discriminated by "kind").
type StepConfig struct {
	Kind StepKind `json:"kind"`

	// pick
	Path string `json:"path,omitempty"`

	// scale
	Field string   `json:"field,omitempty"`
	Mul   *float64 `json:"mul,omitempty"`
	Add   float64  `json:"add,omitempty"`

	// unit
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`

	// round
	Decimals *int `json:"decimals,omitempty"`

	// clamp
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`

	// map
	Table   map[string]json.RawMessage `json:"table,omitempty"`
	Default *json.RawMessage           `json:"default,omitempty"`

	// deadband
	Abs        *float64 `json:"abs,omitempty"`
	Pct        *float64 `json:"pct,omitempty"`
	MaxSilence *string  `json:"max_silence,omitempty"` // go duration

	// drop_if
	Op    string   `json:"op,omitempty"`
	Value *float64 `json:"value,omitempty"`

	// script
	Source string `json:"source,omitempty"`
}

// ------- compiled pipeline -------

// Pipeline is a compiled, immutable pipeline. Zero value = no pipeline (valid,
// apply is a no-op).
type Pipeline struct {
	Version int
	steps   []compiledStep
}

// Context provides execution context to pipeline steps.
type Context struct {
	Element string
	Device  string
	Time    time.Time
	Last    Msg
}

// compiledStep is a compiled step function.
type compiledStep struct {
	kind       StepKind
	maxSilence time.Duration
	run        func(msg map[string]any, ctx Context) (map[string]any, RunResult, FilterReason, error)
	inverse    func(msg map[string]any) (map[string]any, error) // nil = not invertible
}

// ------- validation & compilation -------

// Compile validates and compiles a slice of step configs. Returns an error if
// any step is invalid or the script won't compile.
func Compile(version int, steps []StepConfig) (*Pipeline, error) {
	if len(steps) > MaxSteps {
		return nil, fmt.Errorf("at most %d steps", MaxSteps)
	}
	p := &Pipeline{Version: version, steps: make([]compiledStep, len(steps))}
	for i, s := range steps {
		cs, err := compileStep(s)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", i, s.Kind, err)
		}
		p.steps[i] = cs
	}
	return p, nil
}

// CompileJSON parses and compiles a JSON steps array.
func CompileJSON(version int, raw json.RawMessage) (*Pipeline, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "[]" {
		return &Pipeline{Version: version}, nil
	}
	var steps []StepConfig
	if err := json.Unmarshal(raw, &steps); err != nil {
		return nil, fmt.Errorf("steps: %w", err)
	}
	return Compile(version, steps)
}

// HasSteps reports whether the pipeline has any steps.
func (p *Pipeline) HasSteps() bool {
	return p != nil && len(p.steps) > 0
}

// HasScript reports whether the pipeline has any script steps.
func (p *Pipeline) HasScript() bool {
	if p == nil {
		return false
	}
	for _, s := range p.steps {
		if s.kind == KindScript {
			return true
		}
	}
	return false
}

// MaxSilence returns the minimum max_silence duration configured across deadband steps
// (defaulting to 10m if a deadband step has no explicit max_silence).
// Returns 0 if there are no deadband steps.
func (p *Pipeline) MaxSilence() time.Duration {
	if p == nil {
		return 0
	}
	var minSilence time.Duration
	for _, s := range p.steps {
		if s.kind == KindDeadband {
			ms := s.maxSilence
			if ms == 0 {
				ms = 10 * time.Minute
			}
			if minSilence == 0 || ms < minSilence {
				minSilence = ms
			}
		}
	}
	return minSilence
}

// ------- apply -------

// Msg is a decoded element message (always a JSON object on the bus).
type Msg = map[string]any

// Apply runs the pipeline on msg. last is the latest published message for
// this element (for deadband; may be nil). On RunOK the returned map is the
// transformed message; on RunFiltered the caller should not publish.
func (p *Pipeline) Apply(msg Msg, last Msg) (Msg, RunResult, FilterReason, error) {
	return p.ApplyWithContext(msg, Context{Last: last, Time: time.Now().UTC()})
}

// ApplyWithContext runs the pipeline with full execution context (element, device, time, last).
func (p *Pipeline) ApplyWithContext(msg Msg, ctx Context) (Msg, RunResult, FilterReason, error) {
	if p == nil || len(p.steps) == 0 {
		return msg, RunOK, "", nil
	}
	if ctx.Time.IsZero() {
		ctx.Time = time.Now().UTC()
	}
	cur := copyMap(msg)
	for _, s := range p.steps {
		out, res, fr, err := s.run(cur, ctx)
		if err != nil {
			return nil, RunOK, "", err
		}
		if res == RunFiltered {
			return nil, RunFiltered, fr, nil
		}
		cur = out
	}
	return cur, RunOK, "", nil
}

// Inverse runs the invertible steps in reverse order on a command message.
// Steps that are not invertible are skipped. Returns the transformed message.
func (p *Pipeline) Inverse(msg Msg) (Msg, error) {
	if p == nil || len(p.steps) == 0 {
		return msg, nil
	}
	cur := copyMap(msg)
	for i := len(p.steps) - 1; i >= 0; i-- {
		s := p.steps[i]
		if s.inverse == nil {
			continue
		}
		out, err := s.inverse(cur)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s) inverse: %w", i, s.kind, err)
		}
		cur = out
	}
	return cur, nil
}

// ------- step compilation -------

func compileStep(s StepConfig) (compiledStep, error) {
	switch s.Kind {
	case KindPick:
		return compilePick(s)
	case KindScale:
		return compileScale(s)
	case KindUnit:
		return compileUnit(s)
	case KindRound:
		return compileRound(s)
	case KindClamp:
		return compileClamp(s)
	case KindMap:
		return compileMap(s)
	case KindDeadband:
		return compileDeadband(s)
	case KindDropIf:
		return compileDropIf(s)
	case KindScript:
		return compileScript(s)
	default:
		return compiledStep{}, fmt.Errorf("unknown kind %q", s.Kind)
	}
}

// pick: replace the message by {"value": <path>}
func compilePick(s StepConfig) (compiledStep, error) {
	path, err := mqttspec.ParsePath(s.Path)
	if err != nil {
		return compiledStep{}, fmt.Errorf("path: %w", err)
	}
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		var val any
		if len(path) == 0 {
			val = msg
		} else {
			v, ok := path.Get(msg)
			if !ok {
				return nil, RunOK, "", &ErrPipeline{Step: "pick", Reason: fmt.Sprintf("path %q not found", s.Path)}
			}
			val = v
		}
		return map[string]any{"value": val}, RunOK, "", nil
	}
	return compiledStep{kind: KindPick, run: run}, nil
}

// scale: v × mul + add (inverse: (v - add) / mul)
func compileScale(s StepConfig) (compiledStep, error) {
	if s.Mul == nil {
		return compiledStep{}, errors.New("mul is required")
	}
	if *s.Mul == 0 {
		return compiledStep{}, errors.New("mul must not be zero")
	}
	field := fieldOrValue(s.Field)
	mul, add := *s.Mul, s.Add
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := getNumericField(msg, field)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "scale", Reason: fmt.Sprintf("field %q is not a number", field)}
		}
		return setField(msg, field, v*mul+add), RunOK, "", nil
	}
	inv := func(msg Msg) (Msg, error) {
		v, ok := getNumericField(msg, field)
		if !ok {
			return msg, nil // skip silently on inverse
		}
		return setField(msg, field, (v-add)/mul), nil
	}
	return compiledStep{kind: KindScale, run: run, inverse: inv}, nil
}

// unit: fixed conversion table
func compileUnit(s StepConfig) (compiledStep, error) {
	fwd, ok := unitConversions[s.From+"/"+s.To]
	if !ok {
		return compiledStep{}, fmt.Errorf("unknown unit conversion %q → %q", s.From, s.To)
	}
	rev, hasRev := unitConversions[s.To+"/"+s.From]
	field := fieldOrValue(s.Field)
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := getNumericField(msg, field)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "unit", Reason: fmt.Sprintf("field %q is not a number", field)}
		}
		return setField(msg, field, fwd(v)), RunOK, "", nil
	}
	var inv func(Msg) (Msg, error)
	if hasRev {
		inv = func(msg Msg) (Msg, error) {
			v, ok := getNumericField(msg, field)
			if !ok {
				return msg, nil
			}
			return setField(msg, field, rev(v)), nil
		}
	}
	return compiledStep{kind: KindUnit, run: run, inverse: inv}, nil
}

// round: round half away from zero
func compileRound(s StepConfig) (compiledStep, error) {
	if s.Decimals == nil {
		return compiledStep{}, errors.New("decimals is required")
	}
	if *s.Decimals < 0 || *s.Decimals > 6 {
		return compiledStep{}, errors.New("decimals must be 0-6")
	}
	field := fieldOrValue(s.Field)
	factor := math.Pow10(*s.Decimals)
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := getNumericField(msg, field)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "round", Reason: fmt.Sprintf("field %q is not a number", field)}
		}
		rounded := math.Round(v*factor) / factor
		return setField(msg, field, rounded), RunOK, "", nil
	}
	return compiledStep{kind: KindRound, run: run}, nil
}

// clamp
func compileClamp(s StepConfig) (compiledStep, error) {
	if s.Min == nil || s.Max == nil {
		return compiledStep{}, errors.New("min and max are required")
	}
	if *s.Min > *s.Max {
		return compiledStep{}, errors.New("min must be ≤ max")
	}
	field := fieldOrValue(s.Field)
	lo, hi := *s.Min, *s.Max
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := getNumericField(msg, field)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "clamp", Reason: fmt.Sprintf("field %q is not a number", field)}
		}
		return setField(msg, field, clamp(v, lo, hi)), RunOK, "", nil
	}
	return compiledStep{kind: KindClamp, run: run}, nil
}

// map: string → JSON value
func compileMap(s StepConfig) (compiledStep, error) {
	if len(s.Table) == 0 {
		return compiledStep{}, errors.New("table is required and must not be empty")
	}
	if len(s.Table) > 256 {
		return compiledStep{}, errors.New("table must have at most 256 entries")
	}
	// pre-unmarshal table values for fast lookup
	table := make(map[string]any, len(s.Table))
	for k, raw := range s.Table {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return compiledStep{}, fmt.Errorf("table[%q]: %w", k, err)
		}
		table[k] = v
	}
	var def *any
	if s.Default != nil {
		var v any
		if err := json.Unmarshal(*s.Default, &v); err != nil {
			return compiledStep{}, fmt.Errorf("default: %w", err)
		}
		def = &v
	}
	field := fieldOrValue(s.Field)

	// check one-to-one for inverse
	rev, isOneToOne := buildReverseMap(table)

	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v := getStringField(msg, field)
		mapped, ok := table[v]
		if !ok {
			if def != nil {
				return setField(msg, field, *def), RunOK, "", nil
			}
			return nil, RunOK, "", &ErrPipeline{Step: "map", Reason: fmt.Sprintf("value %q not in table and no default", v)}
		}
		return setField(msg, field, mapped), RunOK, "", nil
	}
	var inv func(Msg) (Msg, error)
	if isOneToOne {
		inv = func(msg Msg) (Msg, error) {
			v := fmt.Sprint(getAnyField(msg, field))
			if k, ok := rev[v]; ok {
				return setField(msg, field, k), nil
			}
			return msg, nil // not in rev: skip
		}
	}
	return compiledStep{kind: KindMap, run: run, inverse: inv}, nil
}

// deadband: filter if |delta| < abs threshold and |delta/last| < pct threshold
func compileDeadband(s StepConfig) (compiledStep, error) {
	if s.Abs == nil && s.Pct == nil {
		return compiledStep{}, errors.New("at least one of abs or pct is required")
	}
	var maxSilence time.Duration
	if s.MaxSilence != nil {
		d, err := time.ParseDuration(*s.MaxSilence)
		if err != nil {
			return compiledStep{}, fmt.Errorf("max_silence: %w", err)
		}
		if d < 0 {
			return compiledStep{}, errors.New("max_silence must not be negative")
		}
		maxSilence = d
	}
	if maxSilence == 0 {
		maxSilence = 10 * time.Minute
	}
	field := fieldOrValue(s.Field)
	absThresh := s.Abs
	pctThresh := s.Pct
	run := func(msg Msg, ctx Context) (Msg, RunResult, FilterReason, error) {
		v, ok := getNumericField(msg, field)
		if !ok {
			// non-numeric: pass through (spec says deadband passes non-numbers)
			return msg, RunOK, "", nil
		}
		// If we have no last value, always pass
		if ctx.Last == nil {
			return msg, RunOK, "", nil
		}
		lastV, ok := getNumericField(ctx.Last, field)
		if !ok {
			return msg, RunOK, "", nil
		}
		delta := math.Abs(v - lastV)
		// Check thresholds
		if absThresh != nil && delta >= *absThresh {
			return msg, RunOK, "", nil
		}
		if pctThresh != nil {
			pct := 0.0
			if lastV != 0 {
				pct = delta / math.Abs(lastV) * 100
			}
			if pct >= *pctThresh {
				return msg, RunOK, "", nil
			}
		}
		return nil, RunFiltered, FilterDeadband, nil
	}
	return compiledStep{kind: KindDeadband, maxSilence: maxSilence, run: run}, nil
}

// DeadbandMaxSilence returns the max_silence duration from a deadband step (0 = default 10m).
// Used by the gateway to bypass deadband after silence.
func DeadbandMaxSilence(s StepConfig) time.Duration {
	if s.Kind != KindDeadband || s.MaxSilence == nil {
		return 0
	}
	d, _ := time.ParseDuration(*s.MaxSilence)
	return d
}

// drop_if
func compileDropIf(s StepConfig) (compiledStep, error) {
	if s.Value == nil {
		return compiledStep{}, errors.New("value is required")
	}
	ops := map[string]func(float64, float64) bool{
		"<": func(a, b float64) bool { return a < b },
		"<=": func(a, b float64) bool { return a <= b },
		">": func(a, b float64) bool { return a > b },
		">=": func(a, b float64) bool { return a >= b },
		"==": func(a, b float64) bool { return a == b },
		"!=": func(a, b float64) bool { return a != b },
	}
	fn, ok := ops[s.Op]
	if !ok {
		return compiledStep{}, fmt.Errorf("op must be one of < <= > >= == !=, got %q", s.Op)
	}
	field := fieldOrValue(s.Field)
	threshold := *s.Value
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := getNumericField(msg, field)
		if !ok {
			// non-numeric: pass through
			return msg, RunOK, "", nil
		}
		if fn(v, threshold) {
			return nil, RunFiltered, FilterDropIf, nil
		}
		return msg, RunOK, "", nil
	}
	return compiledStep{kind: KindDropIf, run: run}, nil
}

// script
func compileScript(s StepConfig) (compiledStep, error) {
	src := strings.TrimSpace(s.Source)
	if src == "" {
		return compiledStep{}, errors.New("source is required")
	}
	if len(src) > MaxScriptSource {
		return compiledStep{}, fmt.Errorf("source exceeds %d bytes", MaxScriptSource)
	}
	prog, err := goja.Compile("pipeline.js", src, false)
	if err != nil {
		return compiledStep{}, fmt.Errorf("compile: %w", err)
	}
	// Verify it defines transform (and optionally untransform)
	vm, stop, err := runScript(prog)
	if err != nil {
		return compiledStep{}, err
	}
	_, hasTransform := goja.AssertFunction(vm.Get("transform"))
	_, hasUntransform := goja.AssertFunction(vm.Get("untransform"))
	stop()
	if !hasTransform {
		return compiledStep{}, errors.New("must define function transform(msg, ctx)")
	}

	run := func(msg Msg, ctx Context) (outMap Msg, res RunResult, fr FilterReason, err error) {
		defer func() {
			if r := recover(); r != nil {
				outMap = nil
				res = RunOK
				fr = ""
				err = &ErrPipeline{Step: "script", Reason: fmt.Sprintf("panic: %v", r)}
			}
		}()
		vm, stop, err := runScript(prog)
		if err != nil {
			return nil, RunOK, "", &ErrPipeline{Step: "script", Reason: err.Error()}
		}
		defer stop()
		fn, _ := goja.AssertFunction(vm.Get("transform"))
		ctxMap := map[string]any{
			"element": ctx.Element,
			"device":  ctx.Device,
			"time":    ctx.Time.UTC().Format(time.RFC3339Nano),
			"last":    ctx.Last,
		}
		resVal, err := fn(goja.Undefined(), vm.ToValue(msg), vm.ToValue(ctxMap))
		if err != nil {
			return nil, RunOK, "", &ErrPipeline{Step: "script", Reason: scriptErrMsg(err)}
		}
		exp := resVal.Export()
		if exp == nil {
			return nil, RunFiltered, FilterScript, nil
		}
		out, ok := exp.(map[string]any)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "script", Reason: "transform must return an object or null"}
		}
		if err := checkFinite(out, 0); err != nil {
			return nil, RunOK, "", &ErrPipeline{Step: "script", Reason: err.Error()}
		}
		return out, RunOK, "", nil
	}

	var inv func(Msg) (Msg, error)
	if hasUntransform {
		inv = func(msg Msg) (outMap Msg, err error) {
			defer func() {
				if r := recover(); r != nil {
					outMap = msg
					err = nil
				}
			}()
			vm, stop, err := runScript(prog)
			if err != nil {
				return msg, nil // skip on error
			}
			defer stop()
			fn, _ := goja.AssertFunction(vm.Get("untransform"))
			ctxMap := map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano)}
			resVal, err := fn(goja.Undefined(), vm.ToValue(msg), vm.ToValue(ctxMap))
			if err != nil {
				return msg, nil // skip on error in inverse
			}
			exp := resVal.Export()
			if exp == nil {
				return msg, nil
			}
			out, ok := exp.(map[string]any)
			if !ok {
				return msg, nil
			}
			return out, nil
		}
	}
	return compiledStep{kind: KindScript, run: run, inverse: inv}, nil
}

// IsInvertible reports whether all steps with an inverse in the compiled
// pipeline produce a meaningful inverse. Used by the UI to warn.
func (p *Pipeline) IsInvertible() bool {
	if p == nil {
		return true
	}
	for _, s := range p.steps {
		switch s.kind {
		case KindScale, KindUnit, KindMap, KindScript:
			if s.inverse == nil {
				return false
			}
		}
	}
	return true
}

// StepIsInvertible reports whether the step at index i has an inverse.
func (p *Pipeline) StepIsInvertible(i int) bool {
	if p == nil || i >= len(p.steps) {
		return false
	}
	return p.steps[i].inverse != nil
}

// ------- helpers -------

func fieldOrValue(f string) string {
	if f == "" {
		return "value"
	}
	return f
}

func getNumericField(msg Msg, field string) (float64, bool) {
	v, ok := msg[field]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func getStringField(msg Msg, field string) string {
	v, ok := msg[field]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func getAnyField(msg Msg, field string) any {
	return msg[field]
}

func setField(msg Msg, field string, v any) Msg {
	out := copyMap(msg)
	out[field] = v
	return out
}

func copyMap(m Msg) Msg {
	out := make(Msg, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func buildReverseMap(table map[string]any) (map[string]string, bool) {
	rev := make(map[string]string, len(table))
	seen := make(map[string]struct{}, len(table))
	for k, v := range table {
		vs := fmt.Sprint(v)
		if _, dup := seen[vs]; dup {
			return nil, false // not one-to-one
		}
		seen[vs] = struct{}{}
		rev[vs] = k
	}
	return rev, true
}

// runScript starts a fresh goja VM, loads the program and starts the
// deadline interrupt. The caller must call stop() when done.
func runScript(prog *goja.Program) (*goja.Runtime, func(), error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	timer := time.AfterFunc(MaxScriptDeadline, func() { vm.Interrupt("pipeline script took longer than 20ms") })
	stop := func() { timer.Stop() }
	if _, err := vm.RunProgram(prog); err != nil {
		stop()
		return nil, nil, fmt.Errorf("load: %w", err)
	}
	return vm, stop, nil
}

func scriptErrMsg(err error) string {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return ex.Error()
	}
	var in *goja.InterruptedError
	if errors.As(err, &in) {
		return fmt.Sprintf("timeout: %v", in.Value())
	}
	return err.Error()
}

// checkFinite rejects NaN and ±Infinity anywhere in a value.
func checkFinite(v any, depth int) error {
	if depth > 32 {
		return errors.New("value nested too deeply")
	}
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errors.New("value contains NaN or Infinity")
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
