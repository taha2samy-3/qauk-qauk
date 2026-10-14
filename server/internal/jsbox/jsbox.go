// Package jsbox runs admin-written JavaScript (MQTT decoders, pipeline
// scripts) in a fresh goja runtime: plain ECMAScript with no modules, timers,
// network or files, interrupted after a deadline, with a bounded call stack.
//
// goja has no memory limit. The deadline stops loops between instructions,
// but one native call can allocate a lot at once, so the native primitives a
// script references (String repeat/padStart/padEnd, Array fill/join,
// ArrayBuffer and typed arrays) are wrapped with size checks. Only the ones
// that appear in the source are wrapped: goja builds its builtins lazily and
// wrapping all of them would cost ~150 µs per run. Reaching a primitive by a
// computed name (s["re" + "peat"]) skips its guard, and a script can still
// grow values gradually within the deadline; the guards catch mistakes, they
// are not a boundary against a hostile admin.
package jsbox

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const (
	// MaxStringLen bounds strings made by repeat, padStart, padEnd and join.
	MaxStringLen = 1 << 20
	// MaxArrayLen bounds fill and join, and typed array lengths. new Array(n)
	// is sparse in goja and needs no bound.
	MaxArrayLen = 100_000
	// MaxBufferBytes bounds ArrayBuffer sizes.
	MaxBufferBytes = 1 << 20
	// MaxCallStack bounds JS recursion, including recursion through native
	// callbacks such as [].map.
	MaxCallStack = 256
)

var typedArrays = []string{"Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array",
	"Int32Array", "Uint32Array", "Float32Array", "Float64Array"}

// Script is compiled source plus the guards it needs.
type Script struct {
	prog   *goja.Program
	guards *goja.Program // nil: the source uses no guarded primitive
}

// Compile compiles source (name is used in error positions).
func Compile(name, source string) (*Script, error) {
	prog, err := goja.Compile(name, source, false)
	if err != nil {
		return nil, err
	}
	s := &Script{prog: prog}
	if g := guardSource(source); g != "" {
		s.guards = goja.MustCompile("jsbox-guards.js", g, false)
	}
	return s, nil
}

// Load starts a runtime that is interrupted after deadline with reason, and
// runs the script's top-level code in it. Call stop when done. A panic inside
// goja becomes an error.
func (s *Script) Load(deadline time.Duration, reason string) (vm *goja.Runtime, stop func(), err error) {
	defer func() {
		if r := recover(); r != nil {
			if stop != nil {
				stop()
			}
			vm, stop, err = nil, nil, fmt.Errorf("panic: %v", r)
		}
	}()
	vm = goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	vm.SetMaxCallStackSize(MaxCallStack)
	timer := time.AfterFunc(deadline, func() { vm.Interrupt(reason) })
	stop = func() { timer.Stop() }
	if s.guards != nil {
		if _, err = vm.RunProgram(s.guards); err != nil {
			stop()
			return nil, nil, fmt.Errorf("sandbox: %w", err)
		}
	}
	if _, err = vm.RunProgram(s.prog); err != nil {
		stop()
		return nil, nil, err
	}
	return vm, stop, nil
}

// guardSource returns the guard code for the primitives source mentions.
func guardSource(source string) string {
	var b strings.Builder
	for _, m := range []struct{ name, tooBig string }{
		{"repeat", "String(this).length * Number(a[0]) > MAXS"},
		{"padStart", "Number(a[0]) > MAXS"},
		{"padEnd", "Number(a[0]) > MAXS"},
	} {
		if strings.Contains(source, m.name) {
			fmt.Fprintf(&b, "patch(String.prototype, %q, function (a) { if (%s) tooBig(%q); });\n", m.name, m.tooBig, m.name)
		}
	}
	for _, m := range []string{"fill", "join"} {
		if strings.Contains(source, m) {
			fmt.Fprintf(&b, "patch(Array.prototype, %q, function () { if (this.length > MAXA) tooBig(\"array\"); });\n", m)
		}
	}
	if strings.Contains(source, "ArrayBuffer") {
		b.WriteString("wrap(\"ArrayBuffer\", MAXB);\n")
	}
	for _, t := range typedArrays {
		if strings.Contains(source, t) {
			fmt.Fprintf(&b, "wrap(%q, MAXA);\n", t)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return fmt.Sprintf(`(function (g) {
	var MAXS = %d, MAXA = %d, MAXB = %d;
	function tooBig(what) { throw new RangeError(what + " is too large for the sandbox"); }
	function patch(proto, name, check) {
		var orig = proto[name];
		proto[name] = function () { check.call(this, arguments); return orig.apply(this, arguments); };
	}
	function wrap(name, max) {
		var T = g[name];
		var W = function (n) {
			if (typeof n === "number" && n > max) tooBig(name);
			return arguments.length === 0 ? new T() : new T(n, arguments[1], arguments[2]);
		};
		Object.setPrototypeOf(W, T);
		W.prototype = T.prototype;
		g[name] = W;
	}
%s})(this);`, MaxStringLen, MaxArrayLen, MaxBufferBytes, b.String())
}

// Message turns a goja error into a short message (the exception text, the
// interrupt reason for a timeout).
func Message(err error) string {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return ex.Error()
	}
	var in *goja.InterruptedError
	if errors.As(err, &in) {
		return fmt.Sprintf("%v", in.Value())
	}
	var so *goja.StackOverflowError
	if errors.As(err, &so) {
		return "call stack too deep"
	}
	return err.Error()
}
