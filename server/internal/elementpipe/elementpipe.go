// Package elementpipe compiles and runs element pipelines (station 2 of the
// processing chain). A pipeline is an ordered list of steps applied to a
// device message before it reaches the rate limiter and the bus. It is pure:
// no I/O, no database, no bus, no gateway; it can run in any role.
//
// Steps that cannot apply (field missing, wrong type for a numeric step) fail
// the whole message, except deadband and drop_if, which pass non-numbers
// through. Steps never modify their input: messages are copied on write, and
// scripts get JSON copies, so a compiled pipeline and the messages it reads
// can be shared between goroutines.
package elementpipe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/taha2samy/quackquack/server/internal/jsbox"
	"github.com/taha2samy/quackquack/server/internal/mqttspec"
)

const (
	// MaxSteps is the maximum steps a pipeline may have.
	MaxSteps = 32
	// MaxScriptSource is the maximum JS source length for a script step.
	MaxScriptSource = 40 << 10
	// MaxScriptDeadline is the time budget of one script run (loading its
	// top-level code included).
	MaxScriptDeadline = 20 * time.Millisecond
	// MaxScriptOutput bounds a script's result, encoded.
	MaxScriptOutput = 64 << 10
	// MaxTableEntries bounds a map step's table.
	MaxTableEntries = 256
	// DefaultMaxSilence is a deadband's max_silence when none is set.
	DefaultMaxSilence = 10 * time.Minute
	// MinMaxSilence is the shortest max_silence accepted.
	MinMaxSilence = time.Second
)

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
// throws or times out, a result that isn't a finite JSON value). It is
// distinct from filter: the caller should dead-letter the message and report
// an error. For commands, the caller must not deliver the command.
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
// field and path use the dot/[index]/["key"] path syntax of mqttspec.
type StepConfig struct {
	Kind StepKind `json:"kind"`

	// pick
	Path string `json:"path,omitempty"`

	// every step but pick and script: the field it acts on (default "value")
	Field string `json:"field,omitempty"`

	// scale
	Mul *float64 `json:"mul,omitempty"`
	Add float64  `json:"add,omitempty"`

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

// Pipeline is a compiled, immutable pipeline, safe for concurrent use. A nil
// pipeline (or one without steps) passes messages through.
type Pipeline struct {
	Version int
	steps   []compiledStep
}

// Context is what steps know besides the message.
type Context struct {
	Element string
	Device  string
	Time    time.Time
	// Last is the element's latest published message (nil = none known), for
	// deadband and scripts. Steps never modify it.
	Last Msg
	// LastAt is when Last was published (zero = unknown).
	LastAt time.Time
}

type compiledStep struct {
	kind       StepKind
	field      string // canonical path of the field the step acts on; "" = the whole message
	maxSilence time.Duration
	run        func(msg Msg, ctx Context) (Msg, RunResult, FilterReason, error)
	inverse    func(msg Msg, ctx Context) (Msg, error) // nil = not applied to commands
}

// Msg is a decoded element message (a JSON object).
type Msg = map[string]any

// WrapScalar returns an object message for v: objects as they are (false),
// anything else (number, bool, string, null, array) as {"value": v} (true).
// Callers wrap bare JSON values before running a pipeline, and unwrap
// commands they wrapped.
func WrapScalar(v any) (Msg, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, false
	}
	return Msg{"value": v}, true
}

// ------- validation & compilation -------

// Compile validates and compiles a slice of step configs.
func Compile(version int, steps []StepConfig) (*Pipeline, error) {
	if len(steps) > MaxSteps {
		return nil, fmt.Errorf("at most %d steps", MaxSteps)
	}
	p := &Pipeline{Version: version, steps: make([]compiledStep, len(steps))}
	for i, s := range steps {
		cs, err := compileStep(s)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", i+1, s.Kind, err)
		}
		p.steps[i] = cs
	}
	if err := checkDeadbandOrder(p.steps); err != nil {
		return nil, err
	}
	return p, nil
}

