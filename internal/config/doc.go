// Package config loads the Rienda configuration file.
//
// The configuration declares the providers the harness can talk to and the
// models each one offers. A provider either references a built-in connection
// preset or describes a custom endpoint with a protocol and a base URL:
//
//	providers:
//	  openrouter:
//	    preset: openrouter
//	    api_key: sk-or-...
//	    models:
//	      kimi-k2:
//	        id: moonshotai/kimi-k2
//	  local:
//	    protocol: openai_chat_completions
//	    base_url: http://127.0.0.1:8080/v1
//	    session_header: x-session-id
//	    models:
//	      my-model: {}
//
// Model aliases are local names; the wire identifier is Model.ID, which
// defaults to the alias. Resolve turns a provider/model reference into the
// connection settings and model defaults needed to talk to it.
//
// A provider inherits the session header of its preset, if any, and custom
// endpoints declare none by default. Setting SessionHeader overrides the
// inherited name and setting it to an empty value disables it.
//
// Decoding is strict: an unknown key is an error, so typos never pass
// unnoticed.
package config
