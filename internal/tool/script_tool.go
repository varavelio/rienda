package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/varavelio/rienda/internal/jsruntime"
	"github.com/varavelio/rienda/internal/llm"
)

// scriptResultCap caps the model-facing text of a script tool, matching the
// command output cap.
const scriptResultCap = defaultShellMaxOutput

// ScriptTool is a tool implemented by a user JavaScript module.
type ScriptTool struct {
	name        string
	description string
	parameters  json.RawMessage
	module      *jsruntime.Module
	workdir     string
	config      any
	maxOutput   int
	cacheDir    string
	storeDir    string
}

// ScriptToolOptions configures a ScriptTool.
type ScriptToolOptions struct {
	// Name is the tool name, from the extension directory.
	Name string

	// Module is the compiled script. It is required.
	Module *jsruntime.Module

	// Workdir is the base directory handed to the runtime.
	Workdir string

	// Config is the configuration handed to ctx.config.
	Config any

	// MaxOutput caps the model-facing result text. Zero means the default.
	MaxOutput int

	// CacheDir is the shared cache handed to ctx.cache when set.
	CacheDir string

	// StoreDir is the permanent store handed to ctx.store when set.
	StoreDir string
}

// NewScriptTool validates the module exports and builds the tool.
func NewScriptTool(opts ScriptToolOptions) (*ScriptTool, error) {
	if opts.Module == nil {
		return nil, fmt.Errorf("tool: tool %q requires a module", opts.Name)
	}
	maxOutput := opts.MaxOutput
	if maxOutput <= 0 {
		maxOutput = scriptResultCap
	}
	tool := &ScriptTool{
		name:      opts.Name,
		module:    opts.Module,
		workdir:   opts.Workdir,
		config:    opts.Config,
		maxOutput: maxOutput,
		cacheDir:  opts.CacheDir,
		storeDir:  opts.StoreDir,
	}
	if err := tool.load(); err != nil {
		return nil, err
	}
	switch {
	case !ValidName(tool.name):
		return nil, fmt.Errorf("tool: invalid tool name %q", tool.name)
	case strings.TrimSpace(tool.description) == "":
		return nil, fmt.Errorf("tool: tool %q requires a description", tool.name)
	case !validSchema(tool.parameters):
		return nil, fmt.Errorf(
			"tool: tool %q requires a valid JSON Schema in Parameters",
			tool.name,
		)
	}
	return tool, nil
}

// runtimeOptions builds the options every invocation of the tool runs on:
// the static settings the tool was loaded with, stores included.
func (t *ScriptTool) runtimeOptions() jsruntime.Options {
	return jsruntime.Options{
		Workdir:  t.workdir,
		Config:   t.config,
		CacheDir: t.cacheDir,
		StoreDir: t.storeDir,
	}
}

// load reads the description, parameters and execute function of the module.
func (t *ScriptTool) load() error {
	ctx := context.Background()
	err := t.module.Invoke(ctx, t.runtimeOptions(), nil,
		func(rt *jsruntime.Runtime, exports *jsruntime.Exports) error {
			if description, ok := exports.Field("description"); ok {
				if text, ok := description.String(); ok {
					t.description = text
				}
			}
			if parameters, ok := exports.Field("parameters"); ok {
				if data, ok := parameters.JSON(); ok {
					encoded, err := json.Marshal(data)
					if err == nil {
						t.parameters = encoded
					}
				}
			}
			execute, ok := exports.Field("execute")
			if !ok || !execute.IsCallable() {
				return fmt.Errorf("tool: tool %q requires an execute function", t.name)
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("tool: %w", err)
	}
	return nil
}

// Definition returns the tool definition shown to the model.
func (t *ScriptTool) Definition() llm.Tool {
	return llm.Tool{
		Name:        t.name,
		Description: t.description,
		Parameters:  t.parameters,
	}
}

// Execute runs one invocation of the script.
func (t *ScriptTool) Execute(ctx context.Context, call Call, out Sink) (Result, error) {
	var raw map[string]any
	if err := decodeArguments(call.Arguments, &raw); err != nil {
		return Result{}, fmt.Errorf("tool: %w", err)
	}
	var result Result
	err := t.module.Invoke(ctx, t.runtimeOptions(),
		func(stream jsruntime.Stream, data []byte) {
			if out == nil {
				return
			}
			switch stream {
			case jsruntime.StreamStderr:
				out.Emit(StreamStderr, data)
			default:
				out.Emit(StreamStdout, data)
			}
		}, func(rt *jsruntime.Runtime, exports *jsruntime.Exports) error {
			execute, ok := exports.Field("execute")
			if !ok || !execute.IsCallable() {
				return fmt.Errorf("tool: tool %q requires an execute function", t.name)
			}
			args, err := toPlain(raw)
			if err != nil {
				return err
			}
			returned, err := execute.Call(ctx, rtVMContext(rt), args)
			if err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("tool: command canceled: %w", ctx.Err())
				}
				result = ErrorResult(err.Error())
				return nil
			}
			result = t.toResult(returned)
			return nil
		})
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("tool: command canceled: %w", ctx.Err())
		}
		return Result{}, fmt.Errorf("tool: %w", err)
	}
	return result, nil
}

// toResult converts a script return value into a tool result. A string is the
// success text, an object with a text string and an optional isError boolean
// is the explicit result, and anything else is an error result naming the
// accepted shapes.
func (t *ScriptTool) toResult(returned jsruntime.Value) Result {
	if returned.Undefined() {
		return ErrorResult("tool: execute must return a string or { text, isError }, got undefined")
	}
	if text, ok := returned.String(); ok {
		return TextResult(truncateForModel(text, t.maxOutput))
	}
	data, ok := returned.JSON()
	if !ok {
		return ErrorResult("tool: execute must return a string or { text, isError }")
	}
	doc, ok := data.(map[string]any)
	if !ok {
		return ErrorResult("tool: execute must return a string or { text, isError }")
	}
	rawText, ok := doc["text"]
	if !ok {
		return ErrorResult("tool: execute must return a string or { text, isError }")
	}
	text, ok := rawText.(string)
	if !ok {
		return ErrorResult("tool: execute must return a string or { text, isError }")
	}
	isError := false
	if rawError, ok := doc["isError"]; ok && rawError != nil {
		flag, ok := rawError.(bool)
		if !ok {
			return ErrorResult("tool: execute must return a string or { text, isError }")
		}
		isError = flag
	}
	text = truncateForModel(text, t.maxOutput)
	if isError {
		return ErrorResult(text)
	}
	return TextResult(text)
}

// validSchema reports whether schema is a non-empty JSON object.
func validSchema(schema json.RawMessage) bool {
	if len(schema) == 0 || !json.Valid(schema) {
		return false
	}
	var object map[string]any
	if err := json.Unmarshal(schema, &object); err != nil {
		return false
	}
	return true
}

// truncateForModel caps text rune-safely.
func truncateForModel(text string, maxBytes int) string {
	out, cut := truncateUTF8(text, maxBytes)
	if cut {
		out += "\n[output truncated]"
	}
	return out
}

// rtVMContext returns the ctx object of the invocation for the call.
func rtVMContext(rt *jsruntime.Runtime) any {
	return rt.ContextValue()
}

// toPlain converts decoded arguments into plain data the bridge accepts.
func toPlain(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("tool: encode arguments: %w", err)
	}
	var plain map[string]any
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return nil, fmt.Errorf("tool: decode arguments: %w", err)
	}
	if plain == nil {
		return map[string]any{}, nil
	}
	return plain, nil
}
