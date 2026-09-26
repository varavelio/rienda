package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

// Exports is the module.exports value of one invocation.
type Exports struct {
	vm    *goja.Runtime
	value goja.Value
}

// Keys returns the names of the exported fields, in object order.
func (e *Exports) Keys() []string {
	obj := e.value.ToObject(e.vm)
	if obj == nil {
		return nil
	}
	return obj.Keys()
}

// Field returns the exported field with the given name, reporting whether it
// is present. A null or undefined field is reported as absent.
func (e *Exports) Field(name string) (Value, bool) {
	obj := e.value.ToObject(e.vm)
	if obj == nil {
		return Value{}, false
	}
	got := obj.Get(name)
	if got == nil || goja.IsUndefined(got) || goja.IsNull(got) {
		return Value{}, false
	}
	return Value{vm: e.vm, value: got}, true
}

// errNotCallable reports a value that cannot be called.
var errNotCallable = errors.New("value is not callable")

// Value is one value that crossed the bridge: a string, decoded JSON data or
// a callable.
type Value struct {
	vm    *goja.Runtime
	value goja.Value
}

// IsCallable reports whether the value can be called.
func (v Value) IsCallable() bool {
	fn, ok := goja.AssertFunction(v.value)
	return ok && fn != nil
}

// String returns the value when it is a string.
func (v Value) String() (string, bool) {
	if str, ok := v.value.Export().(string); ok {
		return str, true
	}
	return "", false
}

// JSON decodes the value into plain data: strings, numbers and booleans as
// themselves, objects and arrays as decoded JSON. It reports false for
// callables and for values that cannot cross.
func (v Value) JSON() (any, bool) {
	if v.IsCallable() {
		return nil, false
	}
	exported := v.value.Export()
	if exported == nil {
		return nil, false
	}
	if _, ok := exported.(func(goja.FunctionCall) goja.Value); ok {
		return nil, false
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

// Call invokes the callable with the given arguments and returns its return
// value. A throw becomes a Go error carrying the thrown message.
func (v Value) Call(ctx context.Context, args ...any) (result Value, err error) {
	fn, ok := goja.AssertFunction(v.value)
	if !ok {
		return Value{}, errNotCallable
	}
	converted := make([]goja.Value, 0, len(args))
	for _, arg := range args {
		converted = append(converted, v.vm.ToValue(arg))
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = Value{}
			err = callError(recovered)
		}
	}()
	returned, callErr := fn(goja.Undefined(), converted...)
	if callErr != nil {
		return Value{}, callError(callErr)
	}
	if returned == nil || goja.IsUndefined(returned) || goja.IsNull(returned) {
		return Value{}, nil
	}
	return Value{vm: v.vm, value: returned}, nil
}

// Undefined reports whether the value is absent: the zero Value, null or
// undefined.
func (v Value) Undefined() bool {
	return v.value == nil || goja.IsUndefined(v.value) || goja.IsNull(v.value)
}

// callError converts a throw or a goja failure into a plain error. A thrown
// Error contributes its message without any stack or host path; a thrown
// string contributes itself.
func callError(recovered any) error {
	switch value := recovered.(type) {
	case string:
		return fmt.Errorf("%s", value)
	case *goja.Exception:
		if thrown := value.Value(); thrown != nil && !goja.IsUndefined(thrown) {
			if obj, ok := thrown.(*goja.Object); ok {
				if message := obj.Get("message"); message != nil && !goja.IsUndefined(message) {
					if text, ok := message.Export().(string); ok && text != "" {
						return fmt.Errorf("%s", text)
					}
				}
			}
			if text := thrown.String(); text != "" {
				return fmt.Errorf("%s", text)
			}
		}
		return fmt.Errorf("%s", value.Error())
	case *goja.InterruptedError:
		return context.Canceled
	case error:
		return value
	default:
		return fmt.Errorf("%v", value)
	}
}
