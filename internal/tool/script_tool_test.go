package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/jsruntime"
	"github.com/varavelio/rienda/internal/llm"
)

// writeUserTool writes a user tool module and returns its path.
func writeUserTool(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name, "index.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func newUserTool(t *testing.T, name, body string) *ScriptTool {
	t.Helper()
	module, err := jsruntime.Compile(writeUserTool(t, name, body))
	require.NoError(t, err)
	tool, err := NewScriptTool(ScriptToolOptions{Name: name, Module: module, Workdir: t.TempDir()})
	require.NoError(t, err)
	return tool
}

const probeModule = `module.exports = {
  description: "A probe tool.",
  parameters: {type: "object", properties: {path: {type: "string"}}, required: ["path"], additionalProperties: false},
  execute: function(ctx, args) { return "got " + args.path; },
};`

func runUserTool(t *testing.T, tool *ScriptTool, arguments string) (Result, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	result, err := tool.Execute(
		t.Context(),
		Call{ID: "call_1", Name: tool.Definition().Name, Arguments: json.RawMessage(arguments)},
		sink,
	)
	require.NoError(t, err)
	return result, sink
}

func TestScriptTool(t *testing.T) {
	t.Run("takes the definition from the module", func(t *testing.T) {
		tool := newUserTool(t, "probe", probeModule)
		definition := tool.Definition()
		require.Equal(t, "probe", definition.Name)
		require.Equal(t, "A probe tool.", definition.Description)
		require.Contains(t, string(definition.Parameters), `"path"`)
	})

	t.Run("returns a string", func(t *testing.T) {
		tool := newUserTool(t, "probe", probeModule)
		result, _ := runUserTool(t, tool, `{"path":"a.txt"}`)
		require.False(t, result.IsError)
		require.Equal(t, "got a.txt", result.Blocks[0].Text)
	})

	t.Run("returns an explicit object", func(t *testing.T) {
		tool := newUserTool(t, "probe", `module.exports = {
  description: "P.", parameters: {type: "object"},
  execute: function(ctx, args) { return {text: "bad", isError: true}; },
};`)
		result, _ := runUserTool(t, tool, `{}`)
		require.True(t, result.IsError)
		require.Equal(t, "bad", result.Blocks[0].Text)
	})

	t.Run("throw becomes an error result", func(t *testing.T) {
		tool := newUserTool(t, "probe", `module.exports = {
  description: "P.", parameters: {type: "object"},
  execute: function(ctx, args) { throw new Error("kaput"); },
};`)
		result, _ := runUserTool(t, tool, `{}`)
		require.True(t, result.IsError)
		require.Equal(t, "kaput", result.Blocks[0].Text)
	})

	t.Run("wrong shapes become error results", func(t *testing.T) {
		for _, body := range []string{
			`return 42;`, `return true;`, `return [1];`, `return null;`, `return;`,
			`return {};`, `return {text: 1};`, `return {text: "x", isError: "y"};`,
		} {
			tool := newUserTool(t, "probe", `module.exports = {
  description: "P.", parameters: {type: "object"},
  execute: function(ctx, args) { `+body+` },
};`)
			result, _ := runUserTool(t, tool, `{}`)
			require.True(t, result.IsError, body)
			require.Contains(t, result.Blocks[0].Text, "string or { text, isError }", body)
		}
	})

	t.Run("malformed arguments never reach the runtime", func(t *testing.T) {
		tool := newUserTool(t, "probe", probeModule)
		sink := &recordingSink{}
		for _, raw := range []string{`{"path":`, `[1]`, `{} {}`} {
			_, err := tool.Execute(
				t.Context(),
				Call{ID: "c", Name: "probe", Arguments: json.RawMessage(raw)},
				sink,
			)
			require.Error(t, err, raw)
		}
	})

	t.Run("leaves no host path in errors", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "probe", "index.js")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(probeModule), 0o600))
		module, err := jsruntime.Compile(path)
		require.NoError(t, err)
		tool, err := NewScriptTool(ScriptToolOptions{Name: "probe", Module: module})
		require.NoError(t, err)
		_, err = tool.Execute(
			t.Context(),
			Call{ID: "c", Name: "probe", Arguments: json.RawMessage(`{bad`)},
			&recordingSink{},
		)
		require.Error(t, err)
		require.NotContains(t, err.Error(), dir)
	})

	t.Run("rejects a broken module", func(t *testing.T) {
		for name, body := range map[string]string{
			"no description": `module.exports = {parameters: {type: "object"}, execute: function() {}};`,
			"no parameters":  `module.exports = {description: "P.", execute: function() {}};`,
			"no execute":     `module.exports = {description: "P.", parameters: {type: "object"}};`,
			"static execute": `module.exports = {description: "P.", parameters: {type: "object"}, execute: 42};`,
			"invalid name":   probeModule,
			"invalid schema": `module.exports = {description: "P.", parameters: "nope", execute: function() {}};`,
		} {
			toolName := "probe"
			if name == "invalid name" {
				toolName = "no good"
			}
			module, err := jsruntime.Compile(writeUserTool(t, "probe", body))
			require.NoError(t, err)
			_, err = NewScriptTool(ScriptToolOptions{Name: toolName, Module: module})
			require.Error(t, err, name)
		}
	})

	t.Run("streams ctx.log to the sink", func(t *testing.T) {
		tool := newUserTool(t, "probe", `module.exports = {
  description: "P.", parameters: {type: "object"},
  execute: function(ctx, args) { ctx.log("live"); return "done"; },
};`)
		result, sink := runUserTool(t, tool, `{}`)
		require.Equal(t, "done", result.Blocks[0].Text)
		require.Contains(t, sink.text(), "live")
	})

	t.Run("caps the result text", func(t *testing.T) {
		tool := newUserTool(t, "probe", `module.exports = {
  description: "P.", parameters: {type: "object"},
  execute: function(ctx, args) { return "x".repeat(300000); },
};`)
		result, _ := runUserTool(t, tool, `{}`)
		require.LessOrEqual(t, len(result.Blocks[0].Text), (256<<10)+64)
		require.Contains(t, result.Blocks[0].Text, "[output truncated]")
	})

	t.Run("does not see script mutations", func(t *testing.T) {
		tool := newUserTool(t, "probe", `module.exports = {
  description: "P.", parameters: {type: "object", properties: {path: {type: "string"}}},
  execute: function(ctx, args) { args.path = "changed"; return args.path; },
};`)
		result, _ := runUserTool(t, tool, `{"path":"orig"}`)
		require.Equal(t, "changed", result.Blocks[0].Text)
		result, _ = runUserTool(t, tool, `{"path":"orig"}`)
		require.Equal(t, "changed", result.Blocks[0].Text)
	})
}

