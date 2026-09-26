package jsruntime

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/command"
	"github.com/varavelio/rienda/internal/files"
	"github.com/varavelio/rienda/internal/workdir"
)

// outputCap caps the payload a primitive hands to a script, matching the tool
// output cap.
const outputCap = 256 << 10

// installContext builds the ctx object of one invocation and sets it on the
// virtual machine.
func installContext(rt *Runtime, invocationCtx context.Context) error {
	vm := rt.vm
	ctx := vm.NewObject()
	if err := ctx.Set("workdir", rt.opts.Workdir); err != nil {
		return fmt.Errorf("jsruntime: build ctx.workdir: %w", err)
	}
	if err := ctx.Set("session", rt.session(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.session: %w", err)
	}
	if err := ctx.Set("agent", rt.agentInfo(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.agent: %w", err)
	}
	if err := ctx.Set("config", rt.config()); err != nil {
		return fmt.Errorf("jsruntime: build ctx.config: %w", err)
	}
	if err := ctx.Set("log", rt.logFunc(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.log: %w", err)
	}
	if err := ctx.Set("sleep", rt.sleepFunc(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.sleep: %w", err)
	}
	if err := ctx.Set("confirm", rt.confirmFunc(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.confirm: %w", err)
	}
	if err := ctx.Set("notify", rt.notifyFunc(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.notify: %w", err)
	}
	file, err := rt.fileObject(invocationCtx)
	if err != nil {
		return err
	}
	if err := ctx.Set("file", file); err != nil {
		return fmt.Errorf("jsruntime: build ctx.file: %w", err)
	}
	if err := ctx.Set("env", rt.envObject(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.env: %w", err)
	}
	if err := ctx.Set("http", rt.httpObject(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.http: %w", err)
	}
	if err := ctx.Set("system", rt.systemObject(invocationCtx)); err != nil {
		return fmt.Errorf("jsruntime: build ctx.system: %w", err)
	}
	if err := vm.Set("ctx", ctx); err != nil {
		return fmt.Errorf("jsruntime: build ctx: %w", err)
	}
	return nil
}

// session returns the ctx.session value: the identity of the run plus the
// base directory.
func (rt *Runtime) session(ctx context.Context) map[string]any {
	run, _ := agent.RunFromContext(ctx)
	return map[string]any{
		"id":      run.SessionID,
		"agentId": run.Agent.ID,
		"modelId": run.ModelID,
		"workdir": rt.opts.Workdir,
	}
}

// agentInfo returns the ctx.agent value: the running agent definition.
func (rt *Runtime) agentInfo(ctx context.Context) map[string]any {
	run, _ := agent.RunFromContext(ctx)
	definition := run.Agent
	config := make(map[string]any, len(definition.Config))
	for name, values := range definition.Config {
		inner := make(map[string]any, len(values))
		maps.Copy(inner, values)
		config[name] = inner
	}
	return map[string]any{
		"name":         definition.ID,
		"description":  definition.Description,
		"model":        definition.Model,
		"tools":        definition.Tools,
		"hooks":        definition.Hooks,
		"config":       config,
		"systemPrompt": definition.SystemPrompt,
	}
}

// config returns a private copy of the configuration, so a script that mutates
// ctx.config changes nothing the host holds.
func (rt *Runtime) config() any {
	if rt.opts.Config == nil {
		return map[string]any{}
	}
	return deepCopyValue(rt.opts.Config)
}

// deepCopyValue clones plain data through JSON, so mutations stay inside the
// invocation.
func deepCopyValue(value any) any {
	encoded, err := jsonEncode(value)
	if err != nil {
		return map[string]any{}
	}
	decoded, err := jsonDecode(encoded)
	if err != nil {
		return map[string]any{}
	}
	if decoded == nil {
		return map[string]any{}
	}
	return decoded
}

// logFunc streams text to the front end, uncapped, adding no newline.
func (rt *Runtime) logFunc(ctx context.Context) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		text := requiredString(rt, "ctx.log", call, 0, "text")
		rt.canceled(ctx)
		rt.emit(StreamStdout, []byte(text))
		return goja.Undefined()
	}
}

// sleepFunc sleeps for the given milliseconds, returning early on cancel.
func (rt *Runtime) sleepFunc(ctx context.Context) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		ms := requiredNumber(rt, "ctx.sleep", call, 0, "ms")
		if ms < 0 {
			rt.raise("ctx.sleep: ms must not be negative")
		}
		rt.canceled(ctx)
		if ms == 0 {
			return goja.Undefined()
		}
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			panic(rt.vm.ToValue(canceledMessage))
		case <-timer.C:
			return goja.Undefined()
		}
	}
}

// titleBody reads the {title, body} argument of an interaction primitive.
func (rt *Runtime) titleBody(
	_ context.Context,
	primitive string,
	call goja.FunctionCall,
) (string, string) {
	obj := requiredObject(rt, primitive, call, 0, "request")
	title, ok := stringField(obj, "title")
	if !ok {
		rt.raise("%s: title is required", primitive)
	}
	body, ok := stringField(obj, "body")
	if !ok {
		rt.raise("%s: body is required", primitive)
	}
	return title, body
}

// confirmFunc asks the user a yes/no question and blocks until they answer.
func (rt *Runtime) confirmFunc(ctx context.Context) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		title, body := rt.titleBody(ctx, "ctx.confirm", call)
		rt.canceled(ctx)
		interactor, ok := InteractorFromContext(ctx)
		if !ok {
			return rt.vm.ToValue(false)
		}
		approved, err := interactor.Confirm(ctx, ConfirmRequest{Title: title, Body: body})
		if err != nil {
			if ctx.Err() != nil {
				panic(rt.vm.ToValue(canceledMessage))
			}
			rt.raise("ctx.confirm: %s", err.Error())
		}
		return rt.vm.ToValue(approved)
	}
}

// notifyFunc shows a non-blocking notice to the user.
func (rt *Runtime) notifyFunc(ctx context.Context) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		title, body := rt.titleBody(ctx, "ctx.notify", call)
		rt.canceled(ctx)
		if interactor, ok := InteractorFromContext(ctx); ok {
			interactor.Notify(ctx, Notification{Title: title, Body: body})
		}
		return goja.Undefined()
	}
}

