# Example `runtime.json` configs

Safe templates for the model backends this project has actually been tested
against - no real API keys (every `apiKey` is an empty string placeholder).
Copy one, fill in a real key, and either point `<agent_dir>/runtime.json`
directly at it or symlink it in (see `.agents/LOCAL_TESTING.md` for the
symlink-per-backend pattern used for switching backends without duplicating
an agent's config).

- **`openrouter-auto.json`** - OpenRouter's `"auto"` model routing, which
  picks a backend per-request. `supportsReasoningDetails` is deliberately
  `false` here: OpenRouter's structured `reasoning_details` blocks can be
  tied to the specific backend that generated them, and replaying one to a
  *different* backend than "auto" happens to route to next time can fail
  outright (see `.agents/DECISIONS.md` D6).
- **`openrouter-haiku.json`** - OpenRouter, pinned to a specific model
  (`anthropic/claude-haiku-4.5`) rather than `"auto"` - safe to enable
  `supportsReasoningDetails` since every request goes to the same backend.
- **`llamacpp.json`** - A local `llama.cpp` server (default port `8080`).
  Update `endpoint`/`model` to match your actual server. No reasoning
  fields set - add `reasoningField`/`supportsReasoningDetails` if your
  server's chat template actually emits structured reasoning.

Both OpenRouter examples set `extraBody.reasoning.effort: "high"` - an
OpenRouter-specific passthrough field, not something WackyPubAI interprets
itself. Swap the model string for any other OpenRouter-hosted model and
this still applies.

More examples will be added here as more backends/providers gain explicit
support (see `.agents/TODOS.md` for native Gemini and Anthropic runtime
support, currently deferred).
