// z-ai is the built-in Z.AI provider of Rienda. It serves the GLM catalogue
// over the endpoint the Z.AI open platform publishes, and derives its model
// catalogue from the models.dev database.
//
// The catalog is cached through ctx.cache under "models.dev", shared with
// every other provider that reads the database.
module.exports = function(ctx) {
  let MODELS_DEV = "https://models.dev/api.json";
  let CACHE_KEY = "models.dev";
  let FETCH_TIMEOUT_MS = 30000;

  // The catalog is refetched once in this window; between refetches the
  // cache answers alone.
  let REFRESH_AFTER_MS = 4 * 60 * 60 * 1000;

  // A model carries a status only while it is not fully live: "alpha",
  // "beta" or "deprecated" (the models.dev schema declares exactly these).
  // A model whose status is present, then, is one the plan does not serve
  // the way it serves the rest, so it is skipped.
  let GATED_STATUSES = { alpha: true, beta: true, deprecated: true };

  // The adapters Rienda speaks, mapped to the wire protocol each names.
  // Anything else is out of reach of the current clients and is skipped.
  let PROTOCOLS = {
    "@ai-sdk/openai": "openai_responses",
    "@ai-sdk/openai-compatible": "openai_chat_completions",
    "@ai-sdk/anthropic": "anthropic",
  };

  // The thinking levels every provider documents on its effort option, minus
  // the ones Rienda reserves: "off" and "none" name the picker entry that
  // sends nothing, so a database level either of those names is skipped.
  let RESERVED_LEVELS = { off: true, none: true };

  // cacheSet wraps set so a cache that cannot be stored is logged and
  // dropped: the next start fetches again.
  function cacheSet(text) {
    try {
      ctx.cache.set(CACHE_KEY, text, REFRESH_AFTER_MS / 1000);
    } catch (err) {
      ctx.log("z-ai: cache write failed: " + err);
    }
  }

  // fetchCatalog fetches the models.dev document and returns its text.
  function fetchCatalog() {
    let res = ctx.http.fetch(MODELS_DEV, { timeout_ms: FETCH_TIMEOUT_MS });
    if (res.status !== 200) throw new Error("models.dev returned " + res.status);
    return res.body;
  }

  // catalog returns the models.dev database: the cached text while it lasts
  // (the runtime drops the entry the moment it expires), the live fetch
  // otherwise. Without either, the roster stays empty until the next start.
  function catalog() {
    let cached = null;
    try {
      cached = ctx.cache.get(CACHE_KEY);
    } catch (err) {
      ctx.log("z-ai: cache read failed: " + err);
    }
    if (typeof cached === "string") {
      // The cache holds a parseable document: it wins over everything,
      // including a fetch, because it is younger than the window proves.
      try {
        return JSON.parse(cached);
      } catch (err) {
        ctx.log("z-ai: the cached catalog is not JSON: " + err);
      }
    }

    try {
      let live = fetchCatalog();
      cacheSet(live);
      return JSON.parse(live);
    } catch (err) {
      ctx.log("z-ai: models.dev fetch failed (" + err + "); using the cache");
    }
    if (typeof cached === "string") {
      // A fetch failure with an unparsed cache is the last mile: serve it
      // stale rather than empty.
      try {
        return JSON.parse(cached);
      } catch (err) {
        ctx.log("z-ai: the cached catalog is not JSON: " + err);
      }
    }
    ctx.log("z-ai: no cache and no models.dev: the roster stays empty until the next start");
    return {};
  }

  // protocolOf resolves the wire protocol of a model: the adapter the model
  // declares when it does, the adapter of the provider otherwise. Null when
  // the effective adapter is not one Rienda speaks.
  function protocolOf(providerNpm, model) {
    let modelNpm = model.provider && model.provider.npm;
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
    let options = model.reasoning_options || [];
    for (let i = 0; i < options.length; i++) {
      if (options[i].type !== "effort") continue;
      let modes = [];
      let values = options[i].values || [];
      for (let j = 0; j < values.length; j++) {
        let level = values[j];
        if (RESERVED_LEVELS[level]) continue;
        modes.push({ level: level, max_tokens: 0 });
      }
      return modes;
    }
    return [];
  }

  // roster maps the models.dev models of the plan to the canonical shape.
  function roster(db) {
    let plan = db["zai"];
    let models = [];
    if (!plan || !plan.models) return models;
    let npm = plan.npm;
    // Ascending id order, so the roster reads the same on every start.
    let ids = Object.keys(plan.models).sort();
    for (let i = 0; i < ids.length; i++) {
      let id = ids[i];
      let entry = plan.models[id];
      if (GATED_STATUSES[entry.status]) continue;
      let protocol = protocolOf(npm, entry);
      if (!protocol) continue;
      let limit = entry.limit || {};
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

  let db = catalog();
  let plan = db["zai"] || {};
  if (typeof plan.api !== "string" || plan.api === "") {
    throw new Error("models.dev carries no api endpoint for zai; restarting after a catalog refresh");
  }

  return {
    protocol: "openai_chat_completions",
    base_url: plan.api,
    auth: "api_key",
    models: roster(db),
  };
};
