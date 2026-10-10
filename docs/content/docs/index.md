---
title: "Documentation"
description: "Install Rienda, define your first agent, and extend it with your own JavaScript tools and hooks."
icon: "book-open"
weight: 0
---

# Documentation

Rienda is a simple, declarative, multi-agent LLM harness. You describe an agent
in a Markdown file, Rienda runs it against the provider you configure, and the
whole conversation is stored as a session you can reopen.

## How to read this documentation

Start with installation, then read the advanced section only when you want to
extend Rienda beyond its built-in behavior:

1. [Installation](/docs/installation/) covers the installers, the prebuilt
   binaries, the Docker image, and building from source.
2. [Advanced](/docs/advanced/) explains the JavaScript extensions: the tools the
   model can call and the hooks that run at fixed points of a run.

## The three moving parts

Everything in Rienda reduces to three inputs, and it helps to keep them separate
in your head from the beginning:

- **Provider modules** (`~/.rienda/providers/<name>/index.js`) are JavaScript
  functions returning a canonical declaration of the connection and the model
  roster of the provider; the ones built in ship inside the binary, and a user
  module of the same name replaces them. `rienda auth` stores the keys the
  modules ask for. Declare nothing twice: models are not listed in a config
  file, and an agent does not name a model either.
- **Agent definitions** (`~/.rienda/agents/*.md`) are Markdown files whose YAML
  frontmatter declares the description and the tools and hooks the agent may
  use, and whose body is the system prompt. Agents never name a model: a
  session picks the model it runs, and `ctrl+x m` or `ctrl+x t` switch it or
  its thinking mode while the conversation goes. An agent is the unit you
  run.
- **Sessions** (`~/.rienda/sessions`) are the transcripts Rienda writes as you
  work. They are what the start list offers back, and they let a conversation be
  continued, branched, or read again later.

The rest of this documentation is what those three statements mean in practice.

## Where to go next

- New here? Go through [Installation](/docs/installation/).
- Want to extend Rienda without touching Go? Read
  [Advanced](/docs/advanced/).