// CompileJSON parses (unknown keys are an error) and compiles a JSON steps array.
func CompileJSON(version int, raw json.RawMessage) (*Pipeline, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return &Pipeline{Version: version}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var steps []StepConfig
	if err := dec.Decode(&steps); err != nil {
		return nil, fmt.Errorf("steps: %w", err)
	}
	if len(steps) == 0 {
		return &Pipeline{Version: version}, nil
	}
	return Compile(version, steps)
}

// A deadband compares with the last PUBLISHED message, i.e. the pipeline's
// output. A step after it that changes the compared field (or the whole
// message) would make it compare different units, so that order is refused.
func checkDeadbandOrder(steps []compiledStep) error {
	var deadbands []int
	for i, s := range steps {
		for _, d := range deadbands {
			switch s.kind {
			case KindPick, KindScript:
				return fmt.Errorf("step %d (%s) changes the message after the deadband in step %d; put the deadband after it", i+1, s.kind, d+1)
			case KindScale, KindUnit, KindRound, KindClamp, KindMap:
				if overlaps(s.field, steps[d].field) {
					return fmt.Errorf("step %d (%s) changes the field the deadband in step %d compares; put the deadband after the steps that change it", i+1, s.kind, d+1)
				}
			}
		}
		if s.kind == KindDeadband {
			deadbands = append(deadbands, i)
		}
	}
	return nil
}

// overlaps reports whether two canonical paths are the same or one contains the other.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"[") || strings.HasPrefix(b, a+"[")
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

// MaxSilence returns the shortest max_silence of the deadband steps (0 when
// there are none). Deadband steps apply their own max_silence from
// Context.LastAt; this is informational.
func (p *Pipeline) MaxSilence() time.Duration {
	if p == nil {
		return 0
	}
	var out time.Duration
	for _, s := range p.steps {
		if s.kind == KindDeadband && (out == 0 || s.maxSilence < out) {
			out = s.maxSilence
		}
	}
	return out
}

// ------- apply -------

// Apply runs the pipeline on msg with last as the latest published message.
func (p *Pipeline) Apply(msg Msg, last Msg) (Msg, RunResult, FilterReason, error) {
	return p.ApplyWithContext(msg, Context{Last: last, Time: time.Now().UTC()})
}

// ApplyWithContext runs the pipeline on a device message. On RunOK the
// returned map is the transformed message; on RunFiltered the caller should
// not publish. msg and ctx.Last are not modified.
func (p *Pipeline) ApplyWithContext(msg Msg, ctx Context) (Msg, RunResult, FilterReason, error) {
	if p == nil || len(p.steps) == 0 {
		return msg, RunOK, "", nil
	}
	if ctx.Time.IsZero() {
		ctx.Time = time.Now().UTC()
	}
	cur := msg
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

// Inverse runs the steps that apply to commands, in reverse order.
func (p *Pipeline) Inverse(msg Msg) (Msg, error) {
	return p.InverseWithContext(msg, Context{Time: time.Now().UTC()})
}

// InverseWithContext turns a command in engineering units into device units:
// the steps that apply to commands (scale, unit, one-to-one map, script with
// untransform) run in reverse order; the others are skipped. Any failure is an
// *ErrPipeline, and the caller must not deliver the command.
func (p *Pipeline) InverseWithContext(msg Msg, ctx Context) (Msg, error) {
	if p == nil || len(p.steps) == 0 {
		return msg, nil
	}
	if ctx.Time.IsZero() {
		ctx.Time = time.Now().UTC()
	}
	cur := msg
	for i := len(p.steps) - 1; i >= 0; i-- {
		s := p.steps[i]
		if s.inverse == nil {
			continue
		}
		out, err := s.inverse(cur, ctx)
		if err != nil {
			return nil, err
		}
		cur = out
	}
	return cur, nil
}

// Invertible reports whether commands are transformed at all (at least one
// step applies to commands).
func (p *Pipeline) Invertible() bool {
	if p == nil {
		return false
	}
	for _, s := range p.steps {
		if s.inverse != nil {
			return true
		}
	}
	return false
}

// IsInvertible reports whether every step that changes values also applies to
// commands, i.e. a command round-trips (round, clamp, deadband and drop_if
// only lose precision or drop, and don't count). A pick, a non one-to-one map
// or a script without untransform make it false.
func (p *Pipeline) IsInvertible() bool {
	if p == nil {
		return true
	}
	for _, s := range p.steps {
		switch s.kind {
		case KindPick, KindScale, KindUnit, KindMap, KindScript:
			if s.inverse == nil {
				return false
			}
		}
	}
	return true
}

// StepIsInvertible reports whether step i applies to commands.
func (p *Pipeline) StepIsInvertible(i int) bool {
	if p == nil || i < 0 || i >= len(p.steps) {
		return false
	}
	return p.steps[i].inverse != nil
}

// StepsInvertible reports, per step, whether it applies to commands.
func (p *Pipeline) StepsInvertible() []bool {
	if p == nil {
		return nil
	}
	out := make([]bool, len(p.steps))
	for i, s := range p.steps {
		out[i] = s.inverse != nil
	}
	return out
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

// pick: the object at path becomes the message; anything else becomes {"value": x}.
func compilePick(s StepConfig) (compiledStep, error) {
	path, err := mqttspec.ParsePath(s.Path)
	if err != nil {
		return compiledStep{}, fmt.Errorf("path: %w", err)
	}
	if len(path) == 0 {
		return compiledStep{}, errors.New("path is required")
	}
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := path.Get(msg)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "pick", Reason: fmt.Sprintf("path %q not found", s.Path)}
		}
		if m, ok := v.(map[string]any); ok {
			return m, RunOK, "", nil
		}
		return Msg{"value": v}, RunOK, "", nil
	}
	return compiledStep{kind: KindPick, run: run}, nil
}

