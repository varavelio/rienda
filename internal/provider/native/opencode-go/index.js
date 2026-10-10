// opencode-go is the built-in OpenCode Go provider of Rienda. It serves the
// models of the OpenCode Go plan over the endpoint the plan publishes, and
// derives its roster from the models.dev database: every model whose adapter
// is one Rienda speaks, mapped to the wire protocol that adapter names,
// carrying the display names and the thinking levels models.dev documents.
//
// The catalog is cached through ctx.cache under "models.dev", shared with
// every other provider that reads the database. A fresh-enough cache serves
// as it is: the database is refetched only when the cached copy is older than
// the refresh window. On a failed fetch the cache serves whatever it holds,
// however old, and only an empty roster is left when there is neither.
// Refreshing the roster is restarting Rienda.
module.exports = function(ctx) {
  var BASE_URL = "https://opencode.ai/zen/go/v1";
  var SESSION_HEADER = "x-opencode-session";
  var MODELS_DEV = "https://models.dev/api.json";
  var CACHE_KEY = "models.dev";
  var FETCH_TIMEOUT_MS = 30000;

  // The catalog is refetched once in this window; between refetches the
  // cache answers alone.
  var REFRESH_AFTER_MS = 4 * 60 * 60 * 1000;

  // The adapters Rienda speaks, mapped to the wire protocol each names.
  // Anything else is out of reach of the current clients and is skipped.
  var PROTOCOLS = {
    "@ai-sdk/openai": "openai_responses",
    "@ai-sdk/openai-compatible": "openai_chat_completions",
    "@ai-sdk/anthropic": "anthropic",
  };

  // The thinking levels every provider documents on its effort option, minus
  // the ones Rienda reserves: "off" and "none" name the picker entry that
  // sends nothing, so a database level either of those names is skipped.
  var RESERVED_LEVELS = { off: true, none: true };

  // isFresh reports whether a cached catalog is young enough to serve
  // without a refetch. A document without a parsable date is stale: the
  // cache stays a hint, so a malformed envelope can only cost a refetch,
  // never the roster itself.
  function isFresh(doc) {
    if (!doc || typeof doc.date !== "string") return false;
    var saved = Date.parse(doc.date);
    if (isNaN(saved)) return false;
    return Date.now() - saved < REFRESH_AFTER_MS;
  }

  // cacheWrite wraps write so a cache that cannot be stored is logged and
  // dropped: the next start fetches again.
  function cacheWrite(data) {
    try {
      ctx.cache.write(CACHE_KEY, data);
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

  // catalog returns the models.dev database: the fresh cache when one
  // exists, the live fetch otherwise, and whatever the cache holds on a
  // failed fetch. Without either, the roster stays empty until the next
  // start.
  function catalog() {
    var cached = null;
    try {
      cached = ctx.cache.read(CACHE_KEY);
    } catch (err) {
      ctx.log("opencode-go: cache read failed: " + err);
    }
    if (isFresh(cached)) return cached.data;

    try {
      var live = JSON.parse(fetchCatalog());
      cacheWrite(live);
      return live;
    } catch (err) {
      ctx.log("opencode-go: models.dev fetch failed (" + err + "); using the cache");
    }
    if (cached) return cached.data;
    ctx.log("opencode-go: no cache and no models.dev: the roster stays empty until the next start");
    return {};
  }

  // protocolOf resolves the wire protocol of a model: the adapter the model
  // declares when it does, the adapter of the provider otherwise. Null when
  // the effective adapter is not one Rienda speaks.
  function protocolOf(providerNpm, model) {
    var modelNpm = model.provider && model.provider.npm;
    return PROTOCOLS[modelNpm || providerNpm] || null;
  }

  // thinkingModes returns the extended thinking levels a reasoning model
  // offers, in the order models.dev documents them: the values of its
  // effort option, which map one to one onto the level-based wire APIs.
  // A model that reasons without an effort option, or takes a bare toggle
  // or a token budget, offers the picker nothing: Rienda speaks levels
  // only, and a model with fewer than one speakable level reads as fixed
  // reasoning the provider decides alone.
  function thinkingModes(model) {
    var options = model.reasoning_options || [];
    for (var i = 0; i < options.length; i++) {
      if (options[i].type !== "effort") continue;
      var modes = [];
      var values = options[i].values || [];
      for (var j = 0; j < values.length; j++) {
        var level = values[j];
        if (RESERVED_LEVELS[level]) continue;
        modes.push({ level: level, max_tokens: 0 });
      }
      return modes;
    }
    return [];
  }

  // roster maps the models.dev models of the plan to the canonical shape.
  function roster(db) {
    var plan = db["opencode-go"];
    var models = [];
    if (!plan || !plan.models) return models;
    var npm = plan.npm;
    // Ascending id order, so the roster reads the same on every start.
    var ids = Object.keys(plan.models).sort();
    for (var i = 0; i < ids.length; i++) {
      var id = ids[i];
      var entry = plan.models[id];
      var protocol = protocolOf(npm, entry);
      if (!protocol) continue;
      var limit = entry.limit || {};
      models.push({
        id: id,
        name: entry.name || "",
        protocol: protocol,
        context_window: limit.context || 0,
        max_output_tokens: limit.output || 0,
        reasoning: entry.reasoning === true,
        thinking_modes: thinkingModes(entry),
      });
    }
    return models;
  }

  return {
    protocol: "openai_chat_completions",
    base_url: BASE_URL,
    session_header: SESSION_HEADER,
    auth: "api_key",
    models: roster(catalog()),
  };
};
