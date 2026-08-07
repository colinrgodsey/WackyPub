# TODOS.md

Deferred work and known gaps. Not a backlog of feature ideas - only things
that are already known to be incomplete, fragile, or blocked on something
external.

## In-process agent tooling doesn't exist yet

The plan (see `.agents/AGENTS.md`'s Project Overview and CLI/SDK/tool
documentation pattern) is for `AgentSDK` operations to eventually be
callable as in-process tools by an agent running in the same process,
without shelling out to the `wackypub` binary. None of that exists yet -
today `AgentSDK` methods are only reachable via the CLI. When it's built,
it should reuse the same command descriptions/argument docs that the CLI's
`Short`/`Long`/flag help text already carries, rather than a hand-written
third copy of "what this operation does." Worth deciding at that point
whether descriptions live on the `AgentSDK` method doc comments (with CLI
and tool-schema generation both reading from there) or in some other shared
metadata structure - not decided yet. `AgentInspection` (`AgentSDK.InspectAgent`)
is a step in this direction: it returns a typed struct rather than
pre-formatted text, so a future tool layer can consume it directly instead
of parsing `workspace`'s CLI output.

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

## No automated test coverage for reasoning-handling wiring

`MergeConsecutiveUserTurns`, `StripReasoningDetails`,
`StripSessionReasoningDetails`, and the `RuntimeConfig` -> `adkopenai.Config`
field mapping in `NewOpenAIModel` have all been validated by hand (manual
`httptest`-mocked wire-payload inspection, and live runs against real
OpenRouter/llama.cpp backends) but none of that is captured as `_test.go`
files. If the adk-utils-go fork's wire behavior or field names change, there
is nothing that would catch a silent regression except another manual run.
Worth adding at minimum: a mocked-server test asserting the exact outgoing
JSON for each `reasoningEgress` mode, and a unit test for
`MergeConsecutiveUserTurns`'s merge/no-merge boundary cases.

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
