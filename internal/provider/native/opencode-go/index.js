// opencode-go is the built-in OpenCode Go provider of Rienda. It serves the
// models of the OpenCode Go plan over the endpoint the plan publishes, and
// derives the roster from the models.dev database: every model whose adapter
// is one Rienda speaks, mapped to the wire protocol that adapter names.
//
// The catalog is cached beside this module (the writable mirror the loader
// documents) and is the only thing this module keeps: on a failed fetch it
// serves whatever the cache holds, however old, and only yields an empty
// roster when it has neither. Refreshing the roster is restarting Rienda.
module.exports = function(ctx) {
  var BASE_URL = "https://opencode.ai/zen/go/v1";
  var SESSION_HEADER = "x-opencode-session";
  var MODELS_DEV = "https://models.dev/api.json";
  var CACHE = "cache/models.json";
  var FETCH_TIMEOUT_MS = 30000;

  // The adapters Rienda speaks, mapped to the wire protocol each names.
  // Anything else is out of reach of the current clients and is skipped.
  var PROTOCOLS = {
    "@ai-sdk/openai": "openai_responses",
    "@ai-sdk/openai-compatible": "openai_chat_completions",
    "@ai-sdk/anthropic": "anthropic",
  };

  // The thinking modes every reasoning model of the plan offers. "off" is
  // implicit and never declared.
  var THINKING_MODES = [
    { level: "low", max_tokens: 8192 },
    { level: "high", max_tokens: 16384 },
  ];

  // readCache returns the cached catalog text, or null when there is none.
  function readCache() {
    if (!ctx.file.exists(CACHE)) return null;
    return ctx.file.read(CACHE);
  }

  // writeCache stores the catalog text, tolerating a failure: the next start
  // fetches again.
  function writeCache(text) {
    try {
      ctx.file.write(CACHE, text);
    } catch (err) {
      ctx.log("opencode-go: cache write failed: " + err);
    }
  }

  // fetchCatalog fetches the models.dev document and returns its text.
  function fetchCatalog() {
    var res = ctx.http.fetch(MODELS_DEV, { timeout_ms: FETCH_TIMEOUT_MS });
    if (res.status !== 200) throw new Error("models.dev returned " + res.status);
    return res.body;
  }

  // catalog returns the freshest catalog text available: the live fetch when
  // it works, the cache whatever its age otherwise.
  function catalog() {
    try {
      var live = fetchCatalog();
      writeCache(live);
      return live;
    } catch (err) {
      ctx.log("opencode-go: models.dev fetch failed (" + err + "); using the cache");
    }
    var cached = readCache();
    if (cached) return cached;
    ctx.log("opencode-go: no cache and no models.dev: the roster stays empty until the next start");
    return "{}";
  }

  // protocolOf resolves the wire protocol of a model: the adapter the model
  // declares when it does, the adapter of the provider otherwise. Null when
  // the effective adapter is not one Rienda speaks.
  function protocolOf(providerNpm, model) {
    var modelNpm = model.provider && model.provider.npm;
    return PROTOCOLS[modelNpm || providerNpm] || null;
  }

  // thinking returns the thinking modes of a reasoning model: the declared
  // modes when the model publishes an effort option, the plan default
  // otherwise.
  function thinking(model) {
    var options = model.reasoning_options || [];
    for (var i = 0; i < options.length; i++) {
      if (options[i].type === "effort") return THINKING_MODES;
    }
    if (model.reasoning) return THINKING_MODES;
    return [];
  }

  // roster maps the models.dev models of the plan to the canonical shape.
  function roster(db) {
    var provider = db["opencode-go"];
    var models = [];
    if (!provider || !provider.models) return models;
    var npm = provider.npm;
    for (var id in provider.models) {
      var entry = provider.models[id];
      var protocol = protocolOf(npm, entry);
      if (!protocol) continue;
      var limit = entry.limit || {};
      models.push({
        id: id,
        protocol: protocol,
        context_window: limit.context || 0,
        max_output_tokens: limit.output || 0,
        reasoning: entry.reasoning === true,
        thinking_modes: thinking(entry),
      });
    }
    return models;
  }

  return {
    protocol: "openai_chat_completions",
    base_url: BASE_URL,
    session_header: SESSION_HEADER,
    auth: "api_key",
    models: roster(JSON.parse(catalog())),
  };
};