// scale: v × mul + add (commands: (v − add) / mul)
func compileScale(s StepConfig) (compiledStep, error) {
	if s.Mul == nil {
		return compiledStep{}, errors.New("mul is required")
	}
	mul, add := *s.Mul, s.Add
	switch {
	case !finite(mul) || !finite(add):
		return compiledStep{}, errors.New("mul and add must be finite numbers")
	case mul == 0:
		return compiledStep{}, errors.New("mul must not be zero")
	case math.IsInf(1/mul, 0):
		return compiledStep{}, errors.New("mul is too small to invert")
	}
	f, err := compileField(s.Field)
	if err != nil {
		return compiledStep{}, err
	}
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := f.num(msg)
		if !ok {
			return nil, RunOK, "", f.notNumber(KindScale)
		}
		out, err := f.setNum(KindScale, msg, v*mul+add)
		return out, RunOK, "", err
	}
	inv := func(msg Msg, _ Context) (Msg, error) {
		v, ok := f.num(msg)
		if !ok {
			return nil, f.notNumber(KindScale)
		}
		return f.setNum(KindScale, msg, (v-add)/mul)
	}
	return compiledStep{kind: KindScale, field: f.key, run: run, inverse: inv}, nil
}

// unit: a conversion within one dimension (commands: the reverse conversion)
func compileUnit(s StepConfig) (compiledStep, error) {
	fwd, rev, err := conversion(s.From, s.To)
	if err != nil {
		return compiledStep{}, err
	}
	f, err := compileField(s.Field)
	if err != nil {
		return compiledStep{}, err
	}
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := f.num(msg)
		if !ok {
			return nil, RunOK, "", f.notNumber(KindUnit)
		}
		out, err := f.setNum(KindUnit, msg, fwd(v))
		return out, RunOK, "", err
	}
	inv := func(msg Msg, _ Context) (Msg, error) {
		v, ok := f.num(msg)
		if !ok {
			return nil, f.notNumber(KindUnit)
		}
		return f.setNum(KindUnit, msg, rev(v))
	}
	return compiledStep{kind: KindUnit, field: f.key, run: run, inverse: inv}, nil
}

