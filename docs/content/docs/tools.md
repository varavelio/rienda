# Tools

A tool is a capability the model can invoke during a conversation. Rienda ships
a couple of built-in tools, and you can add your own in JavaScript. A custom
tool lives in its own directory under `~/.rienda/tools`:

```
~/.rienda/tools/<tool-name>/index.js
```

The directory name is the tool name the model calls, so `~/.rienda/tools/word-count`
defines a tool called `word-count`.

Tools are global: they are read from `~/.rienda` and never from a workspace, so
a tool changes Rienda everywhere. A user tool that shares a name with a built-in
shadows it, which is how you tune or replace built-in behavior.

Because a tool runs with your privileges on your machine, it is trusted. Rienda
does not sandbox extension scripts: install only tools you trust.

## Your first tool

```js
// ~/.rienda/tools/word-count/index.js
module.exports = {
  description: "Counts the words of a file in the workspace.",
  parameters: {
    type: "object",
    properties: {
      path: { type: "string", description: "Path relative to the workspace." },
    },
    required: ["path"],
    additionalProperties: false,
  },
  execute: function(ctx, args) {
    const text = ctx.file.read(args.path);
    return String(text.trim().split(/\s+/).length);
  },
};
```

For the model to use it, an agent must declare the tool in its definition:

```yaml
---
description: A careful agent.
model: openai/gpt-4o
tools:
  - word-count
---
```

## Anatomy

A tool file exports a single object with three fields.

| Field         | Type     | Required | Purpose                                                           |
| ------------- | -------- | -------- | ----------------------------------------------------------------- |
| `description` | string   | yes      | What the tool does and when to use it. Shown to the model.        |
| `parameters`  | object   | yes      | A JSON Schema for the arguments. Passed to the provider verbatim. |
| `execute`     | function | yes      | Runs one invocation. See below.                                   |

A file that is missing any of these is skipped, and the problem is reported as
a diagnostic. It never crashes a run.

## `execute(ctx, args)`