// fileObject builds the ctx.file namespace.
func (rt *Runtime) fileObject(ctx context.Context) (*goja.Object, error) {
	file := rt.vm.NewObject()
	for name, fn := range map[string]func(goja.FunctionCall) goja.Value{
		"read":   func(call goja.FunctionCall) goja.Value { return rt.fileRead(ctx, call) },
		"write":  func(call goja.FunctionCall) goja.Value { return rt.fileWrite(ctx, call) },
		"exists": func(call goja.FunctionCall) goja.Value { return rt.fileExists(ctx, call) },
		"list":   func(call goja.FunctionCall) goja.Value { return rt.fileList(ctx, call) },
	} {
		if err := file.Set(name, fn); err != nil {
			return nil, fmt.Errorf("jsruntime: build ctx.file: %w", err)
		}
	}
	return file, nil
}

// resolvePath resolves a path argument against the workspace. An empty path
// means the workspace itself.
func (rt *Runtime) resolvePath(
	ctx context.Context,
	primitive string,
	call goja.FunctionCall,
	index int,
) string {
	path := requiredString(rt, primitive, call, index, "path")
	base, err := workdir.Base(ctx, rt.opts.Workdir)
	if err != nil {
		rt.raise("%s: %s", primitive, err.Error())
	}
	return workdir.Resolve(base, path)
}

