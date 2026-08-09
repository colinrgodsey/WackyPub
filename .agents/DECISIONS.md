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

**Corollary**: `StripSignatures`/`StripSessionSignatures` exist
specifically to remove stale block metadata (never readable `Thought` text)
when an agent switches away from a backend that emitted encrypted blocks, or
between providers entirely (also covers Gemini's `ThoughtSignature` field,
confirmed live to be rejected outright when replayed to Anthropic) - see the
`strip-signatures` CLI command.

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

**Why**: two different kinds of caller need the same operations. The CLI's
primary consumers are agent platforms shelling out to `wackypub` as a single
command per call (see D13) - they only ever see the CLI surface. Separately,
a Go-native agent platform or other implementer can `import
"github.com/colinrgodsey/WackyPubAI/pkg/agent"` and call `AgentSDK` directly,
skipping the CLI entirely. Keeping the SDK method as the single place
behavior and documentation live means both callers get the same behavior
for free, and neither one requires re-deriving or duplicating what already
exists in `cmd/agent.go`.

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

## D13: An agent's tool for using WackyPubAI is single `wackypub` command execution, not a shell and not a hand-authored tool schema

The intended integration for an LLM agent platform is a tool constrained to
running one `wackypub` subcommand per call - not general shell/bash access
(no pipes, no other binaries, no scripting), and not a bespoke
function-calling schema wrapping `AgentSDK` methods one by one.

**Why not a bespoke tool schema**: the CLI's `--help` output (`Short`/`Long`
on every command, per D11's argument-completeness requirement) already *is*
a complete, accurate description of every operation. A hand-authored schema
listing every command and argument up front would either duplicate that
text (and drift from it over time) or under-specify it to stay within a
reasonable context budget. A single root command sidesteps both problems:
an agent explores progressively (`wackypub --help` -> `agent --help` ->
`agent generate --help`), paying token cost only for the part of the
surface it actually needs on a given turn, instead of every registered tool
being listed regardless of relevance - which is the usual failure mode of
large tool-listing setups (context bloat, and models confusing
similar-sounding tools).

**Why not general shell access**: constraining the tool to "run one
`wackypub` subcommand" keeps the blast radius no larger than `wackypub`'s
own command set. An agent with this tool cannot `rm -rf`, pipe output into
another program, or otherwise act outside what `wackypub` itself permits,
unlike raw bash access.

**Where skills fit**: if a specific task pattern needs more guidance than
`--help` economically provides on its own (e.g. a multi-step workflow spread
across several commands), a skill supplements it. A skill should describe
*when* and *why* to use which commands together, and point at `--help` for
argument details - not duplicate argument documentation that already lives
in the CLI, for the same drift reason a hand-authored tool schema would.

**Corollary**: `AgentSDK` (see D11) remains a fully separate, independent
integration path for Go-native agent platforms or other implementers that
want direct in-process calls without running a subprocess at all. The two
paths aren't in tension - they're different callers with different needs,
and both are already fully supported today.

## D14: Agent tools are executables discovered under `tools/`, not a hand-authored registry

An agent's available tools are whatever executable files are found by recursively walking `<agent_dir>/tools/` (itself possibly a symlink, and free to contain symlinks to shared "toolpack" directories rather than individual tool files) - mirroring the existing symlink-sharing pattern used for `runtime.json` and `AGENTS.md`/`IDENTITY.md`.

**Symlink Resolution Mechanics**:
- `DiscoverAgentToolsMap` resolves directory and file symlinks using `os.Stat` and `filepath.EvalSymlinks`, traversing into symlinked "toolpack" directories (e.g. `tools/read-only-fs` -> `/path/to/toolpack`) and discovering the executable files inside them (`cat`, `ls`, `man`).
- Symlinks pointing directly to executable files are followed to their target, registering the symlink's basename as the discovered tool.
- Broken symlinks are ignored safely without halting traversal.
- Visited real directory paths are tracked to prevent infinite recursion on circular symlink loops.

**Why filesystem discovery over a hand-authored tool registry**: same
rationale as D13 - an executable that self-describes (name, description,
argument schema, on request) is a single source of truth for its own tool
definition, the same way `--help` is for a CLI command. A separate
hand-maintained list of "here are agent X's tools and their schemas" would
duplicate what the executables already know about themselves and drift out
of sync. The exact self-description query protocol (what flag or convention
a tool executable responds to) is not settled yet.

**Name collisions are accepted, not an error.** If multiple toolpacks
symlinked into `tools/` happen to contain a same-named executable, the one
that "wins" is whichever the traversal encounters last - and that order is
*not* guaranteed deterministic across filesystems, deliberately. Anyone
symlinking in toolpacks with generically-named tools that might collide is
expected to give the link itself a distinguishing name if they care which
one wins. `workspace <agent_id>` should surface detected shadowing in its
diagnostic output (see D12) so it's discoverable when someone needs to know,
without requiring the discovery process itself to block or warn.

**Scope of this decision**: it covers tool *discovery* and *naming* only.
The actual tool-use loop (model returns a `FunctionCall` part -> exec the
matching tool -> append a `FunctionResponse` -> generate again -> repeat
until the model stops calling tools) is a separate, larger, not-yet-designed
piece of work - `GenerateTurn` is single-shot today (see D2's description of
the current generation path).

## D15: Workspace root is marked by a `WACKYPUB_ROOT` file, discovered by walking up from CWD only when `--ws` isn't explicit