// round: half away from zero
func compileRound(s StepConfig) (compiledStep, error) {
	if s.Decimals == nil {
		return compiledStep{}, errors.New("decimals is required")
	}
	if *s.Decimals < 0 || *s.Decimals > 6 {
		return compiledStep{}, errors.New("decimals must be 0-6")
	}
	f, err := compileField(s.Field)
	if err != nil {
		return compiledStep{}, err
	}
	factor := math.Pow10(*s.Decimals)
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := f.num(msg)
		if !ok {
			return nil, RunOK, "", f.notNumber(KindRound)
		}
		if math.Abs(v*factor) >= 1<<52 { // already has no more decimals than a float64 holds
			return msg, RunOK, "", nil
		}
		out, err := f.setNum(KindRound, msg, math.Round(v*factor)/factor)
		return out, RunOK, "", err
	}
	return compiledStep{kind: KindRound, field: f.key, run: run}, nil
}

// clamp: min ≤ v ≤ max
func compileClamp(s StepConfig) (compiledStep, error) {
	if s.Min == nil || s.Max == nil {
		return compiledStep{}, errors.New("min and max are required")
	}
	lo, hi := *s.Min, *s.Max
	if !finite(lo) || !finite(hi) {
		return compiledStep{}, errors.New("min and max must be finite numbers")
	}
	if lo > hi {
		return compiledStep{}, errors.New("min must be ≤ max")
	}
	f, err := compileField(s.Field)
	if err != nil {
		return compiledStep{}, err
	}
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := f.num(msg)
		if !ok {
			return nil, RunOK, "", f.notNumber(KindClamp)
		}
		out, err := f.setNum(KindClamp, msg, math.Min(math.Max(v, lo), hi))
		return out, RunOK, "", err
	}
	return compiledStep{kind: KindClamp, field: f.key, run: run}, nil
}

// map: the field's value (a string, a number or a boolean, compared by its
// text: 1000000, "ON", true) is replaced by the table's JSON value. Commands
// map back when no two keys share a value; keys that read as a number or
// true/false are sent back as that number or boolean.
func compileMap(s StepConfig) (compiledStep, error) {
	if len(s.Table) == 0 {
		return compiledStep{}, errors.New("table is required and must not be empty")
	}
	if len(s.Table) > MaxTableEntries {
		return compiledStep{}, fmt.Errorf("table must have at most %d entries", MaxTableEntries)
	}
	table := make(map[string]any, len(s.Table))
	rev := make(map[string]string, len(s.Table))
	oneToOne := true
	for k, raw := range s.Table {
		v, err := decodeJSON(raw)
		if err != nil {
			return compiledStep{}, fmt.Errorf("table[%q]: %w", k, err)
		}
		table[k] = v
		c := canonical(v)
		if _, dup := rev[c]; dup {
			oneToOne = false
		}
		rev[c] = k
	}
	var def any
	hasDef := s.Default != nil
	if hasDef {
		v, err := decodeJSON(*s.Default)
		if err != nil {
			return compiledStep{}, fmt.Errorf("default: %w", err)
		}
		def = v
	}
	f, err := compileField(s.Field)
	if err != nil {
		return compiledStep{}, err
	}
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		raw, ok := f.path.Get(msg)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "map", Reason: fmt.Sprintf("field %q is missing", f.name)}
		}
		key, ok := mapKey(raw)
		if !ok {
			return nil, RunOK, "", &ErrPipeline{Step: "map", Reason: fmt.Sprintf("field %q is not a string, number or boolean", f.name)}
		}
		mapped, ok := table[key]
		if !ok {
			if !hasDef {
				return nil, RunOK, "", &ErrPipeline{Step: "map", Reason: fmt.Sprintf("value %q is not in the table and there is no default", key)}
			}
			mapped = def
		}
		out, err := f.set(KindMap, msg, deepCopy(mapped))
		return out, RunOK, "", err
	}
	var inv func(Msg, Context) (Msg, error)
	if oneToOne {
		inv = func(msg Msg, _ Context) (Msg, error) {
			raw, ok := f.path.Get(msg)
			if !ok {
				return nil, &ErrPipeline{Step: "map", Reason: fmt.Sprintf("field %q is missing", f.name)}
			}
			key, ok := rev[canonical(raw)]
			if !ok {
				return nil, &ErrPipeline{Step: "map", Reason: fmt.Sprintf("%s is not a value of the table", canonical(raw))}
			}
			return f.set(KindMap, msg, keyValue(key))
		}
	}
	return compiledStep{kind: KindMap, field: f.key, run: run, inverse: inv}, nil
}

