# Hooks

A hook runs your code at fixed points of a run, so you can inspect, gate or
change what happens without touching Rienda's source. A hook lives in its own
directory under `~/.rienda/hooks`:

```
~/.rienda/hooks/<hook-name>/index.js
```

The directory name is the hook name, so `~/.rienda/hooks/guard` defines a hook
called `guard`.

Hooks are global: they are read from `~/.rienda` and never from a workspace.
They run with your privileges and are trusted: install only hooks you trust.

## Your first hook

A hook file exports one function per point it handles. It may implement any
subset of the points.

```js
// ~/.rienda/hooks/guard/index.js
module.exports = {
  beforeToolExecute: function(ctx, call) {
    if (
      call.name === "shell" && /\brm\s+-rf\b/.test(call.arguments.command || "")
    ) {
      if (
        !ctx.confirm({
          title: "Dangerous command",
          body: call.arguments.command,
        })
      ) {
        return { allow: false, reason: "refused by the user" };
      }
    }
  },

  afterRun: function(ctx, event) {
    ctx.notify({ title: "rienda", body: "run finished: " + event.reason });
  },
};
```

For the hook to run, an agent must declare it. The list order is the execution
order:

```yaml
---
description: A careful agent.
model: openai/gpt-4o
tools:
  - shell
hooks:
  - guard
  - audit
---
```

An agent that declares no `hooks` runs with no hooks. A declared hook that does
not exist fails the run, the same way an unknown tool does.

## The points

Every point is named `<phase><Subject>`, where the phase is `before` or `after`.
A hook receives `ctx` (identical to the one tools get; see [Tools](./tools.md))
and the payload described below. Returning nothing means "no opinion" and
changes nothing.

| Point                | Runs                                                     | Receives                                            | May return                        |
| -------------------- | -------------------------------------------------------- | --------------------------------------------------- | --------------------------------- |
| `beforeRun`          | Once, at the start of a run.                             | `{ sessionId, agentId, modelId }`                   | nothing                           |
| `afterRun`           | Once, when the run ends, for any reason.                 | `{ reason }` (`end_turn`, `interrupted` or `error`) | nothing                           |
| `beforeModelRequest` | Every turn, just before the provider call.               | `{ system, model, messages }`                       | `{ system?, context? }`           |
| `afterModelResponse` | Every turn, after the model answers, before it is saved. | `{ text, thinking, toolCalls }`                     | `{ text?, thinking? }`            |
| `beforeToolExecute`  | Before a tool runs, once per call.                       | `{ id, name, arguments }`                           | `{ allow?, reason?, arguments? }` |
| `afterToolExecute`   | After a tool returns, once per call.                     | `{ id, name, arguments }` and `{ text, isError }`   | `{ text?, isError? }`             |

`beforeModelRequest.messages` and `afterModelResponse.toolCalls` are read-only
today.

## What each return does

### `beforeModelRequest`

The common use is adding context to the system prompt.

- `system` replaces the system prompt for this turn only. It is not saved and
  does not carry over to the next turn.
- `context` is appended as an extra section of the system prompt.

```js
beforeModelRequest: function (ctx, request) {
  return { context: "Today is " + new Date().toISOString().slice(0, 10) + "." };
}
```

### `afterModelResponse`

Rewrites the assistant's own text before it enters the conversation. Useful to
redact secrets or normalize output.

```js
afterModelResponse: function (ctx, response) {
  return { text: response.text.replace(/\b\d{16}\b/g, "[redacted]") };
}
```

### `beforeToolExecute`

Gates a call, rewrites its arguments, or both.

- `allow: false` refuses the call. The model receives an error result carrying
  `reason`, and no tool runs.
- `arguments` replaces the arguments the next hook and the tool see.

```js
beforeToolExecute: function (ctx, call) {
  if (call.name !== "http") return;
  return { arguments: { ...call.arguments, url: call.arguments.url.replace("http://", "https://") } };
}
```

### `afterToolExecute`

Rewrites the result the model reads.

```js
afterToolExecute: function (ctx, call, result) {
  return { text: result.text.toUpperCase() };
}
```

## Chaining and order

- Hooks run in the order their extensions are listed in the agent.
- Within a point, one hook's output is the next hook's input: a rewrite chains,
  and the last writer wins.
- `beforeToolExecute`: on the first `allow: false`, the call is refused and no
  further hook is consulted for that call.
- A hook that throws is reported as a diagnostic and skipped. It never crashes
  the run.

## The context

Hooks use the exact same `ctx` as tools: `ctx.workdir`, `ctx.session`,
`ctx.agent`, `ctx.config`, `ctx.file`, `ctx.env`, `ctx.http`, `ctx.system`,
`ctx.log`, `ctx.confirm` and `ctx.notify`. See
[Tools](./tools.md#the-context) for the full list.

`ctx.confirm` is the human-in-the-loop primitive: it blocks until the user
answers. When a run is non-interactive, it is answered automatically based on
the run's auto-approve setting.

## Behavior and limits

- **No timeout.** A hook runs until it returns or the run is interrupted.
- **Fresh state.** Every invocation runs in a fresh runtime, so nothing persists
  in memory between calls. To remember something, write a file.
- **Errors are non-fatal.** A broken hook or a throwing handler is reported as a
  diagnostic and never fails the run.
