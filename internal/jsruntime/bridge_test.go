package jsruntime

import (
	"testing"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/require"
)

// TestExports verifies reading the fields of a compiled module exports object.
func TestExports(t *testing.T) {
	newExports := func(t *testing.T, source string) *Exports {
		t.Helper()
		vm := goja.New()
		value, err := vm.RunString(source)
		require.NoError(t, err)
		return &Exports{vm: vm, value: value}
	}

	t.Run("lists the exported keys in object order", func(t *testing.T) {
		exports := newExports(t, `({ execute: function(){}, name: "read" })`)

		require.Equal(t, []string{"execute", "name"}, exports.Keys())
	})

	t.Run("reads a present field", func(t *testing.T) {
		exports := newExports(t, `({ name: "read" })`)

		value, ok := exports.Field("name")

		require.True(t, ok)
		require.Equal(t, "read", value.value.Export())
	})

	t.Run("reports absent, null and undefined fields as missing", func(t *testing.T) {
		exports := newExports(t, `({ nil: null, undef: undefined })`)

		_, ok := exports.Field("nil")
		require.False(t, ok)
		_, ok = exports.Field("undef")
		require.False(t, ok)
		_, ok = exports.Field("missing")
		require.False(t, ok)
	})

	t.Run("reports a non-object export as holding no keys", func(t *testing.T) {
		vm := goja.New()
		require.NoError(t, vm.Set("exports", goja.Undefined()))
		value, err := vm.RunString("undefined")
		require.NoError(t, err)
		exports := &Exports{vm: vm, value: value}

		require.Nil(t, exports.Keys())
		_, ok := exports.Field("anything")
		require.False(t, ok)
	})
}

// TestValue verifies the values that cross the bridge.
func TestValue(t *testing.T) {
	newValue := func(t *testing.T, source string) Value {
		t.Helper()
		vm := goja.New()
		value, err := vm.RunString(source)
		require.NoError(t, err)
		return Value{vm: vm, value: value}
	}

	t.Run("exports a string", func(t *testing.T) {
		value := newValue(t, `"hello"`)

		text, ok := value.String()

		require.True(t, ok)
		require.Equal(t, "hello", text)
	})

	t.Run("reports a non-string as not a string", func(t *testing.T) {
		value := newValue(t, `42`)

		_, ok := value.String()

		require.False(t, ok)
	})

	t.Run("decodes objects and arrays into plain data", func(t *testing.T) {
		value := newValue(t, `({ a: 1, b: [true, null] })`)

		decoded, ok := value.JSON()

		require.True(t, ok)
		require.Equal(t, map[string]any{"a": float64(1), "b": []any{true, nil}}, decoded)
	})

	t.Run("reports callables and undefined as not decodable", func(t *testing.T) {
		callable := newValue(t, `(function(){})`)
		_, ok := callable.JSON()
		require.False(t, ok)

		undefined := newValue(t, `undefined`)
		_, ok = undefined.JSON()
		require.False(t, ok)
	})

	t.Run("detects callables", func(t *testing.T) {
		require.True(t, newValue(t, `(function(){})`).IsCallable())
		require.False(t, newValue(t, `42`).IsCallable())
	})
}