// fileRead reads a file as text, replacing invalid UTF-8 instead of raising.
func (rt *Runtime) fileRead(ctx context.Context, call goja.FunctionCall) goja.Value {
	path := rt.resolvePath(ctx, "ctx.file.read", call, 0)
	rt.canceled(ctx)
	opts := optionalObject(rt, "ctx.file.read", call, 1)
	if encoding, ok := opts["encoding"]; ok && encoding != "utf8" {
		rt.raise("ctx.file.read: encoding %q is not supported", encoding)
	}
	info, err := os.Stat(path)
	if err != nil {
		rt.raise("ctx.file.read: %s", err.Error())
	}
	if info.IsDir() {
		rt.raise("ctx.file.read: %s is a directory", path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // scripts are trusted with host paths.
	if err != nil {
		rt.raise("ctx.file.read: %s", err.Error())
	}
	return rt.vm.ToValue(strings.ToValidUTF8(string(data), "\uFFFD"))
}

// fileWrite writes data, creating parent directories and truncating unless
// append is set.
func (rt *Runtime) fileWrite(ctx context.Context, call goja.FunctionCall) goja.Value {
	path := rt.resolvePath(ctx, "ctx.file.write", call, 0)
	data := requiredString(rt, "ctx.file.write", call, 1, "data")
	opts := optionalObject(rt, "ctx.file.write", call, 2)
	rt.canceled(ctx)
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			rt.raise("ctx.file.write: %s", err.Error())
		}
	}
	flags := os.O_WRONLY | os.O_CREATE
	if appendOpt, ok := opts["append"]; ok && appendOpt == true {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	//nolint:gosec // G304 takes user paths by design; scripts are trusted.
	file, err := os.OpenFile(
		path,
		flags,
		0o644,
	)
	if err != nil {
		rt.raise("ctx.file.write: %s", err.Error())
	}
	if _, err := io.WriteString(file, data); err != nil {
		_ = file.Close()
		rt.raise("ctx.file.write: %s", err.Error())
	}
	if err := file.Close(); err != nil {
		rt.raise("ctx.file.write: %s", err.Error())
	}
	return goja.Undefined()
}

// fileExists reports whether a path exists, for a file and for a directory,
// and never raises.
func (rt *Runtime) fileExists(ctx context.Context, call goja.FunctionCall) goja.Value {
	path := rt.resolvePath(ctx, "ctx.file.exists", call, 0)
	rt.canceled(ctx)
	_, err := os.Stat(path)
	return rt.vm.ToValue(err == nil)
}

// fileList lists a directory as [{name, path, isDir}].
func (rt *Runtime) fileList(ctx context.Context, call goja.FunctionCall) goja.Value {
	path := rt.resolvePath(ctx, "ctx.file.list", call, 0)
	opts := optionalObject(rt, "ctx.file.list", call, 1)
	recursive, ok := opts["recursive"]
	if !ok {
		recursive = true
	}
	respect, ok := opts["respectIgnoreFiles"]
	if !ok {
		respect = true
	}
	rt.canceled(ctx)
	entries, err := files.Entries(path, files.Options{
		Recursive:          recursive == true,
		RespectIgnoreFiles: respect == true,
	})
	if err != nil {
		rt.raise("ctx.file.list: %s", err.Error())
	}
	base, err := workdir.Base(ctx, rt.opts.Workdir)
	if err != nil {
		rt.raise("ctx.file.list: %s", err.Error())
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		rel := entry.Path
		abs := workdir.Resolve(path, rel)
		if reversed, err := filepath.Rel(base, abs); err == nil {
			rel = filepath.ToSlash(reversed)
		}
		items = append(items, map[string]any{
			"name":  filepath.Base(entry.Path),
			"path":  rel,
			"isDir": entry.IsDir,
		})
	}
	return rt.vm.ToValue(items)
}

// envObject builds the ctx.env namespace.
func (rt *Runtime) envObject(ctx context.Context) *goja.Object {
	env := rt.vm.NewObject()
	_ = env.Set("get", func(call goja.FunctionCall) goja.Value {
		name := requiredString(rt, "ctx.env.get", call, 0, "name")
		rt.canceled(ctx)
		if value, ok := os.LookupEnv(name); ok {
			return rt.vm.ToValue(value)
		}
		return goja.Null()
	})
	return env
}

// httpObject builds the ctx.http namespace.
func (rt *Runtime) httpObject(ctx context.Context) *goja.Object {
	httpObj := rt.vm.NewObject()
	_ = httpObj.Set(
		"fetch",
		func(call goja.FunctionCall) goja.Value { return rt.httpFetch(ctx, call) },
	)
	return httpObj
}

// httpFetch sends an HTTP request and returns {status, headers, body}.
func (rt *Runtime) httpFetch(ctx context.Context, call goja.FunctionCall) goja.Value {
	rawURL := requiredString(rt, "ctx.http.fetch", call, 0, "url")
	opts := optionalObject(rt, "ctx.http.fetch", call, 1)
	method, _ := opts["method"].(string)
	if method == "" {
		method = http.MethodGet
	}
	method = strings.ToUpper(method)
	var body io.Reader
	if raw, ok := opts["body"]; ok {
		text, ok := raw.(string)
		if !ok {
			rt.raise("ctx.http.fetch: body must be a string")
		}
		body = strings.NewReader(text)
	}
	timeout := time.Duration(0)
	if raw, ok := opts["timeout_ms"]; ok {
		ms, ok := toFloat(raw)
		if !ok || ms < 0 {
			rt.raise("ctx.http.fetch: timeout_ms must be a non-negative number")
		}
		timeout = time.Duration(ms) * time.Millisecond
	}
	headers := map[string]string{}
	if raw, ok := opts["headers"]; ok {
		table, ok := raw.(map[string]any)
		if !ok {
			rt.raise("ctx.http.fetch: headers must be an object")
		}
		for k, v := range table {
			text, ok := v.(string)
			if !ok {
				rt.raise("ctx.http.fetch: header %q must be a string", k)
			}
			headers[k] = text
		}
	}
	rt.canceled(ctx)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		rt.raise("ctx.http.fetch: %s", err.Error())
	}
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			panic(rt.vm.ToValue(canceledMessage))
		}
		rt.raise("ctx.http.fetch: %s", err.Error())
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, outputCap+1))
	if err != nil {
		rt.raise("ctx.http.fetch: %s", err.Error())
	}
	if len(data) > outputCap {
		data = data[:outputCap]
	}
	lowered := map[string]any{}
	for k, values := range response.Header {
		lowered[strings.ToLower(k)] = strings.Join(values, ", ")
	}
	return rt.vm.ToValue(map[string]any{
		"status":  response.StatusCode,
		"headers": lowered,
		"body":    strings.ToValidUTF8(string(data), "\uFFFD"),
	})
}