**Not implemented yet - design only, see TODOS.md.** Every valid workspace
must have an (empty, content doesn't matter) `WACKYPUB_ROOT` file directly
in its root directory - the same marker-file pattern `.git`, `package.json`,
and `Cargo.toml` use for their own tools' root discovery.

- If `--ws X` is passed explicitly, `X` itself must directly contain
  `WACKYPUB_ROOT`; error if it doesn't. No walking past an explicitly-given
  path - explicit intent is respected, not second-guessed.
- If `--ws` isn't given (the default), walk up from the current directory
  looking for the nearest ancestor containing `WACKYPUB_ROOT`, and use that
  as the workspace root. Error if none is found up to filesystem root -
  never silently fall back to treating an arbitrary CWD as a workspace.

**Why**: the concrete problem this fixes already happened - running
`wackypub` from the wrong directory with no `--ws` given silently created
`session.lock`/`session.jsonl` files wherever CWD happened to be, since the
old default was a bare `.`. A hard marker-file requirement makes that
failure mode impossible: either you're somewhere under a real workspace, or
you get a clear error, never a silently-wrong location.

The walk-up also does double duty once tool invocation exists: a tool
executable (or `wackypub` invoked as a tool - see D16) running with CWD
somewhere under an agent's own folder doesn't need `--ws` passed to it at
all; it discovers its own workspace root automatically.

Bootstrapping a new workspace's `WACKYPUB_ROOT` file has no dedicated
command yet (`touch WACKYPUB_ROOT` is sufficient by hand) - see TODOS.md for
whether that's worth a command later.

## D16: Cross-agent tool invocation is governed by two separate mechanisms - `WACKYPUB_ALLOWED_AGENTS` for authorization, `WACKYPUB_CALL_CHAIN` for deadlock safety

Agents can invoke `wackypub` itself as a discovered tool (e.g. to message
another agent - see D17). Two failure modes need independent guards: an
agent reaching an agent nobody authorized it to reach, and an agent
reaching (directly or transitively) back to itself mid-generation.

**`WACKYPUB_ALLOWED_AGENTS`** is a file in an agent's own folder listing
which other agent IDs it's permitted to target via a `wackypub`-as-tool
invocation. It's checked against CWD *before* the `WACKYPUB_ROOT` walk-up
(D15) - a cheap, local check that can reject before even resolving the
workspace root. **If the file doesn't exist, the default is deny-all**: no
cross-agent access without an explicit opt-in allowlist. This also covers
self-targeting for free - an agent's own ID simply isn't in its own
allowlist unless someone deliberately puts it there, so no separate
self-check is needed.

Whether this check applies to *any* invocation with a matching CWD
(including a human manually running `wackypub agent <id> ...` from inside
another agent's folder for debugging) or only to invocations actually
happening as a tool call from a live generation is an open question - see
TODOS.md. Until it's resolved, assume the simpler CWD-only check.

**Why authorization alone doesn't prevent deadlocks**: session locking
(`session.lock`, see `.agents/AGENTS.md`'s gotchas) does not provide cycle
protection - it produces a *deadlock*, not a clean rejection. If agent X's
live generation (process A, holding X's session lock) spawns a tool call
that invokes `wackypub agent X ...` again (process B), process B blocks
trying to acquire the same lock, and process A can't release it until B's
tool call returns - both hang forever. Worse, `WACKYPUB_ALLOWED_AGENTS`
alone doesn't close this either: if X's allowlist includes Y and Y's
allowlist includes X, that authorizes exactly the X -> Y -> X cycle that
deadlocks, just deliberately instead of accidentally. Authorization and
cycle-safety are different concerns and need different mechanisms.

**`WACKYPUB_CALL_CHAIN`** is the cycle-safety mechanism: an environment
variable carrying the list of agent IDs already active in the current call
stack (e.g. `bob,jax`), which each `wackypub`-as-tool invocation appends its
own agent ID to before spawning a further tool subprocess (env vars
propagate to child processes for free). Before targeting an agent, refuse
immediately if that agent ID is already present in the chain - regardless
of what any `WACKYPUB_ALLOWED_AGENTS` file permits. This is a structural
guarantee (catches a cycle of any length, doesn't depend on anyone
configuring allowlists acyclically), not a policy one, and it's cheap (no
filesystem access, just an inherited env var).

**Division of responsibility**: `WACKYPUB_ALLOWED_AGENTS` decides who's
authorized to talk to whom. `WACKYPUB_CALL_CHAIN` guarantees that no
authorized configuration can still produce a deadlock. Neither is a
substitute for the other.

**Concurrency Scope Note**: `WACKYPUB_CALL_CHAIN` environment variable propagation is designed for subprocess CLI calls (where each spawned tool process inherits its own environment snapshot). For multi-threaded Go SDK consumers calling `AgentSDK` directly across concurrent goroutines in the same process, `os.Setenv` is process-global; if concurrent in-process Goroutine SDK calls targeting different agents are required in the future, `context.Context` key propagation can supplement env var checks.

**Read-only inspection is deliberately exempt**: `AgentSDK.InspectAgent` (and therefore `wackypub workspace`) does *not* go through `ValidateAgentTarget`. This authorization boundary exists to gate cross-agent tool invocation/generation - something that can cause a side effect or a deadlock risk - not read-only diagnostic visibility, which has neither. Gating it the same way was a real bug in practice: an agent inspecting the workspace (including inspecting *itself*, since an agent's own ID isn't automatically in its own allowlist) would get an authorization failure that `wackypub workspace`'s summary table rendered as a generic `"error"` in the RUNTIME.JSON column - indistinguishable from an actually-broken config file, and actively misleading. This exemption is scoped narrowly to inspection; mutating/generating SDK methods still go through the full check, and the open question in TODOS.md about whether CWD-based *mutating* invocations should be scoped differently remains unresolved.

## D17: Tool execution loop and self-describing tool protocol

`<agent_dir>/tools/` executables discovered per D14 are registered as LLM function declarations on each generation request.

**Tool Schema Registration**:
- Built-in tools (`create_scratchpad`, `get_scratchpad`, `list_scratchpads` - see D18) and a single generic `run_command` tool covering every discovered executable are constructed as Google ADK `tool.Tool` instances using `google.golang.org/adk/v2/tool/functiontool.New`.
- Strongly typed Go structs automatically generate their `genai.FunctionDeclaration` schemas and handle JSON argument unmarshaling and type validation.
- Discovered executables under `tools/` are **not** individually registered as separate function declarations - they're dispatched through the one `run_command` tool's `command` argument. `run_command`'s description is built dynamically at agent-load time from the discovered command names, plus general usage guidance (see below), so an agent gets both "what commands exist" and "how command invocation works in general" from a single tool description instead of N near-duplicate `"Command <Name>"` descriptions with no shared context.
- The command list embedded in the description is always alphabetically sorted (`DiscoverAgentToolsMap` already sorts before returning) - filesystem readdir order is not guaranteed stable across runs, and an unsorted list would change the description's bytes between generations for no reason, defeating prompt caching on every single request.

**`run_command` Usage Guidance** (baked into its description, not repeated per-agent in AGENTS.md):
- The working directory is always the agent's own directory - there's no way to `cd` elsewhere, since commands don't chain (see TODOS.md for the deliberately-deferred question of whether that's ever needed).
- `args` entries are passed as literal argv elements, not shell-parsed - no quoting or escaping needed for spaces/special characters.
- The agent's scratchpad may already contain the data it needs - check before running a command to regenerate something already available.
- Running a command with no arguments or `--help` is a legitimate way to learn what it is, how to use it, and what arguments it takes.

**Tool Execution Protocol**:
- When the model returns a `FunctionCall` part for `run_command` naming a command `X`:
  - The handler validates `X` against the discovered command map first and returns a real error for an unrecognized command (ADK's own dispatch can no longer catch this, since `run_command` itself is always a valid, registered tool).
  - `wackypub` executes `<agent_dir>/tools/X` (or its discovered path) with the positional `args` list.
  - Any key-value entries in `env` are added to the subprocess environment (along with inheriting process environment including `WACKYPUB_CALL_CHAIN`).
  - Full raw JSON arguments are also passed to the tool's stdin and as `WACKYPUB_TOOL_ARGS`.
  - `wackypub` captures stdout (and stderr on failure) from the subprocess.
  - A `FunctionResponse` part is constructed with the response output and appended to the session log.

**Loop Termination & Max Tool Turns**:
- `GenerateTurn` executes in a loop: after receiving tool calls and appending tool responses, it invokes the model again until the model returns a text response (no function calls) or the maximum tool turns limit is reached.
- Default max tool turns is 10 per `GenerateTurn` invocation, configurable via the persistent `--max-tool-turns` CLI flag.

**Tool Error Signaling**:
- `executeTool` returns `(string, error)`, not just a formatted string. A non-zero exit, a missing binary, or a scratchpad read/write failure surfaces as a real Go `error`.
- Every `functiontool.New` handler (`create_scratchpad`, `get_scratchpad`, `list_scratchpads`, and `run_command`) propagates that error as its own return value instead of packing failure text into a normal-looking result and always returning `nil`.
- Google ADK's own tool dispatch (`internal/llminternal/base_flow.go`) turns a non-nil handler error into a `FunctionResponse.Response` shaped as `{"error": "<message>"}`, structurally distinct from the `{"output": "..."}` shape a successful call produces - no extra callback wiring required on our side.

**Why**: A uniform `{args: []string, env: map[string]string}` schema allows arbitrary CLI tool binaries under `tools/` to be invoked natively as subprocesses without requiring every tool to author custom schema metadata parser extensions, keeping discovery fast and compatible with any shell command. Error signaling matters because a model can only react differently to a tool failure if the failure looks different on the wire - previously, `"Error executing tool X: ..."` was just prose living in the same `output` field a successful call would use, so recognizing a failure depended entirely on the model reading it as English rather than on any structural signal. Collapsing per-discovered-tool declarations into one `run_command` mirrors how most modern coding agents actually work (a single shell/command-execution tool, not one declaration per binary) - it lets general operating guidance (working directory behavior, argv conventions, "check your scratchpad first," "use `--help` to explore") live in exactly one place instead of being absent or repeated per tool, at the cost of the model no longer seeing each command as its own named function in the schema.

## D18: Scratchpad system - collision-safe IDs, bounded retention, and inline macro expansion for command I/O

Agents can store text payloads and intermediate command output in a persistent, session-level scratchpad (`<agent_dir>/scratchpad.json`) instead of paying to regenerate or re-read that data through the model on every turn it's needed.

**Why a scratchpad at all**: three distinct token-economics wins, not just "big output goes somewhere else." (1) An agent can pipe one agent's response directly into another tool call without ever entering those tokens into its own context. (2) One write can be reused across multiple downstream calls (send the same text to three peer agents) without re-entering it each time. (3) Even reading a scratchpad back is comparatively cheap - it's tokens the model consumes, not tokens it has to *generate*, and generation is the expensive side of that trade.

**Tools**:
- `create_scratchpad(text: string) -> id`: stores `text` under a freshly generated ID and returns it. Replaces the old `set_scratchpad(id, text)` - the caller never chooses an ID, which is what makes the concurrency race below structurally impossible rather than just discouraged.
- `get_scratchpad(id: string, skip_lines?: int, num_lines?: int)`: retrieves the stored text, optionally paginated by line range.
- `list_scratchpads() -> [{id, seq, size, created_by}]`: metadata-only forensic listing of every currently-live entry (plus live-count/cap), described below.

**ID shape and eviction**:
- IDs are a randomly generated 4-character string from `[0-9a-z]` (~1.68M possible values), collision-checked against currently-*live* entries only - a since-evicted ID is fair game to reuse, since nothing is stored there anymore.
- Each entry also stores a monotonically increasing `seq` integer (`max(existing seq) + 1` at creation), used purely for FIFO ordering - it's never shown as the entry's identity, only used internally to decide what to evict.
- The scratchpad holds a bounded number of live entries (default 50). When a new entry would exceed that cap, the entry with the lowest `seq` is evicted - its data is actually deleted, not just marked stale.
- Because eviction deletes rather than tombstones, a lookup miss on `get_scratchpad` is **structurally indistinguishable** between "this ID was evicted" and "this ID never existed" - there's no surviving record to tell the difference. `list_scratchpads` exists specifically to give a confused agent a way to check current reality directly, rather than trying (and failing) to explain history we no longer have.

**Concurrent Same-Turn Race - now closed by construction**: Google ADK dispatches every `FunctionCall` in a single model response concurrently (`handleFunctionCalls` spawns one goroutine per call, no ordering guarantee), which previously meant a model calling `set_scratchpad` and something consuming that slot in the *same* turn could have the read execute before the write landed. Since `create_scratchpad` always returns a freshly generated ID in its response, a model cannot reference a slot before seeing the response that creates it - it has no way to know the ID in advance. The race isn't mitigated anymore, it's impossible to trigger.

**`run_command` I/O integration** (see D17 for `run_command` itself):
- `args[]` entries and a new `stdin` template string both support inline `<SCRATCHPAD_DATA id="X" skip_lines="N" num_lines="M" />` macro expansion, resolved server-side against stored scratchpad content *before* the subprocess is built - never round-tripping the data through model-generated tokens. This replaces the old bare `stdin_scratchpad_id` integer field: `stdin` is now a template (which can be just the macro alone, or the macro embedded in a larger string with a prefix/suffix).
- Any single argument, after macro expansion, that exceeds 500,000 bytes fails with an explicit internal error before `exec` is ever called (`"expanded argument exceeds 500000 bytes (was N) - use stdin/stdout scratchpad redirection instead"`) rather than surfacing a raw OS `E2BIG`.
- `run_command` is the only tool that auto-creates scratchpad entries from its own output, since it's the only unbounded-output producer in the system (`create_scratchpad`/`get_scratchpad`/`list_scratchpads` all have naturally small, self-limiting output). Past a size threshold, stdout/stderr are each captured into a fresh scratchpad entry instead of being inlined; the response is tagged either way so the shape is uniform and self-documenting:
  - `<STDOUT><SCRATCHPAD_DATA id="k3p1" /></STDOUT><STDERR><SCRATCHPAD_DATA id="k3p2" /></STDERR>` (both large)
  - `<STDOUT><SCRATCHPAD_DATA id="k3p1" /></STDOUT>` (only stdout was large; nothing on stderr)
  - `<STDOUT><SCRATCHPAD_DATA id="k3p1" /></STDOUT><STDERR>low memory</STDERR>` (stdout large, stderr small enough to inline)
  - `<STDOUT>operation complete</STDOUT>` (both small enough to inline directly)
- `env` map values are explicitly **not** macro-expanded - env vars are expected to stay small, and adding a second expansion surface for something that doesn't need it isn't worth the complexity.

## D19: Folder agents migrate to Google ADK `runner.Runner` backed by `FileSessionService`

Agent generation turns migrate from manual `model.LLMRequest` construction to Google ADK's `runner.Runner` engine, backed by a custom `FileSessionService` (`pkg/agent/file_session_service.go`).

**FileSessionService & Storage Compatibility**:
- Implements ADK's `session.Service` interface (`Create`, `Get`, `List`, `Delete`, `AppendEvent`).
- Reads and writes serialized `genai.Content` objects directly from/to `<agent_dir>/session.jsonl` under the session lock, preserving 100% backward compatibility with existing agent sessions and file formats.

**In-Memory Event List Synchronization**:
- `FileSessionService.AppendEvent` appends new events (`evt`) directly to the in-memory `fileSession.events` list in addition to writing to `session.jsonl` on disk.
- Ensures subsequent iterations within a multi-turn tool execution loop read live assistant tool calls and user tool response events from `sess.Events()` rather than a frozen snapshot.

**Event Author**:
- `FileSessionService.Get()` sets `evt.Author` to the actual agent id for model-role turns and `"user"` for user-role turns, not the raw `genai.Content.Role` string. ADK's runner expects `Author` to be a real participant identifier and logs "Event from an unknown agent" warnings (spamming the console on every turn of loaded history) if it's just handed the literal string `"model"`.

**System Prompt & Persistent Memory Layout**:
- Rendered `AGENTS.md` is passed directly as `Instruction` on `llmagent.Config` (ADK's native system prompt).
- `FileSessionService.Get()` formats `MEMORY.md` (`<PERSISTENT_MEMORY>`) as User Turn 1 without prepending `AGENTS.md`, eliminating duplicate system prompt messages.
- Consecutive user turns are merged (`MergeConsecutiveUserTurns`) to ensure prompt cache consistency and model template compatibility.

**Runner Execution & User Turn Handling**:
- `GenerateTurn` passes `msg = nil` into `r.Run(ctx, "user", agentID, nil, ...)` because user turns are already loaded from disk via `SessionService.Get()`. This prevents duplicate user turn appending.

**Tool Loop Termination & `--max-tool-turns` Cap**:
- `LoadFolderAgent` accepts `maxToolTurns int` (defaulting to 10 if <= 0) and threads it to `BuildADKAgent` at agent construction time.
- `AgentSDK` passes `s.MaxToolTurns` into `LoadFolderAgent`, binding the CLI flag `--max-tool-turns` directly to `llmagent.Config`'s `BeforeModelCallbacks`.
- When consecutive tool loop requests exceed `maxToolTurns`, `BeforeModelCallback` skips the model call and returns `"exceeded maximum tool turns limit (%d)"`.

**Compaction**:
- Compaction remains single-shot via `CheckAndCompactSession` before generation runs.

**Why**: Using ADK's `runner.Runner` and `session.Service` standardizes agent execution on official Google ADK primitives while preserving full filesystem control, multi-turn tool loop fidelity, and `session.jsonl` compatibility.

## D20: Skills system - distilled, discoverable knowledge for agents

Agents can be given pre-written, distilled guidance ("skills") instead of having to re-derive how something works from raw `--help` output or trial and error every session - the same problem `run_command`'s baked-in usage guidance (D17) addresses for command execution in general, but for anything else worth writing down once.

**Discovery**:
- A `skills/` folder per agent, discovered recursively the same way `tools/` is (D14) - including symlinked "skill packs" shared across agents the same way toolpacks are today.
- A skill is a folder containing `SKILL.md` with YAML frontmatter: standard `name` and `description` fields, matching the format other agent harnesses already use so off-the-shelf skills can be dropped in as-is, plus one non-standard field: `always_load: true`. `gopkg.in/yaml.v3` is already a dependency (`pkg/config/config.go`), so this doesn't add a new one.

**On-demand skills** (the default, `always_load` unset or `false`):
- Only `name` + `description` are ever in context - surfaced in the `load_skill` tool's own dynamically-built description, the same pattern `run_command` uses for its command list (D17), always alphabetically sorted by skill name for prompt-cache stability.
- `load_skill(name)` returns the skill body as a normal tool response (`FunctionResponse`) - the same mechanical pattern `get_scratchpad`/`run_command` already use. There's no "inject into system prompt mid-session" mechanism to build, since ADK's `Instruction` isn't mutable per-turn anyway; a loaded skill just becomes part of the ordinary conversation history from that point on, same as any other tool result.

**Always-loaded skills** (`always_load: true`):
- Excluded entirely from `load_skill`'s registry - no on-demand entry for something already in context.
- Bodies are concatenated onto the end of the rendered `Instruction` string (`macro.go`'s system prompt rendering, alongside AGENTS.md), sorted alphabetically by skill name, wrapped as `<AUTOLOADED_SKILLS><SKILL name="...">...</SKILL>...</AUTOLOADED_SKILLS>`.

**Why**: Mirrors D17's `run_command` reasoning - a short, always-visible description plus content loaded only when actually needed keeps the always-in-context cost near zero while still making distilled knowledge discoverable, rather than forcing a choice between "nothing is ever pre-written" and "everything is always in every prompt." Reusing the standard `SKILL.md` + YAML-frontmatter shape (rather than inventing a new format) means existing skills written for other harnesses work here with no translation beyond the one added `always_load` field. Loading a skill as a normal tool response - not a special system-prompt mutation - keeps the mechanism consistent with everything else in the tool-calling system and avoids building a second, bespoke content-injection path alongside the one `FunctionResponse` already provides.

## D21: Explicit model provider selection (`"openai"`, `"gemini"`, `"anthropic"`) and Anthropic ADK model adapter

Agent runtime configurations (`runtime.json`) support explicit model provider selection via a `provider` field (`"openai"` | `"gemini"` | `"anthropic"`), eliminating implicit fallback ambiguity and enabling native Anthropic Claude models via `github.com/achetronic/adk-utils-go/genai/anthropic` (resolved to the `colinrgodsey` fork - see D3).

**Provider Resolution (`runtime.json`)**:
- `"openai"` (or `"openai-compatible"`): instantiates `NewOpenAIModel` (`pkg/agent/openai_model.go`).
- `"anthropic"`: instantiates `NewAnthropicModel` (`pkg/agent/anthropic_model.go`), backed by `github.com/achetronic/adk-utils-go/genai/anthropic`.
- `"gemini"`: instantiates `CreateGeminiModel` (`pkg/agent/adk_agent.go`), backed by native `google.golang.org/adk/v2/model/gemini`.
- **Defaulting & Backwards Compatibility**: If `provider` is empty or unset, `LoadRuntimeConfig` defaults to `"openai"` if `endpoint` is set, or `"gemini"` if `endpoint` is empty.

**Anthropic Thinking Knobs**:
`RuntimeConfig` exposes native Anthropic thinking/reasoning configuration:
- `thinkingBudgetTokens`: Sets classic token budget for Anthropic thinking (`"enabled"` mode, e.g. 1024).
- `thinkingEffort`: Sets reasoning effort (`"low"`, `"medium"`, `"high"`) for adaptive thinking mode.
- `thinkingMode`: Sets thinking mode (`"enabled"`, `"adaptive"`, or empty for auto-detection).

**Why**: Explicit provider selection removes provider ambiguity and gives every LLM backend (OpenAI-compatible gateways, native Gemini ADK models, and native Anthropic Claude models) first-class runtime config support with dedicated thinking/reasoning controls.

## D22: `files-rw` - standalone read/write/edit/patch/list tool gated by a per-directory access file

A companion binary, `files-rw` (`cmd/files-rw/main.go` + `pkg/filesrw/`, same module as `wackypub` - not a separate repo, so it can reuse conventions like the comment/blank-line rule parsing already established for `WACKYPUB_ALLOWED_AGENTS` rather than duplicating it), gives agents an explicit, per-directory-scoped read/write/edit/patch/list tool suite instead of relying solely on the invoking harness's own sandboxing. It's registered like any other agent tool: symlinked into `<agent_dir>/tools/`, invoked via the generic `run_command` tool with a real argv (never shell-parsed) and `cmd.Dir` set to the agent's own directory (see D14, `pkg/agent/agent_folder.go`) - which is why the access grant below can safely be scoped to "the tool's cwd" with no upward search.

**Access grant (`FILES_RW_ACCESS`)**:
- Exact filename, read only from the tool's cwd, never searched upward - since `cmd.Dir` is always the agent's own directory, this is always that agent's own grant, never inherited from a parent.
- Missing file -> deny everything. No default-allow, no partial trust.
- One rule per line: `w: <path>` or `r: <path>`. `w` implies `r`. Blank lines and `#`-prefixed lines are ignored (mirrors `WACKYPUB_ALLOWED_AGENTS`, D16).
- Re-read fresh on every invocation - no caching, no state carried between runs.
- The access file's own canonical path is always denied, unconditionally, even to a rule that would otherwise cover it (e.g. a broad `r: .`).

**Path handling**:
- `~` is rejected outright (fail loud) wherever it can appear - in a rule's path or a request target. Argv-based invocation means the shell never expands `~` for the tool; a literal `~` reaching it is almost certainly the model assuming shell semantics that aren't happening, not a case worth silently guessing at (`$HOME`) - matches the project's established preference for loud failure over silent disambiguation (see D-note on the Gemini thinking-config conflict fix).
- Relative paths, in both rules and request targets, resolve against the tool's cwd.
- Every path - granted root and request target alike - is canonicalized via `filepath.EvalSymlinks` before any containment check, so a symlink inside a granted root that points outside it can't be used to escape it. A granted root must already exist (fails loudly at load time otherwise); a request target may not (`write` creates new files) - in that case the longest existing ancestor is resolved through symlinks and the not-yet-existing tail is trusted as given relative to that resolved ancestor.
- Containment is a path-separator-aware boundary check (`path == root || strings.HasPrefix(path, root+sep)`), not a naive string prefix - avoids a false-positive match of a granted `/home/bob/Downloads` against a request for `/home/bob/Downloads-secret`.

**Command surface**:
- `read <path> [--start N] [--end N]`: `cat -n`-style numbered output, whole file by default. No built-in pagination - relies on the existing `run_command`-to-scratchpad redirect (D18) for output too large for one tool response, same as any other tool.
- `write <path>`: content via stdin only (plays cleanly with D18's `<SCRATCHPAD_DATA id="X" />` stdin macro for large content without spending model output tokens). Atomic (temp file + rename). Auto-creates missing parent directories, bounded inside the already-validated writable root.
- `edit <path> --old <str> --new <str> [--replace-all]`: exact string replace, implemented directly rather than shelled to `patch` - rejects zero or more-than-one match unless `--replace-all` is given, so the caller supplies more surrounding context instead of an edit silently landing on the wrong occurrence.
- `patch <path>`: unified diff via stdin, shelled to the system `patch` binary (`-o <tempfile>` then atomic rename over the target) - the actual "piggyback on `patch`" piece, for multi-hunk edits `edit` isn't suited for.
- `list <path> [-l] [-a] [-R]`: shells to `ls`, but only ever with a fixed, hardcoded set of boolean flags translated ourselves - deliberately not raw argv passthrough, since that would let a second positional path argument slip past access control entirely (`ls` lists every path it's given; only the first would ever get checked).
- `read`/`edit` refuse binary files (NUL-byte heuristic) - numbering/string-replace don't mean anything on non-text content.

**Why**: Tool-level filesystem access in this project so far has been all-or-nothing (whatever `<agent_dir>/tools/` symlinks in, the executable gets whatever access the OS user running it has). `files-rw` adds an explicit, fail-loud, per-agent-directory allowlist on top, without requiring every future file-touching tool to reimplement its own sandboxing.

## D23: `files-rw` round two - `copy`/`move`/`delete`, `read` defaults to raw (no numbering), `patch` restricted to unified diffs, hard read size cap

Found live: gave a wiped-clean test agent (clerk, no other file tools) `files-rw` and an 82KB source file, asked for a copy. `files-rw` has no `copy` command, so the only path available was `read` piped through the scratchpad into `write` - but `read`'s default output is `cat -n`-style numbered (`D22`), and clerk had no way to know that numbering wasn't part of the real file content. The "copy" came out with a `<N>\t` prefix injected into every line, and clerk reported success without re-reading to verify. Re-run with a raw `cat` available (not `files-rw read`) alongside `files-rw`, the same read-through-scratchpad-into-write pattern produced a byte-identical copy - confirming the numbering, not the pipe pattern itself, was the actual bug.

**New commands**:
- `copy <src> <dst>`: reads raw bytes internally - never through the numbered-`read` presentation layer - and writes them to `dst`. Requires `r:` on `src`, `w:` on `dst`.
- `move <src> <dst>`: `os.Rename`-based (with the same cross-device fallback copy+delete a plain rename would need). Requires `w:` on both `src` and `dst` - moving relinquishes `src`, so the relevant grant is write, not read.
- `delete <path>`: `os.Remove`. Requires `w:` on `path`.

Before this, `move` was impossible (no rename primitive and no delete to fake it with) and `delete` didn't exist at all - not inconvenient, genuinely unreachable.

**`read` behavior change**: numbering flips from default-on to default-**off**, opt-in via `-n`/`--numbers` (`cat -n` itself defaults to no numbering; `read` should match that convention, not invert it). Numbered format matches `cat -n` exactly - `%6d\t%s` (line number right-justified in a minimum 6-wide field, then a literal tab, then the raw line text), confirmed against real `cat -n` output including the padding behavior once line numbers exceed 6 digits (the field just grows, `%6d`'s normal behavior). Default (no `-n`) is the file's raw bytes - safe to pipe through the scratchpad into `write`/`copy`/anything else without silently mutating the content. `-n` exists for when an agent actually needs to reference specific lines - before constructing an `edit` or `patch` call - not as a general-purpose default; the help text should say so explicitly, since "numbering exists to help you write a correct patch hunk, not because it's part of the file" was exactly the piece of context clerk didn't have.

**`read` gets a hard size cap** (target ~200KB): if the requested read - whole file, or the given `--start`/`--end` range - would exceed it, refuse with a clear error suggesting line-based pagination (`--start`/`--end`) instead of silently truncating or dumping it anyway. `files-rw` shouldn't depend on the invoking harness happening to have its own large-output handling (D18's scratchpad auto-redirect is a `wackypub`/`run_command` feature, not something `files-rw` can assume - it's meant to be usable by any harness that can exec a tool with an argv and stdin).

**`patch` restricted to unified-diff format only**: validate the incoming diff actually looks like a unified diff (`---`/`+++` headers, `@@` hunk markers) before ever handing it to the system `patch` binary, rejecting anything else with a clear error - don't rely on `patch`'s own format auto-detection leniency (context diffs, normal diffs, ed scripts are all things GNU `patch` will happily also try to interpret). `patch`'s help text should say to read the target with `read -n` first (to get correct line numbers for the hunk header) and that only unified diffs are accepted. Unified diffs carry unchanged context lines that must match before a hunk applies, which is exactly the self-verifying property that makes them more resistant to drift/hallucination than an agent guessing at raw line-offset edits - and it's the format models are most commonly trained to produce anyway.

**Explicitly rejected**: a dedicated line-range replace command (`edit-lines <path> --start N --end M`, new content via stdin). Considered as a lower-ceremony alternative to constructing a one-hunk unified diff, but decided against - it reintroduces exactly the kind of unverified, drift-prone line-offset editing that restricting `patch` to unified-diff format is meant to avoid. `edit` (exact-string match) + `patch` (unified diff, self-verifying via context lines) stay the only two edit primitives. Matches how Claude Code's own file-editing toolset gets by on exact-string `Edit` + full-file `Write` alone, no line-range primitive.

**Why**: `read`+`write` alone can't safely stand in for `copy` when `read`'s own output isn't the raw file - that's a correctness bug, not a missing convenience, confirmed by an actual corrupted "copy." `move`/`delete` close a real capability gap (not just ergonomics: previously unreachable, not merely awkward). Restricting `patch` to unified diffs and requiring an explicit opt-in for `read` numbering both trade a small amount of flexibility for a lot less room for an agent to hallucinate its way into a corrupted or drifted file.

**Addendum: `FILES_RW_ACCESS` is now readable, still never writable.** D22's original "always denied, unconditionally" rule applied to both read and write. Found live (a swarm worker with no other file tools had no way to introspect its own grant except trial-and-error probing - real thrashing, several wasted rounds just rediscovering what was already granted): the actual risk `FILES_RW_ACCESS` denial exists to prevent is an agent *mutating* its own grant, not an agent *seeing* it - the contents aren't secret, and an agent can already infer them through probing anyway. `Access.Resolve` now special-cases the deny-path check on `needWrite`: `read` succeeds unconditionally (regardless of any `r:`/`w:` rule, same as the old always-denied special case just flipped for the read side), while `write`/`edit`/`patch`/`delete`/`move` (either side) stay denied exactly as before - confirmed live, writing/deleting/moving `FILES_RW_ACCESS` still fails with the same "always denied" error.

## D24: `files-rw`'s path-based access model has two real, unfixed gaps - hardlink read bypass (including cross-agent) and a TOCTOU race - accepted for now, not fixed in code

A second swarm run (report deleted per the invalidation rule once D26 changed the command surface again - `SECURITY_TESTING.md` has the current state; 4 of 10 requested rounds completed at the time, stopped early for cost, not because ideas ran out) against the D23 command surface found 7 confirmed bypasses, two of which are more serious than anything found in the first run and go beyond what D22's "if an agent already has `bash`, sandboxing that agent's own reach is moot" reasoning excuses:

1. **Hardlink read bypass generalizes past a single agent's own files.** `ln target/secret.txt ./scratch/hl && files-rw read ./scratch/hl` succeeds, same root cause the first run found (D23's `WriteFile` doc comment already covers the write-side version of this). New this run: the same technique reads **any** same-filesystem file, including **other agents' files across `/ws/`** (`ln /ws/worker-sonnet/target/secret.txt ./scratch/hl` from a *different* worker's own directory - confirmed live). That's not "an agent can read its own out-of-bounds files given shell access" (D22's accepted-risk framing) - it's one agent reading a *different* agent's private files, a materially different concern that has nothing to do with the reading agent's own privilege level.
2. **TOCTOU race between `Access.Resolve()` and the actual I/O, no hardlink required at all.** `Access.Resolve()` returns a path *string*; the real filesystem operation happens afterward, as a separate step, against that same string, with nothing holding a file descriptor open across the gap. A background process racing to swap the file's content between those two steps wins 99-100% of the time in testing (confirmed live against both `read` and, separately, `copy`). This needs no `ln`, no hardlink capability, nothing beyond ordinary write access to a directory the racing process already has - it's a pure timing bug in the check-then-use pattern, present by inspection in every command (`read`/`copy`/`edit`/`patch`/`move`/`delete` all resolve a path once, then act on that string later).

**Root cause for both, ultimately the same one**: path-based access control validates a path string, then either (a) never re-validates which inode that path actually names by the time the syscall runs (hardlink case), or (b) doesn't hold the checked file open at all, so the path can point somewhere else entirely by the time it's used (TOCTOU case). The real fix for both is the same shape: open the file (`O_NOFOLLOW`) as part of, or immediately after, the access check, and do all subsequent I/O through that already-open file descriptor rather than re-resolving the path string - collapses the TOCTOU window to nothing and, as a side effect, makes the hardlink case checkable too (validate the open fd's identity, not just the path that produced it).

**Decision: not fixed in this pass.** Scoped as a TODO (`.agents/TODOS.md`) instead of implemented now - an fd-based rewrite of `Access.Resolve` and every call site touches the core of `pkg/filesrw` broadly enough to warrant its own dedicated pass and a fresh swarm re-test afterward, not a rushed fix bolted onto an already-large round of findings. `SECURITY_TESTING.md`'s `files-rw` entry is `n`, not `?` - this is a confirmed, real finding staying on record, not an invalidated-by-code-change reset.

**Why accept this now instead of fixing immediately**: same reasoning as D22's original hardlink note, extended - `files-rw`'s threat model has always assumed the tool itself is the only file-touching capability an agent has (see D22's "Why"). Every worker in this swarm run also had raw `bash`, which was necessary for building attack fixtures (creating hardlinks, running race loops) but means the *specific* deployment tested here isn't `files-rw`'s intended one either. That doesn't make the finding not real - the TOCTOU race and cross-agent hardlink read are structural gaps in `files-rw` itself, not gaps that require `bash` to exploit once a hardlink can be planted by any means - but it does mean shipping a rushed fix under time pressure is worse than documenting the gap precisely and fixing it properly in a dedicated pass.

## D25: `search_scratchpad` - a fifth built-in scratchpad tool, search as an index into pagination, not a replacement for it

Workshopped, not yet implemented. A large scratchpad entry (a big file dump, a long command output) is currently only navigable by paginating blind through `get_scratchpad`'s `skip_lines`/`num_lines` - fine once you know roughly where the interesting part is, tedious to locate it in the first place. `search_scratchpad` closes that gap as a fourth-ish built-in alongside `create_scratchpad`/`get_scratchpad`/`list_scratchpads`.

**Signature**: `search_scratchpad(id, query, case_sensitive=true, regex=false, max_results=50)`.
- `id` required - scoped to one entry, not a search-everything-live mode. A "search across all live entries" tool would be a different feature (more like "which entry has X") - not ruled out for later, just not conflated with this one, which matches the concrete use case (an agent already holding a big entry, looking for where in it something is).
- `query` is a literal substring by default; `regex` is an opt-in escape hatch, off by default. Mirrors the pattern already used twice in `files-rw` (`read -n` opt-in numbering, `patch` unified-diff-only default): the safe/predictable primitive is the default, power is available but never assumed.
- `case_sensitive` defaults `true`, matching real `grep`'s own default rather than a more "forgiving" case-insensitive-by-default some tools use - avoids surprising a model that already has `grep` conventions baked into its training.
- `max_results` hard-caps the returned list (default ~50), same reasoning as `files-rw read`'s 200KB cap: a pathological query (searching for `"the"` in a large log) shouldn't be able to blow up the response. Total match count is reported separately from the capped list, same shape as `list_scratchpads`' count/cap fields, so the agent knows whether it's seeing everything.

**Result shape, per match**: `{line, skip_lines, text}` - `line` is 1-indexed and human-readable, `skip_lines` is precomputed (`line - 1`) so the agent can hand it straight to `get_scratchpad` or the `<SCRATCHPAD_DATA skip_lines="N" />` macro without doing its own off-by-one arithmetic, and `text` is the single matching line, truncated (~200 chars) so one absurdly long line can't dominate the result.

**Explicitly rejected**: including N lines of surrounding context per match (`grep -C`-style). Search stays a lightweight index - *where* the matches are - and `get_scratchpad` stays the one place that actually extracts content. Two tools each doing one thing kept simpler than one tool that both finds and previews, and avoids two different, only-loosely-consistent pagination code paths.

**Why**: same motivation as D18's original scratchpad design (D18: scratchpad exists because generation is the expensive part of a token budget, not consumption) - search is another way to keep an agent from having to re-read or re-generate content it doesn't need, this time by narrowing *where* to look before it pays for a `get_scratchpad` call at all.

## D26: `files-rw` D24 fix, revised before a swarm re-test - `go-gitdiff` replaces the `patch` subprocess, O(1) `Nlink>1` replaces the O(N) directory-walk hardlink check

Reviewed the first-pass D24 fix before spending a swarm run verifying it, and found two problems in its own coverage:

1. **`patch` still has the TOCTOU gap it was meant to close.** `PatchFile` opens the target via `Access.OpenFile` (the new fd-based check), then immediately closes that fd and does the real work via the `patch` subprocess against the path string. Everything gained by holding the fd open is lost the moment it's closed before the actual syscall - the window this fix exists to close is still open, just for `patch` specifically.
2. **The hardlink check itself is a new performance/DoS surface.** `checkHardlinkSafety`'s `countInodesInRoots` does a full recursive `filepath.Walk` over every allowed root, on *every single access check* - not once at load time. A directory tree of any real size under an allowed root turns every `read`/`write`/`edit`/etc. call into an O(number of files under the roots) operation, which didn't exist before this fix and wasn't itself tested.

**Decision, both fixed in the same revision**:

- **`patch` moves off the system binary entirely, onto `github.com/bluekeyes/go-gitdiff`** (`gitdiff.Parse` + `gitdiff.Apply`), following the exact shape `EditFile` already uses: open via `Access.OpenFile`, read the original content through that fd, apply the parsed diff in memory, hand the result to `WriteFile`'s existing atomic-rename path. This closes `patch`'s TOCTOU gap the same way `read`/`copy`/`edit` are closed, and as a side benefit replaces the old heuristic `isUnifiedDiff` string-sniffing (checks for `---`/`+++`/`@@` substrings) with a real parser that rejects malformed diffs properly. Also drops the dependency on the system `patch` binary entirely - the `Dockerfile`'s `patch` apt-get addition (added for the original D24 swarm test, so `files-rw`'s patch-subcommand attack surface wouldn't go untested for lack of the binary) becomes unnecessary and should be reverted alongside this change.
- **The hardlink check drops the directory walk in favor of a flat `Nlink > 1` rejection** - O(1), a single field off an already-`stat`ed file, no traversal. Every attack the second swarm run actually demonstrated (single-agent hardlink read, hardlink+copy, cross-agent hardlink read, delete-via-hardlink) involved creating an *extra* hardlink, which always bumps `Nlink` from 1 to 2+ - so the cheap version closes every demonstrated attack. Acknowledged cost: it also refuses to touch a legitimate file that happens to have more than one hardlink for unrelated reasons, which is expected to be rare for typical agent-workspace files but is a real, accepted false-positive surface, not a hypothetical one. `countInodesInRoots`/`getNlinkAndDevIno`'s walk-based inode-matching machinery is removed, not kept as a fallback - maintaining two hardlink-detection strategies side by side wasn't judged worth the complexity over just picking the cheap one.

**Deferred, not abandoned**: a real, non-blunt hardlink defense (one that doesn't also block legitimate multi-linked files) remains an open question, alongside the option of pushing more of this mitigation to deployment/environment hardening instead of application code - e.g. actually enforcing `fs.protected_hardlinks` (confirmed disabled/unenforced in the swarm test's container, D24) or simply not co-locating mutually-distrusting agents on a shared writable filesystem in the first place, rather than trying to detect the aliasing after the fact from inside `files-rw` itself.

**Why**: same reasoning as D24's own "why accept this now" - a security fix that isn't actually verified to close what it claims to close, or that trades one real gap for a new unverified one (the walk-based DoS surface), isn't worth spending a swarm run confirming. Catching this in review, before the swarm run, is cheaper than a swarm run rediscovering the exact same category of incomplete fix.

## D27: `wackypub agent <id> scratchpad {create,read,list,search}` - CLI-level scratchpad access

Workshopped, not yet implemented. Closes an already-logged gap (the former "Future Scratchpad management" TODO): scratchpad slots have only ever been reachable from inside a live agent turn via the built-in tools (`create_scratchpad`/`get_scratchpad`/`list_scratchpads`/`search_scratchpad`, D18/D25) - no way for a human operator, external tooling, or another agent driving `wackypub` from the CLI to read or write one directly.

**Surface** (mirrors the four in-agent tools 1:1):
```
wackypub agent <id> scratchpad create [message]     # positional/--message flag/stdin, same 3-way pattern as `agent add`
wackypub agent <id> scratchpad read <entry-id> [--skip-lines N] [--num-lines M]
wackypub agent <id> scratchpad list
wackypub agent <id> scratchpad search <entry-id> <query> [--regex] [--case-insensitive] [--max-results N]
```
Same `ValidateAgentTarget` authorization already applied to every other `agent <id> <verb>` command - no new authorization scheme, just consistent reuse of the existing one.

**Locking**: `create` acquires the session lock (`AcquireSessionLock`) - it's a read-modify-write over the whole `scratchpad.json`, and two concurrent CLI *processes* (not goroutines - the existing in-process `getScratchpadMutex` doesn't help across process boundaries) racing to create an entry could lose one. `read`/`list`/`search` are pure reads against a file that's only ever atomically replaced (temp file + rename, D18), so per the same precedent that already dropped locking from read-only `AgentSDK` methods (self-deadlock fix, see TODOS.md), they don't acquire it.

**What this replaces, and why nothing else needed building**:
- The original idea was three separate features: (1) auto-expand `<SCRATCHPAD_DATA />` macros in an agent's *final response text* (not just tool-call args) so a caller gets the full content without the agent regenerating it; (2) auto-redirect a large *incoming* user message into scratchpad, mirroring the existing large-tool-*output* auto-capture (`ScratchpadOutputThreshold`); (3) this CLI exposure.
- (1) turns out to need no new machinery: an agent can already put a raw `<SCRATCHPAD_DATA id="X" />` reference in its plain response text today (nothing expands it, but nothing breaks either) - once `scratchpad read` exists, the caller just pulls that specific entry on demand instead of every macro in every response getting force-expanded into stdout whether wanted or not. Caller-decides is strictly better than always-expand here.
- (2) was explicitly rejected in favor of explicit-only: the caller stashes large content itself (`wackypub agent bob scratchpad create < bigfile.txt` returns an ID, then `wackypub agent bob prompt "summarize scratchpad <id>"`) rather than `add`/`prompt` silently redirecting a message above some threshold. Matches the project's standing preference for explicit tools over implicit magic (D-numerous precedent, most recently D23/D26's own "predictable primitive by default, no silent guessing") - no threshold to pick, explain, or have surprise a caller who didn't expect their message to be intercepted.

**Explicitly deferred, not part of this decision**: a `scratchpad delete <entry-id>` CLI command (and matching in-agent tool - neither currently exists; entries only leave via automatic lowest-`seq` eviction past the 50-entry cap). Worth its own decision if a real need shows up, not bundled in here just because it's adjacent.

**Why**: enables direct scratchpad-to-scratchpad piping between agents and CLI-level file-into-scratchpad piping for a human operator, both previously impossible without going through a live agent turn - and does it by exposing the same four operations that already exist in-session, not inventing a new mechanism.

## D28: `CreateScratchpad` server-side macro expansion - out-of-band scratchpad concatenation and templating

Implemented in `CreateScratchpad` (`pkg/agent/scratchpad.go`). Automatically expands inline `<SCRATCHPAD_DATA id="X" skip_lines="N" num_lines="M" />` macros server-side before storing a new scratchpad entry payload in `scratchpad.json`.

**Mechanics**:
- `CreateScratchpad` calls `ExpandScratchpadMacros(agentDir, text)` *before* acquiring the per-agent directory mutex (`getScratchpadMutex`), resolving any referenced scratchpad entries (or slices) from disk and substituting their text into the payload before saving under a new 4-character ID.
- Applies uniformly across all creation paths: the ADK `create_scratchpad` tool, the CLI `wackypub agent <id> scratchpad create` subcommand, and `AgentSDK.CreateScratchpad`.
- Thread-safe and deadlock-free: macro resolution reads referenced entries via `GetScratchpad` (which acquires and releases the mutex per read) *before* `CreateScratchpad` locks the mutex for the main write, avoiding recursive/re-entrant mutex lock deadlocks.

**Use Cases**:
- **Out-of-Band Concatenation & Templating**: An agent or CLI script can stitch together multiple scratchpad entries (e.g. `text: "Header:\n<SCRATCHPAD_DATA id=\"hdr1\" />\nBody:\n<SCRATCHPAD_DATA id=\"dat2\" />"`) into a single new scratchpad entry in one tool call, without reading or outputting their text payloads into LLM context turns.
- **Token Efficiency**: Allows agents and multi-agent swarms to combine arbitrary-sized datasets out-of-band with zero LLM generation tokens spent on payload contents.

**Why**: `<SCRATCHPAD_DATA />` macros were originally expanded only inside `run_command` (tool `args` and `stdin`). Extending server-side macro expansion to `CreateScratchpad` brings the same zero-token macro capability to scratchpad creation itself, enabling out-of-band data combination without inventing a separate "combine_scratchpads" tool or forcing data through the model's context window.

## D29: `files-rw` gets `tail` and `append`; "replace the last line" is a composition, not a new primitive

Implemented in `pkg/filesrw/ops.go` and `cmd/files-rw/main.go`. Motivating case: an agent maintaining a large append-only ledger needs to (1) see what's at the end of the file, which currently requires already knowing the total line count to compute a `read --start`/`--end` range, (2) append new entries without reading/rewriting the whole file, which the current `read`'s 200KB cap makes outright impossible for a large ledger via existing primitives, and (3) occasionally replace the last line.

**New: `tail <path> [-n N] [--numbers]`.** Native Go implementation (not a subprocess - D26's lesson: a subprocess is exactly where a TOCTOU window reopens), reads through the same `Access.OpenFile`-protected fd `read`/`edit` already use. Returns the last N lines plus a `total_lines` count reported separately from the (possibly capped) returned lines - same shape `search_scratchpad`'s `total_matches`/capped-`matches` split already established (D25). Solves both "see the end" and "know the total line count" in one call - an agent can use the returned `total_lines` to compute a `read --start`/`--end` range itself afterward if it wants a different window.

**New: `append <path>`.** Content via stdin, same convention as `write`. Genuine `O_APPEND` write to the already-open, already-access-checked fd - not read-the-whole-file-then-atomically-rewrite, which would just be `read`+`write` composed manually and still hit `read`'s 200KB cap for a large ledger, defeating the entire point. This is safe to do post-D26 specifically because D26 moved hardlink defense into the access check itself (`Nlink > 1` rejected before any I/O happens, via `Access.OpenFile`) rather than relying on `write`'s atomic-rename as an incidental side effect (the original D22/D23 mechanism) - so `append`'s in-place write doesn't lose any hardlink protection `write` has. **Honest tradeoff, stated plainly**: `append` does lose `write`'s full-swap crash safety - a crash mid-append can leave a truncated trailing write on disk, where `write` never leaves a half-written file (temp file discarded, original untouched). Worth documenting in `append`'s own `--help`, not just here.

**Explicitly rejected: a dedicated "replace the last line" command.** Resolved by composition instead, using `tail` plus the two edit primitives that already exist:
- If the last line's content is unique in the file: `tail -n 2` to see it, then `edit --old "<second-to-last>\n<last line>" --new "..."` (widening the match to a two-line window is usually enough even when the last line *alone* isn't unique - an empty line or a repeated value is unlikely to repeat as part of the exact same *pair*).
- If even that's not unique: `tail -n N` to learn `total_lines` and the current tail content, then a one-hunk unified diff via `patch` targeting `@@ -total_lines-1,2 +total_lines-1,2 @@` - `patch` targets by *position*, verified against *local* context, and never requires the target line's content to be unique globally the way `edit` does. This is the case `edit` structurally can't cover (a genuinely repeated/empty last line), and it's exactly what `patch`'s design is already good at.

A dedicated primitive would reopen the same line-offset-editing risk D23 already argued against rejecting for the general case - "the last line" is a narrower, lower-risk target than an arbitrary `N..M` range (no counting arithmetic against a file the agent hasn't fully read), but narrower isn't risk-free, and composing existing primitives fully covers both the common case (unique tail) and the edge case (non-unique tail) without adding one.

**Why**: `tail` and `append` close a real capability gap the same way `copy`/`move`/`delete` did in D23 - a ledger-style workload is currently unreachable through `files-rw`'s existing primitives, not just awkward. "Replace the last line" turned out not to need a new primitive at all once `patch`'s position-plus-context-verification model (rather than `edit`'s uniqueness model) was actually walked through - composing what already exists both avoids the D23 risk and confirms the existing primitive set is more complete than it first looked.

## D30: Scratchpad storage moves from a single `scratchpad.json` blob to one file per entry

Implemented in `pkg/agent/scratchpad.go`, `pkg/agent/sdk.go`, `pkg/agent/agent_folder.go`, and `cmd/agent.go`. `pkg/agent/scratchpad.go`'s current design (`ReadScratchpadStore`/`WriteScratchpadStore`) reads and JSON-parses *all* live entries' full `Text` on every single operation - `get_scratchpad`, `list_scratchpads`, and `search_scratchpad` each pay the cost of every other entry's content just to touch one, and `create_scratchpad` does a full read-modify-write of the same blob. At a 50-entry cap this is wasteful; it doesn't get better by raising the cap under the current design, it gets worse.

**New storage**: `<agentDir>/scratchpad/<id>-<createdBy>.txt`, one file per entry, content is the raw stored text with no envelope. `id` keeps the existing 4-character `[0-9a-z]` random format; `createdBy` is the short, closed-set tag already in use (`create_scratchpad`, `run_command`, `cli`) - safe to put directly in the filename, no encoding needed. `ScratchpadFileName`/`ScratchpadStore`/`ReadScratchpadStore`/`WriteScratchpadStore` are removed entirely.

**Fields dropped, not just hidden**: `Seq` and `CreatedAt` are removed from `ScratchpadEntry`/`ScratchpadItem` and from every tool result and CLI output that currently surfaces them (`CreateScratchpadResult.Seq`, `cmd/agent.go`'s `scratchpad create` print statement, `list_scratchpads`' entries). Ordering is now conveyed purely by position: `list_scratchpads`/`scratchpad list` sort entries by file mtime ascending (oldest first) and return them in that order, with no explicit sequence number and no timestamp in the output. Real-world wall-clock time is deliberately never exposed to the model through this path - mtime is used only as an internal sort key, never rendered. (The file's mtime still exists as normal filesystem metadata and isn't otherwise hidden from the OS - this is about not routinely handing it to the model in scratchpad output, not a hard confidentiality boundary.)

**ID uniqueness and creation**: generate a random ID, attempt `os.OpenFile(path, O_CREATE|O_EXCL|O_WRONLY, ...)`, retry on collision. This is atomic and correct across separate OS processes for free - unlike the uniqueness check it replaces (scanning the in-memory `liveEntries` map from a just-read blob).

**Get/search by ID**: entries are looked up with a `filepath.Glob(id + "-*.txt")` (one directory scan, filenames only, no content read) since the caller only has the ID, not the `createdBy` suffix.

**Eviction/cap**: raised from 50 to 300, now cheap to raise because per-entry cost stopped scaling with total blob size. On `create`, if live-entry count exceeds the cap, evict the file with the oldest mtime - a `ReadDir` + stat pass over filenames, never file content.

**Locking removed entirely**: `getScratchpadMutex`/`scratchpadMutexes`/`globalScratchpadMu` (`sync.Map` of in-process `*sync.Mutex`, D27's own review already flagged these as giving zero protection across separate CLI processes) are deleted, not replaced. Entries are write-once and never mutated after creation (no `UpdateScratchpad` exists), so reads need no lock; creates are collision-safe via `O_CREATE|O_EXCL`; the only mutating op left, eviction's `os.Remove`, is fine to race - a reader that loses the race just sees "not found," a legitimate outcome for an entry that just aged out. This is a strict correctness improvement, not just a simplification: D27 gave `scratchpad create` the session lock specifically to work around the old in-memory mutex's cross-process blindness, and that workaround is no longer needed either.

**Migration**: none. Existing `scratchpad.json` files are simply never read again post-upgrade (orphaned, harmless, not auto-deleted) - scratchpads are ephemeral working memory, not history worth preserving across this change, unlike `session.jsonl`/`MEMORY.md`.

**Why**: fixes a real, measured resource-waste problem (full-blob read/parse/rewrite on every operation, including read-only ones) and incidentally closes a real cross-process locking gap that was only ever papered over, not fixed, by D27's session-lock workaround. Found while reviewing D29 and evaluating whether `files-rw` had an equivalent problem (it doesn't, per-file already) - prompted checking whether the scratchpad system had the same shape of issue, and it did, worse.

