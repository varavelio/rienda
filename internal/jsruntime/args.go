package jsruntime

import (
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"
)

// jsonEncode encodes plain data.
func jsonEncode(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("jsruntime: encode value: %w", err)
	}
	return encoded, nil
}

// jsonDecode decodes plain data.
func jsonDecode(data []byte) (any, error) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("jsruntime: decode value: %w", err)
	}
	return decoded, nil
}

// argumentAt returns the argument at the given index, if present.
func argumentAt(call goja.FunctionCall, index int) (goja.Value, bool) {
	if index < 0 || index >= len(call.Arguments) {
		return nil, false
	}
	arg := call.Argument(index)
	if arg == nil || goja.IsUndefined(arg) || goja.IsNull(arg) {
		return nil, false
	}
	return arg, true
}

// requiredString reads a required string argument.
func requiredString(
	rt *Runtime,
	primitive string,
	call goja.FunctionCall,
	index int,
	field string,
) string {
	arg, ok := argumentAt(call, index)
	if !ok {
		rt.raise("%s: %s is required", primitive, field)
	}
	exported := arg.Export()
	text, ok := exported.(string)
	if !ok {
		rt.raise("%s: %s must be a string", primitive, field)
	}
	return text
}

// requiredNumber reads a required numeric argument.
func requiredNumber(
	rt *Runtime,
	primitive string,
	call goja.FunctionCall,
	index int,
	field string,
) float64 {
	arg, ok := argumentAt(call, index)
	if !ok {
		rt.raise("%s: %s is required", primitive, field)
	}
	number, ok := toFloat(arg.Export())
	if !ok {
		rt.raise("%s: %s must be a number", primitive, field)
	}
	return number
}

// requiredObject reads a required object argument as plain data.
func requiredObject(
	rt *Runtime,
	primitive string,
	call goja.FunctionCall,
	index int,
	field string,
) map[string]any {
	arg, ok := argumentAt(call, index)
	if !ok {
		rt.raise("%s: %s is required", primitive, field)
	}
	table, ok := toObject(arg)
	if !ok {
		rt.raise("%s: %s must be an object", primitive, field)
	}
	return table
}

// optionalObject reads an optional object argument, returning an empty object
// when absent and rejecting unknown fields against the known set. An empty
// known set accepts any field.
func optionalObject(
	rt *Runtime,
	primitive string,
	call goja.FunctionCall,
	index int,
	known ...string,
) map[string]any {
	arg, ok := argumentAt(call, index)
	if !ok {
		return map[string]any{}
	}
	table, ok := toObject(arg)
	if !ok {
		rt.raise("%s: options must be an object", primitive)
	}
	if len(known) > 0 {
		allowed := make(map[string]bool, len(known))
		for _, k := range known {
			allowed[k] = true
		}
		for k := range table {
			if !allowed[k] {
				rt.raise("%s: unknown option %q", primitive, k)
			}
		}
	}
	return table
}

// toObject converts a goja value into plain object data.
func toObject(value goja.Value) (map[string]any, bool) {
	exported := value.Export()
	switch table := exported.(type) {
	case map[string]any:
		return table, true
	case map[string]string:
		out := make(map[string]any, len(table))
		for k, v := range table {
			out[k] = v
		}
		return out, true
	default:
		return nil, false
	}
}

// stringField reads a required string field of an object.
func stringField(obj map[string]any, field string) (string, bool) {
	raw, ok := obj[field]
	if !ok || raw == nil {
		return "", false
	}
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	return text, true
}

// toFloat converts plain numeric data into a float.
func toFloat(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case float64:
		return number, true
	case float32:
		return float64(number), true
	default:
		return 0, false
	}
}