// systemObject builds the ctx.system namespace.
func (rt *Runtime) systemObject(ctx context.Context) *goja.Object {
	system := rt.vm.NewObject()
	_ = system.Set(
		"exec",
		func(call goja.FunctionCall) goja.Value { return rt.systemExec(ctx, call) },
	)
	_ = system.Set("which", func(call goja.FunctionCall) goja.Value {
		name := requiredString(rt, "ctx.system.which", call, 0, "name")
		rt.canceled(ctx)
		if path, err := exec.LookPath(name); err == nil {
			return rt.vm.ToValue(path)
		}
		return goja.Null()
	})
	return system
}

// systemExec runs a command through the shell and returns {code, stdout,
// stderr}, streaming output live.
func (rt *Runtime) systemExec(ctx context.Context, call goja.FunctionCall) goja.Value {
	cmdline := requiredString(rt, "ctx.system.exec", call, 0, "command")
	opts := optionalObject(rt, "ctx.system.exec", call, 1)
	if _, ok := opts["onOutput"]; ok {
		rt.raise("ctx.system.exec: onOutput is reserved and not implemented in v1")
	}
	cwd := ""
	if raw, ok := opts["cwd"]; ok {
		text, ok := raw.(string)
		if !ok {
			rt.raise("ctx.system.exec: cwd must be a string")
		}
		cwd = text
	}
	base, err := workdir.Base(ctx, rt.opts.Workdir)
	if err != nil {
		rt.raise("ctx.system.exec: %s", err.Error())
	}
	dir := workdir.Resolve(base, cwd)
	env := map[string]string{}
	if raw, ok := opts["env"]; ok {
		table, ok := raw.(map[string]any)
		if !ok {
			rt.raise("ctx.system.exec: env must be an object")
		}
		for k, v := range table {
			text, ok := v.(string)
			if !ok {
				rt.raise("ctx.system.exec: env %q must be a string", k)
			}
			env[k] = text
		}
	}
	var timeout time.Duration
	if raw, ok := opts["timeout_ms"]; ok {
		ms, ok := toFloat(raw)
		if !ok || ms < 0 {
			rt.raise("ctx.system.exec: timeout_ms must be a non-negative number")
		}
		timeout = time.Duration(ms) * time.Millisecond
	}
	rt.canceled(ctx)
	outcome, err := command.Run(ctx, command.Request{
		Argv:      []string{"sh", "-c", cmdline},
		Dir:       dir,
		Timeout:   timeout,
		MaxOutput: outputCap,
		Env:       env,
		Stream:    execStream{rt: rt},
	})
	if err != nil {
		if ctx.Err() != nil {
			panic(rt.vm.ToValue(canceledMessage))
		}
		rt.raise("ctx.system.exec: %s", err.Error())
	}
	code := outcome.ExitCode
	if outcome.TimedOut {
		code = -1
	}
	return rt.vm.ToValue(map[string]any{
		"code":   code,
		"stdout": outcome.Stdout,
		"stderr": outcome.Stderr,
	})
}

// execStream forwards command output to the invocation sink.
type execStream struct {
	rt *Runtime
}

// Emit reports one chunk of command output.
func (s execStream) Emit(name string, data []byte) {
	if name == "stderr" {
		s.rt.emit(StreamStderr, data)
		return
	}
	s.rt.emit(StreamStdout, data)
}
