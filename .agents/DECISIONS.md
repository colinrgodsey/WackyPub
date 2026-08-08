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

**Not implemented yet - design only, see TODOS.md.** Once agents can invoke
`wackypub` itself as a tool (e.g. to message another agent), two failure
modes need independent guards: an agent reaching an agent nobody authorized
it to reach, and an agent reaching (directly or transitively) back to itself
mid-generation.

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

## D17: Tool execution loop and self-describing tool protocol

`<agent_dir>/tools/` executables discovered per D14 are registered as LLM function declarations on each generation request.

**Tool Schema Registration**:
- Each executable discovered in `tools/` is registered as a `genai.FunctionDeclaration` with `Name` matching the executable's basename.
- `Description` defaults to `"Command <Name>"`.
- `Parameters` defines a standard schema accepting:
  - `"args"`: an array of string CLI command line arguments passed positionally to the tool.
  - `"env"`: an object map of key-value environment variables set for the tool invocation.

**Tool Execution Protocol**:
- When the model returns a `FunctionCall` part for a tool `X`:
  - `wackypub` executes `<agent_dir>/tools/X` (or its discovered path) with the positional `args` list.
  - Any key-value entries in `env` are added to the subprocess environment (along with inheriting process environment including `WACKYPUB_CALL_CHAIN`).
  - Full raw JSON arguments are also passed to the tool's stdin and as `WACKYPUB_TOOL_ARGS`.
  - `wackypub` captures stdout (and stderr on failure) from the subprocess.
  - A `FunctionResponse` part is constructed with the response output and appended to the session log.

**Loop Termination & Max Tool Turns**:
- `GenerateTurn` executes in a loop: after receiving tool calls and appending tool responses, it invokes the model again until the model returns a text response (no function calls) or the maximum tool turns limit is reached.
- Default max tool turns is 10 per `GenerateTurn` invocation, configurable via the persistent `--max-tool-turns` CLI flag.

**Why**: A uniform `{args: []string, env: map[string]string}` schema allows arbitrary CLI tool binaries under `tools/` to be invoked natively as subprocesses without requiring every tool to author custom schema metadata parser extensions, keeping discovery fast and compatible with any shell command.

## D18: Persistent Scratchpad tools and command I/O redirection

Agents can store large text payloads or intermediate command outputs in a persistent session-level scratchpad saved as `<agent_dir>/scratchpad.json`.

**Built-in Scratchpad Tools**:
- `set_scratchpad(id: int, text: string)`: stores `text` under key `id` in `<agent_dir>/scratchpad.json`.
- `get_scratchpad(id: int)`: retrieves the string stored under key `id`.

**Command I/O Redirection Parameters**:
Executable tool declarations in `<agent_dir>/tools/` include optional integer properties:
- `stdin_scratchpad_id`: if set, `executeTool` reads `scratchpad[stdin_scratchpad_id]` and pipes it to the subprocess `Stdin`.
- `stdout_scratchpad_id`: if set, `executeTool` writes the subprocess `Stdout` output into `scratchpad[stdout_scratchpad_id]` and returns a summary message (`"Scratchpad <id> updated (N bytes)"`) instead of placing large output directly into the conversation turn.

**Why**: Passing large command outputs directly through session history inflates model context budgets and token costs. A persistent session-scoped scratchpad allows tools to pipe large payloads to one another or store outputs by integer ID without polluting prompt history.




