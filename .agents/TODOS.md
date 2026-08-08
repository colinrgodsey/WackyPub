# TODOS.md

Deferred work and known gaps. Not a backlog of feature ideas - only things
that are already known to be incomplete, fragile, or blocked on something
external.

## Dedicated `wackypub init` command

Bootstrapping a new workspace's `WACKYPUB_ROOT` file currently requires creating `WACKYPUB_ROOT` by hand (`touch WACKYPUB_ROOT`). A dedicated `wackypub init` command (to create `WACKYPUB_ROOT` and scaffold an agent directory) may be worth adding later.

## Future Scratchpad management (`wackypub scratchpad` CLI commands)

Scratchpad slots are managed in-session via built-in agent tools (`set_scratchpad`/`get_scratchpad`) and command I/O redirection (see DECISIONS.md D18). Adding CLI inspection commands (`wackypub agent <id> scratchpad list/read/clear`) would allow human operators or external tooling to inspect and manage persistent scratchpad slots directly.

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
