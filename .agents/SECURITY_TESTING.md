# SECURITY_TESTING.md

Tracks tools that enforce a security boundary (filesystem access, command
execution, cross-agent authorization, or anything else an agent - or a
prompt-injected one - could try to escape) and whether that boundary has
actually been pen/escape-tested: adversarial probes run against a live
build, not just unit tests for the happy path. Unit tests prove the logic
does what the code says; escape testing proves an attacker-shaped input
can't get past it anyway. Both matter, only one is tracked here.

**The actual testing process is [`docs/SWARM_TESTING.md`](../docs/SWARM_TESTING.md)** -
a `wackypub`-orchestrated swarm (coordinator + N workers) attacking a live
build inside a disposable Docker container, over some number of propose/
execute/report rounds. Read it before running a test or writing a report.

## States

Three states, not a plain checkbox:

- **`?`** untested / unknown - the default, and the state a tool reverts to
  the moment its enforcement logic changes (see invalidation rule below).
- **`y`** tested, no exploitable finding.
- **`n`** tested, a real finding came out of it. Stays `n` - and its report
  stays on disk - even after a fix, until a fresh test run against the fix
  produces a new report and (hopefully) a new `y`. Bad news doesn't get
  erased, it gets superseded by a dated follow-up.

**Every `y` or `n` entry must link a report in `docs/`** written per
`docs/SWARM_TESTING.md`'s report requirements - exact commit tested,
`iterations` used, every idea proposed (including discarded ones), every
idea actually executed with its exact result, and a final verdict.

**Directive: if a tool's enforcement logic changes, reset it to `?` and
delete its report(s) in the same commit.** A `y`/`n` state is a claim about
the code as it stood when it was last tested, not a permanent property of
the tool - and a report describing code that no longer exists is actively
misleading, not just stale, so it doesn't get to linger the way a real `n`
finding does. This applies even if the change looks unrelated or is "just a
refactor." Adding a new such tool means adding a row here at `?`.

**Who's qualified to write a `y`/`n`.** A scripted list of probes written by
whoever implemented the tool only tests the escape vectors that person
already thought of - that's regression coverage, not a pen test. A `y`/`n`
verdict means the boundary was actually attacked by the swarm process in
`docs/SWARM_TESTING.md`, run against a capable, motivated model - not the
same lightweight pass used for routine implementation work. Implementer-
written adversarial probes and live but non-adversarial usage (an agent
using the tool normally and incidentally hitting a bug) are both useful and
worth keeping as a record, but neither alone earns `y` or `n`.

## Checklist

- **`n` `files-rw`** (`pkg/filesrw/`) - filesystem read/write/edit/patch/
  copy/move/delete/list gated by a per-directory `FILES_RW_ACCESS`
  allowlist (see DECISIONS.md D22, D23, D24). Swarm-tested 2026-08-08
  against commit `64f341f` (see
  [`docs/files-rw-security-test.md`](../docs/files-rw-security-test.md)):
  7 confirmed bypasses, most seriously a hardlink read that generalizes to
  **cross-agent** file reads (one agent reading a *different* agent's
  files, not just its own out-of-bounds ones) and a TOCTOU race between
  `Access.Resolve()` and the actual I/O that needs no hardlink at all
  (99-100% reliable). Root cause and fix shape recorded in D24; not fixed
  in this pass - `.agents/TODOS.md` has the scoped fd-based rewrite. This
  stays `n` (not reset to `?`) until that fix lands and a fresh swarm run
  against it produces a new report.

## Not yet on this list

Anything that shells out to an external command with agent-influenced
arguments, or resolves an agent-supplied path against a boundary, belongs
here once it exists - `run_command`'s own executable-discovery and
`WACKYPUB_ALLOWED_AGENTS` cross-agent gating are both candidates that
predate this file and haven't been backfilled with a dedicated escape-test
pass yet.
