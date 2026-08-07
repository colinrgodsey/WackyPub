# DECISIONS.md

Design decisions for `WackyPubAI` and the rationale behind them. This is
living documentation: it states what's true now and why, not how it got
there. When a decision changes, this file is rewritten to reflect the
current state - it doesn't accumulate a history of reversals.

Referencing these IDs (D1, D2, ...) is fine here and in code comments. They
don't belong in commit messages or PR bodies aimed at someone who hasn't
read this file.

## D1: `session.jsonl` stores `genai.Content` directly

Each line is a serialized `google.golang.org/genai.Content`
(`{"role": "user"|"model", "parts": [...]}`), not a custom
`{role, content, timestamp}` struct.

**Why**: `genai.Content` already has JSON serialization, already supports
multi-part messages (text, thinking, and eventually images/audio), and is
the type every ADK model adapter natively produces and consumes. A custom
struct would mean writing a lossy conversion in both directions, and losing
information (like reasoning parts) on every round-trip through storage.

## D2: The system prompt is folded into the first user turn, not sent as a `system`-role message

`FolderAgent.GenerateTurn` and `CheckAndCompactSession` both build a single
first turn combining the rendered `AGENTS.md` system prompt and the
`<PERSISTENT_MEMORY>` block, sent with `role: "user"`. `LLMRequest.Config`
never sets `SystemInstruction`.

**Why**: some local-model chat templates (llama.cpp-served Gemma, in
particular) mishandle or reject a `system`-role turn. An earlier version of
this code had a `systemRole` config knob (`"system"`/`"developer"`/`"user"`)
to make this configurable per-agent. That knob was removed: folding the
system prompt into the first user turn works uniformly across every backend
tested so far, and the configurability wasn't buying anything a fixed
behavior didn't already cover.

**Consequence**: `llmagent.Config.Instruction` (set in `BuildADKAgent`) is
never read by the primary generation path - it only matters for the
alternate `RunWithRunner` path. Don't assume setting `Instruction` affects
`generate`/`prompt` output.

## D3: The OpenAI adapter is `achetronic/adk-utils-go`, replaced with a fork

`pkg/agent/openai_model.go` wraps `achetronic/adk-utils-go`'s `genai/openai`
package (which wraps the official `openai-go/v3` SDK), but `go.mod` points
that import at `github.com/colinrgodsey/adk-utils-go` via a `replace`
directive.

**Why**: a hand-rolled HTTP client adapter previously lived directly in this
repo. It worked, but only covered plain-text `content`/`reasoning_content`
and had to be extended by hand for every new provider quirk (tool calls,
images, OpenRouter's block format, etc.). Switching to the official SDK via
`adk-utils-go` gets that coverage for free - except the upstream adapter, as
of the version evaluated, read reasoning correctly on ingest but lost or
mishandled it on egress (see D5, D6). The fork fixes that. See
`ADK_UTILS_GO_REASONING_EGRESS_BUG.md` at the repo root for the original bug
report, and TODOS.md for dropping the fork once upstream catches up.

## D4: Migrated from ADK v1 to `google.golang.org/adk/v2`

**Why**: `adk-utils-go` requires ADK v2. Before migrating, every type this
repo actually uses (`model.LLM`, `llmagent.Config`, `runner.Config`,
`session.Event`) was diffed between v1 and v2 and found unchanged aside from
the import path - the migration was a mechanical `google.golang.org/adk/...`
-> `google.golang.org/adk/v2/...` rewrite plus a `go.mod` bump, not a
behavioral change.

## D5: Reasoning is replayed as history by default (native egress), never stripped

The OpenAI adapter's default (`reasoningEgress: "native"`, or field left
unset) sends captured reasoning back to the provider as its own field on the
next request, rather than omitting it or merging it into `content`.

**Why**: several providers *require* this. DeepSeek V4 in thinking mode
returns a 400 if `reasoning_content` is missing from a prior assistant
message once thinking mode has been used in the conversation. Kimi K2
Thinking expects prior reasoning resent to continue its chain of thought
across tool calls. Dropping reasoning by default (an earlier, mistaken
assumption) breaks both outright; Qwen3 is the only tested provider that
ignores replayed reasoning by default, and ignoring extra data is harmless.

## D6: `reasoning_details` (OpenRouter's structured block format) is captured unconditionally, replayed only when explicitly enabled and only safe with a pinned model

Ingest of OpenRouter's `reasoning_details` array (typed blocks, including
opaque encrypted/signed reasoning) always happens, regardless of
`supportsReasoningDetails`. Egress (replaying the array back as history) is
gated by that config flag.

**Why capture is unconditional**: so turning the flag on later still
replays what earlier turns already recorded, instead of losing history that
predates the flag.