`execute` is called once per invocation. It receives the runtime context `ctx`
(see [The context](#the-context)) and `args`, the arguments the model sent,
already decoded from JSON into a plain object that matches `parameters`.

It must return one of two shapes:

| Return                                | Result the model reads                            |
| ------------------------------------- | ------------------------------------------------- |
| a string                              | that text, as a successful result                 |
| `{ text: string, isError?: boolean }` | `text`, marked as an error when `isError` is true |

Anything else (a number, a boolean, an array, `null`, an object without a `text`
string) is treated as an error, so a mistake is reported instead of reaching the
model as a surprising value. To return structured data, serialize it yourself:

```js
return JSON.stringify({ count: words, file: args.path });
```

A `throw` fails the invocation, never the run: the model receives the error text
and the conversation continues.

## The context

Every tool and hook receives the same `ctx`. It groups its capabilities in
namespaces and exposes a few top-level values.

### Top level

| Member                         | Type    | Description                                                                                                                                                                                                  |
| ------------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ctx.workdir`                  | string  | Absolute workspace directory. The base for every relative path.                                                                                                                                              |
| `ctx.session`                  | object  | `{ id, agentId, modelId, workdir }` of the current run.                                                                                                                                                      |
| `ctx.agent`                    | object  | The running agent: `name` (its id) plus its whole frontmatter (`description`, `model`, `tools`, `hooks`, `config`) and `systemPrompt` (the Markdown body). Per-agent settings live under `ctx.agent.config`. |
| `ctx.config`                   | object  | The global `config.yaml`, read-only. Put custom settings under its `config` block, keyed by tool or hook name.                                                                                               |
| `ctx.log(text)`                |         | Streams `text` to the front end. Not capped.                                                                                                                                                                 |
| `ctx.sleep(ms)`                |         | Sleeps for `ms` milliseconds, or returns early when the run is interrupted.                                                                                                                                  |
| `ctx.confirm({ title, body })` | boolean | Asks the user a yes/no question. Blocks until they answer; returns `true` on approval, `false` on refusal.                                                                                                   |
| `ctx.notify({ title, body })`  |         | Shows a non-blocking notice to the user.                                                                                                                                                                     |

### `ctx.file`

Reads and writes the file system. Relative paths resolve against `ctx.workdir`.

| Member                              | Description                                                                                                                                                                                   |
| ----------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx.file.read(path, opts?)`        | Returns the file content as text. `opts = { encoding? }` (only `"utf8"` for now). Invalid UTF-8 is replaced, never raised.                                                                    |
| `ctx.file.write(path, data, opts?)` | Writes `data`, creating parent directories. `opts = { append? }`: by default the file is truncated.                                                                                           |
| `ctx.file.exists(path)`             | Returns whether the path exists, file or directory.                                                                                                                                           |
| `ctx.file.list(path, opts?)`        | Lists a directory as `[{ name, path, isDir }]`. `opts = { recursive?, respectIgnoreFiles? }`, both `true` by default. `respectIgnoreFiles` honors the `.gitignore` and `.ignore` of the tree. |

### `ctx.env`

| Member              | Description                                                               |
| ------------------- | ------------------------------------------------------------------------- |
| `ctx.env.get(name)` | Returns the value of an environment variable, or `null` when it is unset. |

### `ctx.http`

| Member                       | Description                                                                                                                                                                                                                                                     |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx.http.fetch(url, opts?)` | Sends an HTTP request and returns `{ status, headers, body }`. `opts = { method?, headers?, body?, timeout_ms? }`. The response is returned whatever the status, so you decide how to handle it. `headers` is a flat string map and `body` a string sent as-is. |

### `ctx.system`

| Member                            | Description                                                                                                                                                                                                                             |
| --------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ctx.system.exec(command, opts?)` | Runs `command` through the shell. `opts = { cwd?, timeout_ms?, env? }`. Returns `{ code, stdout, stderr }` (`code` is `-1` when a signal killed the process). Output is streamed to the front end live, like the built-in `shell` tool. |
| `ctx.system.which(name)`          | Returns the absolute path of an executable, or `null` when it is not on `PATH`.                                                                                                                                                         |

## Examples

Read a JSON file and return one field:

```js
module.exports = {
  description: "Reads a field from a JSON file in the workspace.",
  parameters: {
    type: "object",
    properties: { path: { type: "string" }, field: { type: "string" } },
    required: ["path", "field"],
    additionalProperties: false,
  },
  execute: function(ctx, args) {
    const data = JSON.parse(ctx.file.read(args.path));
    return JSON.stringify(data[args.field]);
  },
};
```

Ask the user before doing something risky:

```js
module.exports = {
  description: "Publishes the current branch.",
  parameters: { type: "object", properties: {}, additionalProperties: false },
  execute: function(ctx) {
    if (!ctx.confirm({ title: "Publish", body: "Push the current branch?" })) {
      return { text: "cancelled by the user", isError: true };
    }
    const result = ctx.system.exec("git push");
    return result.code === 0
      ? "pushed"
      : { text: result.stderr, isError: true };
  },
};
```

Call an API with `ctx.http.fetch`:

```js
module.exports = {
  description: "Returns the latest release tag of a GitHub repository.",
  parameters: {
    type: "object",
    properties: { repo: { type: "string" } },
    required: ["repo"],
    additionalProperties: false,
  },
  execute: function(ctx, args) {
    const res = ctx.http.fetch(
      "https://api.github.com/repos/" + args.repo + "/releases/latest",
    );
    if (res.status !== 200) {
      return { text: "HTTP " + res.status, isError: true };
    }
    return JSON.parse(res.body).tag_name;
  },
};
```

## Behavior and limits

- **Live output.** Output written with `ctx.log` and everything a `ctx.system.exec`
  command prints reaches the front end while the tool runs, so a long tool shows
  progress instead of looking frozen.
- **Typed returns.** `execute` must return a string or `{ text, isError }`;
  anything else becomes an error result. There is no implicit serialization.
- **Output cap.** The text `execute` returns is capped (256 KiB, cut on a rune
  boundary). Streamed output is never capped.
- **No timeout.** A tool runs until it returns or the run is interrupted.
  Controls such as `timeout_ms` in `ctx.system.exec` are yours to add.
- **Fresh state.** Every invocation runs in a fresh runtime, so nothing persists
  in memory between calls. To remember something, write a file.
- **Name rules.** A tool name is 1 to 64 characters of letters, digits,
  underscores or hyphens.
