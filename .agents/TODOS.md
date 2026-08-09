# TODOS.md

Deferred work and known gaps. Not a backlog of feature ideas - only things
that are already known to be incomplete, fragile, or blocked on something
external.

## Handle OpenRouter / OpenAI Rate Limiting & Empty Choices Error Recovery

When calling OpenAI-compatible APIs (specifically OpenRouter), rate-limiting (429), capacity limits, upstream timeouts, or moderation flags can result in HTTP 200 OK responses returning an empty choices array (`"choices": []`). 

The official `openai-go/v3` SDK (used by `adk-utils-go`'s `genai/openai` adapter) parses `resp.Choices` as a 0-length slice, causing `convertResponse` to return a bare `ErrNoChoicesInResponse` ("no choices in OpenAI response") without surfacing the underlying root cause.

**Key Triggers & Gaps**:
1. **Rate Limiting & 429s**: When OpenRouter or an upstream provider rate-limits a request, OpenRouter sometimes wraps rate-limit errors inside an HTTP 200 JSON envelope carrying `"choices": []` alongside an embedded `"error"` object (e.g. `{"choices": [], "error": {"code": 429, "message": "Rate limit reached"}}`).
2. **Upstream Provider Errors**: Upstream nodes (Novita, Together, Fireworks, DeepInfra) hitting gateway timeouts or capacity limits return `"choices": []` with embedded error payloads.
3. **Masked Error Strings**: Because `openai-go` only inspects `resp.Choices`, embedded `"error"` objects on HTTP 200 responses are discarded by `convertResponse`, masking actionable error messages.

**Required Work**:
- In `adk-utils-go`'s `convertResponse`, inspect `resp.RawJSON()` for top-level `"error"` fields when `len(resp.Choices) == 0` and surface the embedded error message (e.g. `fmt.Errorf("no choices in OpenAI response: %s", rawErrMessage)`).
- Implement retry / backoff logic in the caller or model adapter for transient rate-limits (429) and upstream provider timeouts when `choices: []` is received.

## Consider a timeout on session lock acquisition

`AcquireSessionLock` (`pkg/agent/lock.go`) blocks forever on `syscall.Flock`
with no timeout. The read-only SDK methods that used to call it
unnecessarily were fixed to not lock at all (see the self-deadlock fix
found live via clerk), but the genuinely mutating methods (`AddUserTurn`,
`GenerateTurn`, `AddAndGenerateTurn`, `StripSignatures`,
`CompactSession`) still correctly need to hold it, and still block
indefinitely if something else is holding it - a real deadlock we haven't
found yet, or just a second legitimate caller genuinely waiting its turn.

A bounded wait (fail with a clear "timed out waiting for session lock -
another process appears to be using this agent" error instead of hanging)
would be a reasonable safety net. The real constraint: this session
directly observed legitimate `GenerateTurn` calls taking several minutes
(high reasoning effort + several chained tool turns), so the timeout needs
to be generous enough not to false-positive-fail a slow-but-working
generation - probably needs to be configurable rather than a small fixed
constant. `syscall.Flock` has no native blocking-with-timeout mode, so
this would mean polling `LOCK_NB` in a loop until success or timeout
elapses.

## Modular compaction strategy

`CheckAndCompactSession`/`CompactionDirectivePrompt` (`pkg/agent/compaction.go`)
is a single hardcoded approach: an LLM directive prompt that summarizes
archived turns into a `<PERSISTENT_MEMORY>` addendum. There's no seam for
a different strategy (e.g. plain truncation with no summarization,
externally pluggable compaction, per-agent-configurable directives beyond
the AGENTS.md `## Memory Focus` override already supported). Worth
factoring compaction behind some kind of strategy interface/config knob
instead of the one fixed implementation, once a second real use case for
a different strategy actually shows up.

## Real unit test coverage for compaction prefix preservation

`TestCompactionPrefixPreservation` (`pkg/agent/compaction_test.go`) is
misleadingly named - it only checks that `MEMORY.md` still exists after a
compaction call that fails against a fake HTTP endpoint. It does not
actually verify that compaction preserves the request prefix: the same
system prompt, the same tool declarations, and the same initial portion
of turns before and after compaction runs, with only the archived middle
replaced by the memory addendum. A real test would need to capture the
outgoing wire payload (httptest, same pattern as `openai_model_test.go`'s
reasoning-egress tests) before and after a compaction cycle and diff the
surviving prefix.

## How compaction should treat loaded skills

Once `load_skill` returns a skill's body, it's a normal tool-response
turn like any other - fully subject to the same compaction/archival
boundary logic as everything else (D8-ish territory). Most agent harnesses
hit this same problem: is a loaded skill's full text worth preserving
verbatim across compaction (bloats `<PERSISTENT_MEMORY>`), or is it enough
for the memory addendum to note "skill X was loaded" so the agent can
`load_skill` it again if it's actually still needed (cheap, matches how
compaction already treats everything else as re-derivable)? No decision
made yet - needs to happen before/alongside the skills system's first
implementation, not as an afterthought once agents start actually
accumulating loaded-skill turns that get archived.

## `load_skill_extra` for skill reference files

Real-world skill folders often ship extra reference files alongside
`SKILL.md`, referenced from the skill body via relative paths (images,
longer reference docs, example data). Eventually want a companion tool -
`load_skill_extra(skill_name, relative_path)` - so an agent can pull one
of those in on demand rather than the skills system only ever exposing
`SKILL.md`'s own body.

## No total budget on cross-agent call depth, only cycle prevention

`WACKYPUB_CALL_CHAIN` (D16) stops an agent from being re-entered mid-chain
(A -> B -> A), but nothing caps how long or expensive a legitimate, acyclic
chain can get. Live testing showed an agent can chain multiple cross-agent
calls to a peer agent entirely on its own within a single generation turn
(see D16/D17); nothing currently stops that same pattern from fanning out
across several agents on one bad turn. Worth
adding a max-depth counter alongside the existing cycle check - likely
threaded through `WACKYPUB_CALL_CHAIN` the same way `--max-tool-turns`
caps a single agent's own tool loop.

## No automated test coverage for live cross-agent tool invocation

Discovery (symlinked toolpacks), single-agent tool execution, cross-agent
`wackypub agent <id> prompt` invocation via `WACKYPUB_ALLOWED_AGENTS`, and
model-driven multi-hop chaining (one agent calling another twice in a row
based on the first response) have all been verified live against real
LLM backends, but only manually - there's no committed test that exercises
a bob-style agent invoking a peer agent as a subprocess. A synthetic test
using `httptest` (mocking the model backend the way `openai_model_test.go`
already does) that drives a two-agent chain end-to-end would catch a
regression here without requiring a live LLM call.

## Dedicated `wackypub init` command

Bootstrapping a new workspace's `WACKYPUB_ROOT` file currently requires creating `WACKYPUB_ROOT` by hand (`touch WACKYPUB_ROOT`). A dedicated `wackypub init` command (to create `WACKYPUB_ROOT` and scaffold an agent directory) may be worth adding later.

## Future Scratchpad management (`wackypub scratchpad` CLI commands)

Scratchpad slots are managed in-session via built-in agent tools (`create_scratchpad`/`get_scratchpad`/`list_scratchpads`) and `run_command` I/O redirection (see DECISIONS.md D18). `list_scratchpads` covers in-session forensic inspection, but there's still no CLI-level equivalent (`wackypub agent <id> scratchpad list/read/clear`) for human operators or external tooling to inspect and manage persistent scratchpad slots from outside a live agent turn.

## Open question: does `WACKYPUB_ALLOWED_AGENTS` restrict CWD-based invocations in general, or only actual tool-call context?

Flagged during the D16 design discussion, deliberately deferred. If
`WACKYPUB_ALLOWED_AGENTS` is checked purely based on "does CWD fall under
this agent's folder," then a human who `cd`s into agent A's folder and
manually runs `wackypub agent B ...` to debug something also gets
restricted by A's allowlist - even though there's no actual deadlock or
authorization risk in that case, since a human isn't "agent A calling B."
The alternative is scoping the check to only apply when the invocation is
actually happening as a tool call spawned from A's own live generation
(e.g. via an explicit signal set only during a real tool-use loop, such as
`WACKYPUB_CALL_CHAIN` already being non-empty), which would leave manual/
debugging use from inside an agent's folder unrestricted. Needs a decision
if this behavior should be refined; currently implemented with the simpler CWD-only check (see DECISIONS.md D16).

## Drop the `adk-utils-go` fork once upstream catches up

`go.mod` currently has:

```
replace github.com/achetronic/adk-utils-go => github.com/colinrgodsey/adk-utils-go v0.0.0-...
```

pinned to a specific commit pseudo-version on `github.com/colinrgodsey/adk-utils-go`'s
`master` (see DECISIONS.md D3). There's no automation re-pinning this - it's
manually bumped (`go mod edit -replace ... @master && go mod tidy`) whenever
the fork changes. Once the reasoning-egress fix lands in
`achetronic/adk-utils-go` upstream and is tagged, drop the `replace`
directive entirely and depend on a real tagged version.

## `RunWithRunner` / `BuildADKAgent` path is unused

`FolderAgent.RunWithRunner` (`pkg/agent/agent_folder.go`) and
`BuildADKAgent`/`llmagent.New` (`pkg/agent/adk_agent.go`) construct and use
ADK's actual `LLMAgent`/`Runner` pipeline, but no CLI command or SDK method
calls `RunWithRunner`. Either find a use for it (multi-agent delegation,
tool use via ADK's flow, etc.) or remove it - right now it's dead code that
still has to be kept in sync with ADK API changes (it was part of the v1 ->
v2 migration effort) for no exercised benefit.

## `session.jsonl` has no defense against the missing-trailing-newline corruption mode

Documented in `.agents/AGENTS.md`'s Gotchas section and hit for real during
manual testing: if the file's last line lacks a trailing newline (e.g. from
an external edit) and `AppendSessionContent` appends to it, the two JSON
objects land on one line and get silently skipped by `ReadSessionTurns` on
the next read - no error, just a quiet gap in history. `AppendSessionContent`
could check the file's last byte and insert a newline first if needed.
Not fixed yet; current mitigation is "don't hand-edit `session.jsonl`
without checking it still parses afterward."

## `EstimateTokens`'s reasoning-details block cost is not counted

Noted upstream (in the adk-utils-go fork's own gaps list) and true here too:
a `reasoning_details` block kept in `partMetadata` costs real prompt tokens
on replay - an encrypted blob is not small - but `EstimateTokens` only walks
part `Text`, so a session carrying replayed encrypted reasoning has its
token estimate undercounted relative to what's actually sent on the wire.
Only matters for agents with `supportsReasoningDetails: true` (which also
requires a pinned model - see DECISIONS.md D6), so the exposure is narrow,
but the compaction trigger could fire later than it should for those agents.

## HTTP client timeout is far too short for real agent/swarm workloads

`NewOpenAIModel` and `NewAnthropicModel` (`pkg/agent/openai_model.go`,
`pkg/agent/anthropic_model.go`) both hardcode `&http.Client{Timeout: 120 *
time.Second}`. Confirmed live during the first `files-rw` swarm pen-test:
an OpenRouter-hosted worker (`google/gemma-4-26b-a4b-it` at
`reasoning.effort: high`) hit `context deadline exceeded` on its very first
call and again on retry - a slow/loaded backend model legitimately can take
longer than 120s to produce a first token, especially at high reasoning
effort, and this is only going to get more common as agents chain longer
tool loops and get orchestrated into swarms.

Reported directly from local experience running swarms against a
self-hosted llama-server: 15 minutes of no response is the point actually
worth calling a timeout, and even that isn't always enough given a long
enough toolchain - leaning towards no timeout at all by default (`0`, Go's
`http.Client` zero value for "no timeout") rather than picking a longer but
still-arbitrary fixed number. Whatever the default ends up being, it should
almost certainly be a `runtime.json` knob (a generic timeout field, or
per-provider) rather than hardcoded, since "how long is too long" depends
entirely on the backend and workload, not the provider adapter. Native
Gemini (`CreateGeminiModel`, `pkg/agent/adk_agent.go`) doesn't set an
explicit client override at all - worth checking what the underlying
`google.golang.org/adk/v2/model/gemini` package defaults to before assuming
it's unaffected.

## No way to cancel an in-flight agent task

Every other agent harness worth comparing against gives you some way to
interrupt a run that's taking too long or has gone off the rails (Ctrl-C
mid-generation, a cancel button, a kill command) - `wackypub` has nothing
like this. Once `wackypub agent prompt`/`generate` is running, the only way
to stop it is killing the process outright, which (depending on exactly
where it lands) can leave the session lock held or a turn half-appended.
Separate from the timeout issue above (a good cancellation story doesn't
remove the need for a sane default timeout, and vice versa), but related:
once agents are routinely being orchestrated into swarms/multi-agent
chains, a stuck or runaway one needs to be stoppable without taking down
the whole process tree around it. No design started yet.

## Positioning idea: "it's like bash, for agents"

Not code work - a framing/messaging angle for README/philosophy sections
that isn't written up anywhere yet. The parallel is closer than it first
sounds, point for point:

- **Process per command, no daemon.** Every `bash` command is its own
  process; state survives via the filesystem, not a long-lived
  interpreter holding memory. Every `wackypub agent <cmd>` invocation is
  the same - a fresh process per call, state persisted to
  `session.jsonl`/`MEMORY.md` between calls, nothing held in memory
  between invocations.
- **Respects env vars.** `bash` passes environment down a process tree
  (`PATH`, `HOME`, whatever's exported). `wackypub` does the same with
  `WACKYPUB_CALL_CHAIN` (D16) - it's just inherited by every subprocess a
  cross-agent call spawns, exactly like any other env var, and that's the
  entire mechanism the cross-agent deadlock-cycle guard relies on.
- **Special files in a home folder.** `bash` reads `~/.bashrc`,
  `~/.bash_profile`, etc. Every agent has its own directory functioning
  as its "home": `AGENTS.md`, `MEMORY.md`, `runtime.json`,
  `WACKYPUB_ALLOWED_AGENTS`, `WACKYPUB_ROOT` as the workspace-level marker
  - same idea, same reason (config as plain files a human can read/edit
  directly, not a database or hidden state).
- **Looks up available executables.** `bash` resolves a command name
  against `$PATH`. An agent's `tools/` directory (D14) is the same lookup
  - what's callable is whatever's discoverable there, nothing more.
- **Piping between commands.** `bash` pipes let one command's output feed
  another without going through a human. The scratchpad + `<SCRATCHPAD_DATA
  id="X" />` stdin macro (D18) is the rough equivalent for agents - moving
  data between tool calls without forcing it through the model's own
  context/output tokens.

Worth writing up properly in the README's philosophy section once there's
a natural moment for it - it's a genuinely accurate analogy, not just a
catchy line, and might be a better hook than the current "every capability
is a file" framing alone.

## Consider an explicit hardlink/inode check in `files-rw`'s `Access.Resolve`

Found via the first `files-rw` swarm pen-test (`docs/files-rw-security-test.md`):
a hardlink planted inside a writable root, pointing at `FILES_RW_ACCESS`,
currently gets blocked only because `WriteFile` (`pkg/filesrw/ops.go`)
writes atomically (temp file + rename), which severs the hardlink instead
of writing through the shared inode. That's a real, verified protection,
but it's incidental to why the function is atomic, not a deliberate
defense - already called out in `WriteFile`'s doc comment so nobody removes
the temp+rename pattern without noticing. Whether it's worth adding an
explicit check (e.g. comparing `os.SameFile` between a resolved target and
`FILES_RW_ACCESS`, or checking inode/device numbers directly) so the
protection is intentional rather than a side effect is an open question -
no clear second attack path currently exploits the gap, so this is a
robustness nice-to-have, not an urgent fix.