// deadband: drop a value whose change from the last published value is below
// every configured threshold; always pass after max_silence.
func compileDeadband(s StepConfig) (compiledStep, error) {
	if s.Abs == nil && s.Pct == nil {
		return compiledStep{}, errors.New("at least one of abs or pct is required")
	}
	for name, t := range map[string]*float64{"abs": s.Abs, "pct": s.Pct} {
		if t != nil && (!finite(*t) || *t <= 0) {
			return compiledStep{}, fmt.Errorf("%s must be a number > 0", name)
		}
	}
	maxSilence := DefaultMaxSilence
	if s.MaxSilence != nil {
		d, err := time.ParseDuration(*s.MaxSilence)
		if err != nil {
			return compiledStep{}, fmt.Errorf("max_silence: %w", err)
		}
		if d < MinMaxSilence {
			return compiledStep{}, fmt.Errorf("max_silence must be at least %v", MinMaxSilence)
		}
		maxSilence = d
	}
	f, err := compileField(s.Field)
	if err != nil {
		return compiledStep{}, err
	}
	abs, pct := s.Abs, s.Pct
	run := func(msg Msg, ctx Context) (Msg, RunResult, FilterReason, error) {
		v, ok := f.num(msg)
		if !ok || ctx.Last == nil {
			return msg, RunOK, "", nil // non-numbers pass; so does the first value
		}
		if !ctx.LastAt.IsZero() && ctx.Time.Sub(ctx.LastAt) >= maxSilence {
			return msg, RunOK, "", nil // heartbeat for quiet sensors
		}
		last, ok := f.num(ctx.Last)
		if !ok {
			return msg, RunOK, "", nil
		}
		delta := math.Abs(v - last)
		if abs != nil && delta >= *abs {
			return msg, RunOK, "", nil
		}
		if pct != nil {
			if last == 0 {
				if delta > 0 { // any change from 0 is an infinite relative change
					return msg, RunOK, "", nil
				}
			} else if delta/math.Abs(last)*100 >= *pct {
				return msg, RunOK, "", nil
			}
		}
		return nil, RunFiltered, FilterDeadband, nil
	}
	return compiledStep{kind: KindDeadband, field: f.key, maxSilence: maxSilence, run: run}, nil
}

// drop_if: drop when "field op value" holds; non-numbers pass.
func compileDropIf(s StepConfig) (compiledStep, error) {
	if s.Value == nil || !finite(*s.Value) {
		return compiledStep{}, errors.New("value is required and must be a finite number")
	}
	ops := map[string]func(float64, float64) bool{
		"<":  func(a, b float64) bool { return a < b },
		"<=": func(a, b float64) bool { return a <= b },
		">":  func(a, b float64) bool { return a > b },
		">=": func(a, b float64) bool { return a >= b },
		"==": func(a, b float64) bool { return a == b },
		"!=": func(a, b float64) bool { return a != b },
	}
	fn, ok := ops[s.Op]
	if !ok {
		return compiledStep{}, fmt.Errorf("op must be one of < <= > >= == !=, got %q", s.Op)
	}
	f, err := compileField(s.Field)
	if err != nil {
		return compiledStep{}, err
	}
	threshold := *s.Value
	run := func(msg Msg, _ Context) (Msg, RunResult, FilterReason, error) {
		v, ok := f.num(msg)
		if ok && fn(v, threshold) {
			return nil, RunFiltered, FilterDropIf, nil
		}
		return msg, RunOK, "", nil
	}
	return compiledStep{kind: KindDropIf, field: f.key, run: run}, nil
}

