---
title: "Advanced"
description: "Extend Rienda with your own JavaScript: the tools the model can call and the hooks that run around a run."
icon: "puzzle"
weight: 20
---

# Advanced

Rienda is extended in JavaScript, without touching its source and without
compiling anything. Two kinds of extension exist:

- A **tool** is a capability the model can invoke during a conversation, such as
  reading a project file or calling an internal API.
- A **hook** is your code running at a fixed point of a run, so you can inspect
  it, gate it, or change what happens.

Start with [Tools](/docs/advanced/tools/) and continue with
[Hooks](/docs/advanced/hooks/). Both pages share the same runtime and the same
`ctx` object, so everything you learn on the first applies to the second.

## Nothing extra to install

The JavaScript engine is embedded in the binary. Rienda runs your scripts with
[goja](https://github.com/dop251/goja), a pure Go implementation of JavaScript,
so there is no Node.js, no npm, and no dependency to install: write an `index.js`
file, start a session, and it is there.

That also means the language you write is not the one your editor may assume.
The engine implements **ECMAScript 5.1 in full**, plus some features of
**ECMAScript 6**: the newer revision is still work in progress upstream, so
treat ES5.1 as the language you can always rely on. In practice:

- Everything of ES5 works: `var`, functions, prototypes, closures, `Object`
  utilities, `JSON`, regular expressions, `try`/`catch`, and strict mode.
- A good part of ES6 works: `let` and `const`, arrow functions, classes with
  `extends`, template literals, destructuring, default and rest parameters, the
  spread operator, `for...of`, `Map`, `Set`, `Symbol`, `Promise`, generators,
  getters and setters, and the additions to the standard library such as
  `Object.entries`, `Object.assign`, `Array.prototype.includes` and
  `String.prototype.padStart`.
- Not everything does. There is **no module loader**, so `import` and `export`
  do not exist and a script assigns `module.exports` instead. Async iteration
  (`for await...of`) and async generators are unsupported, and the engine does
  not provide the host globals a browser or Node.js would, such as `setTimeout`,
  `fetch`, `URL`, `Buffer`, or `console`: the `ctx` object is what replaces
  them.

When in doubt, write ES5-compatible code, which always runs. The example pages
use nothing beyond what the engine supports.

<vara-alert
title="Your scripts are trusted"
description="An extension runs with your privileges and is not sandboxed: it can read and write your files, run commands, and reach the network. Install only extensions you trust, exactly as you would install a program."
color="warning"
/>

## How the runtime behaves

Four rules explain almost everything about writing an extension:

1. **One call, one fresh runtime.** Every invocation of a tool or a hook starts
   with a clean engine, so nothing is remembered between calls. To keep
   something, write it to a file.
2. **No event loop.** The runtime is synchronous: `setTimeout`, `setInterval`,
   `process`, and `require` do not exist, and nothing waits for a callback that
   would fire later. A `Promise` resolves only after your function has already
   returned, so a result you need must be produced by the code you return from
   directly.
3. **No timeout.** A tool or a hook runs until it returns or the run is
   interrupted, so you own the timeouts of anything slow you call.
4. **`ctx` is the surface.** Everything Rienda offers a script, from reading a
   file to asking the user a question, is a member of the `ctx` object. The
   [Tools](/docs/advanced/tools/#the-context) page documents it completely, and
   hooks receive the same one.