**Why egress is gated, and why it needs a pinned model**: encrypted/signed
blocks are cryptographically tied to the exact backend endpoint that
produced them. `"model": "auto"` on OpenRouter can route a later request to
a *different* endpoint, and that endpoint can't decrypt reasoning it didn't
produce - OpenRouter returns a 404 ("Encrypted payloads can only be replayed
to the endpoint that created them"). This was hit and confirmed live, not
just inferred from docs. `supportsReasoningDetails: true` is only safe with
`model` pinned to a specific slug.

**Corollary**: `StripReasoningDetails`/`StripSessionReasoningDetails` exist
specifically to remove stale block metadata (never readable `Thought` text)
when an agent switches away from a backend that emitted encrypted blocks -
see the `strip-reasoning` CLI command.

## D7: `ContentText` always excludes `Thought`-marked parts

The helper used for the CLI's printed/returned response and for
`MEMORY.md` addenda strips `Thought` parts before joining text; full
`genai.Content` (with `Thought` parts intact) is what gets persisted to
`session.jsonl`.

**Why**: reasoning is internal monologue, not something the character said
out loud. Showing it as if it were dialogue, or letting it leak into a
compacted memory summary, breaks the fiction and pollutes memory with
scratch reasoning instead of actual events/decisions.

## D8: `EstimateTokens`'s inclusion of reasoning text is gated by `RuntimeConfig.PreserveThinking`, not a fixed choice

**Why**: whether replayed reasoning actually counts against a model's
context budget depends on the backend. Providers that require it resent
(D5) bill for and consume context on that text every turn; providers that
ignore replayed reasoning (Qwen3-style) don't. A fixed always-count or
never-count policy would be wrong for one side or the other, so the
`preserveThinking` config flag exists to let the agent's own operator state
which behavior applies to their backend.

## D9: Consecutive `user` turns are merged only at request-build time, never in storage

`MergeConsecutiveUserTurns` runs inside `GenerateTurn` and
`CheckAndCompactSession`, right before a request is sent to the model. It is
never applied to what `AppendSessionContent`/`WriteSessionTurns` persist.

**Why**: `session.jsonl` legitimately accumulates consecutive user turns
(multiple `add` calls without an intervening `generate`, and the injected
system-prompt+memory turn landing before whatever the first real turn is,
which is usually also `user`). Storage should stay a simple, permissive log
of what actually happened. Some backends' chat templates reject or
mishandle non-alternating roles, so the *request* needs normalizing, but
collapsing turns in storage would throw away the actual turn boundaries for
no benefit.

## D10: `runtime.json`'s API key field is `apiKey`, not `api_key`

**Why**: every other field in `RuntimeConfig` is camelCase
(`sessionCompactPct`, `contextWindow`, `reasoningEgress`, ...). `api_key`
was a snake_case holdover; renamed for consistency. No backward-compatible
alias was kept - this is a config file under active development, not a
public API with external consumers to avoid breaking.

## D11: The CLI is a thin wrapper over `AgentSDK`; every subsystem gets its own `pkg/<name>` + `<Name>SDK` + `wackypub <name>` triple

Every `agent` CLI subcommand's `RunE` does argument parsing and formatting
only - the actual work happens in an `AgentSDK` method
(`pkg/agent/sdk.go`), which itself delegates to a package-level function
that doesn't acquire the session lock (so it's independently reusable). No
CLI-only business logic is allowed to live in `cmd/agent.go`.

**Why**: the CLI's primary consumers are expected to be other agent
platforms shelling out to `wackypub`, not humans - and a planned in-process
tool layer will call `AgentSDK` methods directly, skipping the CLI/subprocess
round-trip entirely. Keeping the SDK method as the single place behavior and
documentation live (see the "CLI/SDK/tool documentation" pattern in
`.agents/AGENTS.md`) is what makes that layer addable later without
duplicating or re-deriving behavior that already exists in `cmd/agent.go`.

**Corollary - `pkg/roleplay` and `pkg/cluster` were removed**: they were a
second, YAML-config-driven (`wackypub.yaml` personas/clusters) way to define
and orchestrate agents, existing in parallel with folder-based agents rather
than building on `AgentSDK`. Folder-based agents already cover defining and
driving an agent; the parallel system wasn't earning its maintenance cost
and didn't fit the CLI-is-a-wrapper-over-SDK shape (its commands read/wrote
YAML config directly in `RunE`). If multi-agent orchestration is needed
again, build it as `AgentSDK`-backed methods plus a `pkg/roleplay`-style
package following this same pattern, not as a parallel config format.

## D12: `workspace` is a top-level command, and is read-only with no scaffolding

`wackypub workspace [agent_id]` sits at the root (`wackypub workspace ...`),
not under `agent` (`wackypub agent ... workspace`), and it never creates or
modifies a file - not even the agent directory itself when inspecting an
agent that doesn't exist yet, which every other `AgentSDK` method does via
`os.MkdirAll` as a side effect of acquiring the session lock.

**Why top-level**: it reports on the workspace and on agents within it, but
it isn't itself an agent-session operation the way `add`/`generate`/`prompt`
are - it doesn't touch any particular agent's conversation. Nesting it under
`agent` would suggest it belongs to the same category of operation as those.

**Why read-only, no scaffolding**: the goal is to replace a prose
description of "how to structure a workspace" (in a skill file or doc that
can drift from what the code actually checks for) with something an agent
platform can run against its own directory and get a truthful, current
answer from. Mixing that in with "and also let me create the missing files
for you" would conflate two different concerns - what "correct" starter
content for `AGENTS.md` or `runtime.json` should look like is a separate,
underspecified design question, and answering it wrong as a side effect of
a diagnostic command is worse than not answering it. If scaffolding is
wanted later, it should be its own explicitly-named operation (e.g.
`agent <id> init`), not something `workspace` does implicitly.