// script: transform(msg, ctx) returns a message, or null to drop it; an
// optional untransform(msg, ctx) converts commands. msg and ctx are handed
// over as fresh JS objects (a script can't reach or keep Go values), and the
// result must be an object of finite JSON values (a Date becomes a UTC
// string). Every run is a fresh sandbox that re-runs the script's top-level
// code, so keep top-level code to function declarations: setup there (a
// lookup table, say) is paid per message.
func compileScript(s StepConfig) (compiledStep, error) {
	if strings.TrimSpace(s.Source) == "" {
		return compiledStep{}, errors.New("source is required")
	}
	if len(s.Source) > MaxScriptSource {
		return compiledStep{}, fmt.Errorf("source exceeds %d KB", MaxScriptSource>>10)
	}
	script, err := jsbox.Compile("pipeline.js", s.Source)
	if err != nil {
		return compiledStep{}, fmt.Errorf("syntax: %w", err)
	}
	vm, stop, err := script.Load(MaxScriptDeadline, "pipeline script took longer than 20ms")
	if err != nil {
		return compiledStep{}, fmt.Errorf("load: %s", jsbox.Message(err))
	}
	_, hasTransform := goja.AssertFunction(vm.Get("transform"))
	_, hasUntransform := goja.AssertFunction(vm.Get("untransform"))
	stop()
	if !hasTransform {
		return compiledStep{}, errors.New("must define function transform(msg, ctx)")
	}
	run := func(msg Msg, ctx Context) (Msg, RunResult, FilterReason, error) {
		out, null, err := callScript(script, "transform", msg, ctx)
		switch {
		case err != nil:
			return nil, RunOK, "", err
		case null:
			return nil, RunFiltered, FilterScript, nil
		}
		return out, RunOK, "", nil
	}
	var inv func(Msg, Context) (Msg, error)
	if hasUntransform {
		inv = func(msg Msg, ctx Context) (Msg, error) {
			out, null, err := callScript(script, "untransform", msg, ctx)
			if err == nil && null {
				err = &ErrPipeline{Step: "script", Reason: "untransform returned null; a command can't be dropped"}
			}
			return out, err
		}
	}
	return compiledStep{kind: KindScript, run: run, inverse: inv}, nil
}

func callScript(script *jsbox.Script, name string, msg Msg, ctx Context) (out Msg, null bool, err error) {
	fail := func(reason string) (Msg, bool, error) {
		return nil, false, &ErrPipeline{Step: "script", Reason: name + ": " + reason}
	}
	defer func() {
		if r := recover(); r != nil {
			out, null, err = fail(fmt.Sprintf("panic: %v", r))
		}
	}()
	vm, stop, err := script.Load(MaxScriptDeadline, "pipeline script took longer than 20ms")
	if err != nil {
		return fail(jsbox.Message(err))
	}
	defer stop()
	sctx := vm.NewObject()
	_ = sctx.Set("element", ctx.Element)
	_ = sctx.Set("device", ctx.Device)
	_ = sctx.Set("time", ctx.Time.UTC().Format(time.RFC3339Nano))
	_ = sctx.Set("last", toJS(vm, ctx.Last))
	if ctx.LastAt.IsZero() {
		_ = sctx.Set("last_time", nil)
	} else {
		_ = sctx.Set("last_time", ctx.LastAt.UTC().Format(time.RFC3339Nano))
	}
	fn, _ := goja.AssertFunction(vm.Get(name))
	res, err := fn(goja.Undefined(), toJS(vm, msg), sctx)
	if err != nil {
		return fail(jsbox.Message(err))
	}
	switch {
	case goja.IsUndefined(res):
		return fail("returned undefined (missing return?)")
	case goja.IsNull(res):
		return nil, true, nil
	}
	v, err := fromJS(res.Export(), 0)
	if err != nil {
		return fail(err.Error())
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fail("must return an object or null")
	}
	if b, err := json.Marshal(m); err != nil {
		return fail(err.Error())
	} else if len(b) > MaxScriptOutput {
		return fail(fmt.Sprintf("the result is over %d KiB", MaxScriptOutput>>10))
	}
	return m, false, nil
}

