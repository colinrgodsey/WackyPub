# 🐝 Swarm-Based Security Testing

The process used to satisfy the pen/escape-testing bar in
[`.agents/SECURITY_TESTING.md`](../.agents/SECURITY_TESTING.md): a
`wackypub`-orchestrated swarm of agents attacking a real, live build of the
tool under test, coordinated through an idea-propose / dedupe / execute /
report loop, run inside a disposable Docker container. This dogfoods
`wackypub`'s own multi-agent tool-calling to test `wackypub`'s own tools.

## Why swarm, not one scripted pass or one solo agent

A scripted probe list only tests the vectors its author already thought of.
A single agent - even a capable one - explores one train of thought at a
time. A swarm of independent workers proposing ideas in parallel, cross-
pollinated and refined by a coordinator across several rounds, covers more
of the real attack surface than either alone, for the same reason a human
red team is more than one person.

## Environment: always inside the Docker container

The entire process - coordinator, every worker, the tool under test, and
every fixture the swarm creates - runs inside the disposable container
already set up for this repo (`scripts/run_container.sh`,
`scripts/init_container_env.sh`, `scripts/destroy_container.sh`; see
`.agents/LOCAL_TESTING.md`). This is not optional: a genuine escape found by
the swarm should be contained to a throwaway environment, not the host
running the test. Destroy and recreate the container between test runs so
a prior round's fixtures (symlinks, planted files, whatever the swarm built)
never leak into the next one.

## Roles

### Coordinator

Does not attack the tool directly. Runs the process:

1. **Propose.** Prompt every worker for a batch of candidate attack ideas
   against the tool's stated boundary (what should be impossible; what the
   grant does and does not cover).
2. **Collect.** Gather all worker-submitted ideas for the round.
3. **Assess & deduplicate.** Merge near-duplicate ideas, discard ones that
   don't actually apply to this tool/boundary, and keep the rest as this
   round's candidate list. Record discarded/merged ideas too (see report
   requirement below) - a future run shouldn't waste a round rediscovering
   an idea already ruled out.
4. **Top up if thin.** If the surviving idea set is small or repetitive,
   go back to step 1 and ask workers for more - specifically pointing out
   what's already covered, so the next batch reaches further. Repeat within
   the round until the idea set is healthy or a per-round retry budget is
   spent.
5. **Assign & execute.** Once ideas are settled, hand them out to workers to
   actually run against a live instance of the tool (not reason about
   hypothetically) and report back: exact commands, exact output, whether
   the goal was achieved.
6. **Compile.** Gather worker execution reports into the round's findings.
7. **Iterate.** Repeat the whole propose -> dedupe -> execute -> report cycle
   for `iterations` rounds. `iterations` is not fixed by this process doc -
   it's chosen per test run and declared in that run's report (see below).
   Later rounds should build on earlier findings (a partial weakness found
   in round 2 is exactly what round 3's ideas should try to push further).
8. **Write the report.** Produce `docs/<tool-name>-security-test.md` (exact
   contents below) and update `.agents/SECURITY_TESTING.md`'s state for
   that tool.

### Workers (N of them, N is a per-run choice)

Each worker gets the tool under test wired up exactly as a real deployment
would (symlinked into their own `tools/`, invoked through `run_command` like
any other agent tool - not a mocked interface), plus source read access for
white-box rounds where that's appropriate. Each round, a worker either:
proposes ideas when asked, or executes a specific assigned idea against a
live instance and reports back precisely what happened - including ideas
that didn't pan out; a clean "tried X, blocked as expected, here's the
error" is still a useful record, not just successes.

## Report requirement: `docs/<tool-name>-security-test.md`

Every checklist entry in `.agents/SECURITY_TESTING.md` marked `y` or `n`
(see states below) must have a matching report in `docs/`. Required
contents:

- Tool tested, and the exact commit/version it was tested against.
- `iterations` used for this run, and coordinator/worker counts.
- Every idea proposed each round - including discarded or deduplicated
  ones, and why they were dropped.
- For every idea actually executed: exact repro steps, exact observed
  result, and a pass/fail verdict against that idea's specific goal.
- Final overall verdict for the tool.
- If anything was found: a link to the fix (commit, or a
  `.agents/DECISIONS.md` entry) and to the follow-up report that re-tested
  the fix.

## Checklist states (`.agents/SECURITY_TESTING.md`)

Three states, not a plain checkbox:

- **`?`** untested / unknown - the default. No report exists, or a report
  existed but was deleted (see invalidation rule below).
- **`y`** tested, no exploitable finding - a report exists.
- **`n`** tested, a real finding came out of it - a report exists and is
  **kept even when the finding is bad news**. A subsequent fix and re-test
  produces a new, separate, dated report and updates the state to `y` (or
  back to `n`, if the fix didn't hold) - the original `n` report documenting
  the original finding is not deleted or overwritten. It's part of the
  tool's security history, not a mistake to erase.

## Mandatory invalidation rule

The moment a tested tool's enforcement logic changes - the same trigger
already defined in `.agents/SECURITY_TESTING.md` for resetting its
state - **every report in `docs/` describing the old behavior must be
deleted**, not just the checklist state reset to `?`. A report describing
code that no longer exists is actively misleading rather than merely
outdated, so it doesn't get to linger as history the way a genuine `n`
finding does. State reverts to `?` until a new swarm run against the
changed tool produces a new report.
