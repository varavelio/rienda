package jsruntime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/agent"
)

// invokeWithContext runs body with ctx installed, calling fn(ctx) in the script.
func invokeWithContext(t *testing.T, ctx context.Context, opts Options, body string) (any, error) {
	t.Helper()
	module := compileScript(t, `module.exports = { run: function(ctx) { return (`+body+`); } };`)
	var result any
	err := module.Invoke(ctx, opts, nil, func(rt *Runtime, exports *Exports) error {
		run, ok := exports.Field("run")
		require.True(t, ok)
		got, callErr := run.Call(ctx, rt.vm.Get("ctx"))
		if callErr != nil {
			return callErr
		}
		if got.Undefined() {
			return nil
		}
		result, _ = got.JSON()
		return nil
	})
	return result, err
}

func runCtx(t *testing.T) (context.Context, Options) {
	t.Helper()
	workdir := t.TempDir()
	run := agent.Run{
		SessionID: "sess-1",
		ModelID:   "wire-model",
		Agent: agent.Agent{
			ID:           "coder",
			Description:  "Writes code.",
			Model:        "fake/model",
			Tools:        []string{"shell"},
			Hooks:        []string{"guard"},
			Config:       map[string]map[string]any{"guard": {"level": "strict"}},
			SystemPrompt: "Be brief.",
		},
	}
	ctx := agent.WithRun(t.Context(), run)
	return ctx, Options{
		Workdir: workdir,
		Config: map[string]any{
			"config": map[string]any{"guard": map[string]any{"level": "strict"}},
		},
	}
}

func TestTopLevel(t *testing.T) {
	t.Run("exposes workdir session agent config", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(
			t,
			ctx,
			opts,
			`({workdir: ctx.workdir, session: ctx.session, agent: ctx.agent, config: ctx.config})`,
		)
		require.NoError(t, err)
		doc := requireObject(t, got)
		require.Equal(t, opts.Workdir, requireString(t, doc, "workdir"))
		session := requireObject(t, doc["session"])
		require.Equal(t, "sess-1", requireString(t, session, "id"))
		require.Equal(t, "coder", requireString(t, session, "agentId"))
		require.Equal(t, "wire-model", requireString(t, session, "modelId"))
		require.Equal(t, opts.Workdir, requireString(t, session, "workdir"))
		agentInfo := requireObject(t, doc["agent"])
		require.Equal(t, "coder", requireString(t, agentInfo, "name"))
		require.Equal(t, "Be brief.", requireString(t, agentInfo, "systemPrompt"))
		require.Equal(
			t,
			"strict",
			requireString(
				t,
				requireObject(t, requireObject(t, agentInfo["config"])["guard"]),
				"level",
			),
		)
	})

	t.Run("mutations do not leak", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `(ctx.config.x = 1, ctx.agent.y = 2, true)`)
		require.NoError(t, err)
		got, err := invokeWithContext(t, ctx, opts, `([ctx.config.x, ctx.agent.y].join(","))`)
		require.NoError(t, err)
		require.Equal(t, ",", got)
	})

	t.Run("confirm answers false without an interactor", func(t *testing.T) {
		ctx, opts := runCtx(t)
		got, err := invokeWithContext(t, ctx, opts, `ctx.confirm({title: "t", body: "b"})`)
		require.NoError(t, err)
		require.Equal(t, false, got)
	})

	t.Run("confirm asks the interactor", func(t *testing.T) {
		ctx, opts := runCtx(t)
		inner := &scriptInteractor{answer: true}
		ctx = WithInteractor(ctx, inner)
		got, err := invokeWithContext(t, ctx, opts, `ctx.confirm({title: "t", body: "b"})`)
		require.NoError(t, err)
		require.Equal(t, true, got)
		require.Equal(t, "t", inner.asked[0].Title)
	})

	t.Run("confirm raises on cancel", func(t *testing.T) {
		ctx, opts := runCtx(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := invokeWithContext(t, canceled, opts, `ctx.confirm({title: "t", body: "b"})`)
		require.Error(t, err)
	})

	t.Run("notify drops without an interactor", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.notify({title: "t", body: "b"})`)
		require.NoError(t, err)
	})

	t.Run("notify forwards with an interactor", func(t *testing.T) {
		ctx, opts := runCtx(t)
		inner := &scriptInteractor{}
		ctx = WithInteractor(ctx, inner)
		_, err := invokeWithContext(t, ctx, opts, `ctx.notify({title: "t", body: "b"})`)
		require.NoError(t, err)
		require.Len(t, inner.notices, 1)
	})

	t.Run("rejects bad interaction arguments", func(t *testing.T) {
		ctx, opts := runCtx(t)
		for _, body := range []string{
			`ctx.confirm({})`, `ctx.confirm({title: "t"})`, `ctx.confirm({title: 1, body: "b"})`,
			`ctx.notify({title: "t"})`,
		} {
			_, err := invokeWithContext(t, ctx, opts, body)
			require.Error(t, err, body)
		}
	})

	t.Run("log streams", func(t *testing.T) {
		ctx, opts := runCtx(t)
		module := compileScript(t, `module.exports = { run: function(ctx) { ctx.log("hi"); } };`)
		var got []byte
		require.NoError(t, module.Invoke(ctx, opts, func(s Stream, data []byte) {
			require.Equal(t, StreamStdout, s)
			got = append(got, data...)
		}, func(rt *Runtime, exports *Exports) error {
			run, _ := exports.Field("run")
			_, err := run.Call(ctx, rt.vm.Get("ctx"))
			return err
		}))
		require.Equal(t, "hi", string(got))
	})

	t.Run("sleep returns on cancel", func(t *testing.T) {
		ctx, opts := runCtx(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := invokeWithContext(t, canceled, opts, `ctx.sleep(5000)`)
		require.Error(t, err)
	})

	t.Run("sleep rejects bad values", func(t *testing.T) {
		ctx, opts := runCtx(t)
		_, err := invokeWithContext(t, ctx, opts, `ctx.sleep(-1)`)
		require.Error(t, err)
		_, err = invokeWithContext(t, ctx, opts, `ctx.sleep("x")`)
		require.Error(t, err)
		_, err = invokeWithContext(t, ctx, opts, `ctx.sleep(0)`)
		require.NoError(t, err)
	})
}

// requireObject returns the plain object value.
func requireObject(t *testing.T, value any) map[string]any {
	t.Helper()
	table, ok := value.(map[string]any)
	require.True(t, ok, "expected an object")
	return table
}

// requireNumber returns the numeric field of a plain object or value.
func requireNumber(t *testing.T, value any, field string) float64 {
	t.Helper()
	var number float64
	switch v := value.(type) {
	case map[string]any:
		var ok bool
		number, ok = v[field].(float64)
		require.True(t, ok, "expected a number at %q", field)
	default:
		t.Fatalf("expected an object for %q", field)
	}
	return number
}

// requireString returns the string field of a plain object.
func requireString(t *testing.T, doc map[string]any, field string) string {
	t.Helper()
	text, ok := doc[field].(string)
	require.True(t, ok, "expected a string at %q", field)
	return text
}