func TestOverride(t *testing.T) {
	t.Run("replaces the definition and the execution", func(t *testing.T) {
		shell, err := NewShell(ShellOptions{})
		require.NoError(t, err)
		registry, err := NewRegistry(shell)
		require.NoError(t, err)

		user := newUserTool(t, "shell", `module.exports = {
  description: "User shell.", parameters: {type: "object"},
  execute: function(ctx, args) { return "user"; },
};`)
		require.NoError(t, registry.Override(user))

		definitions, err := registry.Definitions([]string{"shell"})
		require.NoError(t, err)
		require.Len(t, definitions, 1)
		require.Equal(t, "User shell.", definitions[0].Description)

		executor, ok := registry.Lookup("shell")
		require.True(t, ok)
		result, err := executor.Execute(
			t.Context(),
			Call{Arguments: json.RawMessage(`{}`)},
			&recordingSink{},
		)
		require.NoError(t, err)
		require.Equal(t, "user", result.Blocks[0].Text)
	})

	t.Run("behaves like register on a free name", func(t *testing.T) {
		registry, err := NewRegistry()
		require.NoError(t, err)
		user := newUserTool(t, "mine", probeModule)
		require.NoError(t, registry.Override(user))
		_, ok := registry.Lookup("mine")
		require.True(t, ok)
	})

	t.Run("validates like register", func(t *testing.T) {
		registry, err := NewRegistry()
		require.NoError(t, err)
		require.Error(t, registry.Override(nil))
		bad := &fakeDefinitionTool{name: "no good", parameters: json.RawMessage(`{}`)}
		require.Error(t, registry.Override(bad))
	})
}

// fakeDefinitionTool is a tool with a fixed definition for validation tests.
type fakeDefinitionTool struct {
	name       string
	parameters json.RawMessage
}

func (f *fakeDefinitionTool) Definition() llm.Tool {
	return llm.Tool{Name: f.name, Description: "Fake.", Parameters: f.parameters}
}

func (f *fakeDefinitionTool) Execute(_ context.Context, _ Call, _ Sink) (Result, error) {
	return TextResult("fake"), nil
}

// TestScriptToolReachesTheStores verifies a tool's script can reach the
// shared cache and the permanent store its options name, with the runtime
// owning the time to live.
func TestScriptToolReachesTheStores(t *testing.T) {
	module, err := jsruntime.Compile(writeUserTool(t, "storeprobe", `module.exports = {
  description: "A store probe.",
  parameters: {type: "object", properties: {}, additionalProperties: false},
  execute: function(ctx) {
    var expires = ctx.cache.set("probe", "cached text", 30);
    var stored = ctx.store.set("probe", "stored text");
    return "cache=" + ctx.cache.get("probe") + " store=" + ctx.store.get("probe") +
      " expiry=" + (typeof expires === "string" && expires !== "") +
      " eternal=" + (stored === "");
  },
};`))
	require.NoError(t, err)
	tool, err := NewScriptTool(ScriptToolOptions{
		Name:     "storeprobe",
		Module:   module,
		Workdir:  t.TempDir(),
		CacheDir: t.TempDir(),
		StoreDir: t.TempDir(),
	})
	require.NoError(t, err)

	result, _ := runUserTool(t, tool, "{}")
	require.False(t, result.IsError)
	require.Len(t, result.Blocks, 1)
	require.Equal(
		t,
		"cache=cached text store=stored text expiry=true eternal=true",
		result.Blocks[0].Text,
	)
}