// toJS builds fresh JS values from a decoded JSON value.
func toJS(vm *goja.Runtime, v any) goja.Value {
	switch x := v.(type) {
	case map[string]any:
		o := vm.NewObject()
		for k, e := range x {
			_ = o.Set(k, toJS(vm, e))
		}
		return o
	case []any:
		items := make([]any, len(x))
		for i, e := range x {
			items[i] = toJS(vm, e)
		}
		return vm.NewArray(items...)
	case json.Number:
		f, _ := x.Float64()
		return vm.ToValue(f)
	case nil:
		return goja.Null()
	}
	return vm.ToValue(v)
}

// fromJS checks an exported script result is a JSON value (finite numbers;
// no functions, symbols or other host values) and converts dates to UTC text.
func fromJS(v any, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("the result is nested too deeply")
	}
	switch x := v.(type) {
	case nil, bool, string, int64:
		return x, nil
	case float64:
		if !finite(x) {
			return nil, errors.New("the result contains NaN or Infinity")
		}
		return x, nil
	case time.Time:
		return x.UTC().Format("2006-01-02T15:04:05.000Z07:00"), nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			c, err := fromJS(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			c, err := fromJS(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	}
	if _, ok := v.(func(goja.FunctionCall) goja.Value); ok {
		return nil, errors.New("the result contains a function")
	}
	return nil, fmt.Errorf("the result contains a %T, not a JSON value", v)
}

// ------- fields -------

// field is a compiled field path (default "value").
type field struct {
	path mqttspec.Path
	name string // as written
	key  string // canonical, to compare fields
}

func compileField(f string) (field, error) {
	f = strings.TrimSpace(f)
	if f == "" {
		f = "value"
	}
	p, err := mqttspec.ParsePath(f)
	if err != nil {
		return field{}, fmt.Errorf("field: %w", err)
	}
	if len(p) == 0 {
		return field{}, errors.New("field: empty path")
	}
	return field{path: p, name: f, key: p.String()}, nil
}

func (f field) num(msg Msg) (float64, bool) {
	v, ok := f.path.Get(msg)
	if !ok {
		return 0, false
	}
	return toNumber(v)
}

func (f field) set(step StepKind, msg Msg, v any) (Msg, error) {
	out, ok := f.path.Set(msg, v)
	if m, isMap := out.(map[string]any); ok && isMap {
		return m, nil
	}
	return nil, &ErrPipeline{Step: string(step), Reason: fmt.Sprintf("can't set field %q", f.name)}
}

func (f field) setNum(step StepKind, msg Msg, v float64) (Msg, error) {
	if !finite(v) {
		return nil, &ErrPipeline{Step: string(step), Reason: fmt.Sprintf("field %q became %v", f.name, v)}
	}
	return f.set(step, msg, v)
}

func (f field) notNumber(step StepKind) error {
	return &ErrPipeline{Step: string(step), Reason: fmt.Sprintf("field %q is missing or not a number", f.name)}
}

// ------- helpers -------

func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func formatNumber(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// mapKey is the table key a field value looks up.
func mapKey(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case nil:
		return "null", true
	}
	if n, ok := toNumber(v); ok {
		return formatNumber(n), true
	}
	return "", false
}

// keyValue is the value a command gets back for a table key.
func keyValue(key string) any {
	switch key {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.ParseFloat(key, 64); err == nil && finite(n) && formatNumber(n) == key {
		return n
	}
	return key
}

// canonical renders a JSON value so that equal values (numbers by value,
// strings distinct from numbers) render the same.
func canonical(v any) string {
	b, err := json.Marshal(normalize(v))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	}
	if n, ok := toNumber(v); ok {
		return n
	}
	return v
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
}

func decodeJSON(raw []byte) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if !finiteJSON(v) {
		return nil, errors.New("NaN or Infinity")
	}
	return v, nil
}

func finiteJSON(v any) bool {
	switch x := v.(type) {
	case float64:
		return finite(x)
	case map[string]any:
		for _, e := range x {
			if !finiteJSON(e) {
				return false
			}
		}
	case []any:
		for _, e := range x {
			if !finiteJSON(e) {
				return false
			}
		}
	}
	return true
}
