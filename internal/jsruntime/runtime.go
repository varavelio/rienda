package jsruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
)

// canceledMessage is the JavaScript-visible error raised when the run is
// canceled while a primitive blocks.
const canceledMessage = "invocation canceled"

// Stream names where streamed bytes came from.
type Stream string

const (
	// StreamStdout carries ctx.log output and standard output of commands.
	StreamStdout Stream = "stdout"

	// StreamStderr carries standard error of commands.
	StreamStderr Stream = "stderr"
)

// Module is a compiled script, safe to share between invocations.
type Module struct {
	program *goja.Program
	name    string
}

// Compile reads, parses and compiles a script file. A syntax error is a load
// error and never a run error.
func Compile(path string) (*Module, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller selects the extension file.
	if err != nil {
		return nil, fmt.Errorf("extension %q: %w", displayName(path), err)
	}
	program, err := goja.Compile(displayName(path)+"/index.js", string(data), false)
	if err != nil {
		return nil, fmt.Errorf("extension %q: %w", displayName(path), sanitize(path, err))
	}
	return &Module{program: program, name: displayName(path)}, nil
}

// Name returns the display name of the extension the module was compiled from.
func (m *Module) Name() string {
	return m.name
}

// displayName reports the extension name for a script path: the name of the
// directory that holds index.js. It never contains a host path, so errors
// that carry it stay free of host details.
func displayName(path string) string {
	return filepath.Base(filepath.Dir(path))
}

// sanitize removes the script path from an error, so host paths never reach
// the model or the front end.
func sanitize(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), path, displayName(path)))
}

// Options holds the static settings of an extension, resolved once when it is
// loaded.
type Options struct {
	// Workdir is the absolute workspace directory: ctx.workdir and the base
	// of relative paths.
	Workdir string

	// Config is the global configuration as plain data: ctx.config.
	Config any
}

// Runtime is the execution context of one invocation. It owns the goja
// virtual machine together with the static options the ctx primitives read.
// The invocation context travels as a parameter, never as a field, so a
// Runtime never outlives the call it belongs to.
type Runtime struct {
	vm   *goja.Runtime
	opts Options
	out  func(Stream, []byte)
}

// Invoke runs the script in a fresh runtime and hands its exports to fn,
// streaming whatever the script writes to out, which may be nil. It honors
// ctx cancellation and imposes no timeout.
func (m *Module) Invoke(
	ctx context.Context,
	opts Options,
	out func(Stream, []byte),
	fn func(rt *Runtime, exports *Exports) error,
) (err error) {
	vm := goja.New()
	rt := &Runtime{vm: vm, opts: opts, out: out}

	// A canceled run interrupts even a pure JavaScript loop: the interrupt
	// surfaces as a panic carrying a plain string, converted below.
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(canceledMessage) })
	defer stop()

	defer func() {
		if recovered := recover(); recovered != nil {
			err = m.invocationError(recovered)
		}
	}()

	module := vm.NewObject()
	exports := vm.NewObject()
	if err := module.Set("exports", exports); err != nil {
		return fmt.Errorf("extension %q: %w", m.name, err)
	}
	if err := vm.Set("module", module); err != nil {
		return fmt.Errorf("extension %q: %w", m.name, err)
	}

	if _, err := vm.RunProgram(m.program); err != nil {
		return m.invocationError(err)
	}

	resolved := module.Get("exports")
	if resolved == nil {
		return fmt.Errorf("extension %q: module.exports is missing", m.name)
	}

	if err := installContext(rt, ctx); err != nil {
		return fmt.Errorf("extension %q: %w", m.name, err)
	}

	wrapped := &Exports{vm: vm, value: resolved}
	if fn != nil {
		if err := fn(rt, wrapped); err != nil {
			return err
		}
	}
	return nil
}

// invocationError converts a panic or a goja failure into a plain error,
// reporting cancellation as the documented message.
func (m *Module) invocationError(recovered any) error {
	switch value := recovered.(type) {
	case string:
		if value == canceledMessage {
			return context.Canceled
		}
		return fmt.Errorf("extension %q: %s", m.name, value)
	case *goja.Exception:
		if value.Error() == canceledMessage {
			return context.Canceled
		}
		return fmt.Errorf("extension %q: %s", m.name, value.Error())
	case *goja.InterruptedError:
		return context.Canceled
	case error:
		if strings.Contains(value.Error(), canceledMessage) {
			return context.Canceled
		}
		return fmt.Errorf("extension %q: %s", m.name, value.Error())
	default:
		return fmt.Errorf("extension %q: %v", m.name, value)
	}
}

// ContextValue returns the ctx object of the invocation, for callers that pass
// it to an exported function.
func (rt *Runtime) ContextValue() any {
	return rt.vm.Get("ctx")
}

// emit streams data produced on stream, dropping it when no sink is attached.
func (rt *Runtime) emit(stream Stream, data []byte) {
	if rt.out == nil || len(data) == 0 {
		return
	}
	kept := make([]byte, len(data))
	copy(kept, data)
	rt.out(stream, kept)
}

// raise panics with a JavaScript-visible error, aborting the running script.
func (rt *Runtime) raise(format string, args ...any) {
	panic(rt.vm.ToValue(fmt.Sprintf(format, args...)))
}

// canceled raises when the run was canceled.
func (rt *Runtime) canceled(ctx context.Context) {
	if ctx.Err() != nil {
		panic(rt.vm.ToValue(canceledMessage))
	}
}
