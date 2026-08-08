# SECURITY_TESTING.md

Tracks tools that enforce a security boundary (filesystem access, command
execution, cross-agent authorization, or anything else an agent - or a
prompt-injected one - could try to escape) and whether that boundary has
actually been pen/escape-tested: adversarial probes run against a live
build, not just unit tests for the happy path. Unit tests prove the logic
does what the code says; escape testing proves an attacker-shaped input
can't get past it anyway. Both matter, only one is tracked here.

**Directive: if a checked tool's enforcement logic changes, uncheck it in
the same commit.** A passing checkbox is a claim about the code as it stood
when it was last tested, not a permanent property of the tool. Changing the
resolution/containment/validation logic invalidates the claim immediately,
even if the change looks unrelated or is "just a refactor" - re-verify
before trusting it again, the same way `git diff @{u}..` scopes a
verification pass to what actually changed. Adding a new such tool means
adding a row here unchecked.

**Who's qualified to check a box.** A scripted list of probes written by
whoever implemented the tool only tests the escape vectors that person
already thought of - that's regression coverage, not a pen test. A checked
box means the boundary was actually attacked by a capable, motivated
adversary: a large/heavy-hitter model (not the same lightweight pass used
for routine implementation work), ideally a swarm of agents specifically
tasked with breaking it and given genuine incentive/latitude to find
creative escapes rather than confirm the happy path - dogfooding
`wackypub`'s own multi-agent tool-calling to test `wackypub`'s own tools.
Implementer-written adversarial probes and live but non-adversarial usage
(an agent using the tool normally and incidentally hitting a bug) are both
useful and worth keeping as a record, but neither alone earns the checkmark.

## Checklist

- [ ] **`files-rw`** (`pkg/filesrw/`) - filesystem read/write/edit/patch/list
  gated by a per-directory `FILES_RW_ACCESS` allowlist (see DECISIONS.md
  D22). **Not yet pen-tested per the bar above** - unchecked pending a
  heavy-hitter-model or swarm red-team pass. Prior verification on record
  (2026-08-07, does not by itself satisfy the directive): implementer-
  written scripted probes denied read of a file outside any grant, denied
  write to a read-only grant, denied self-access to `FILES_RW_ACCESS`
  itself, rejected a literal `~`, denied everything when the access file
  was missing, denied a symlink planted inside an allowed directory
  pointing outside it, and denied the boundary-prefix collision case
  (`allowed_rw` vs `allowed_rw-secret`); separately, normal (non-adversarial)
  live use through a real agent's tool-call loop (bob) incidentally
  surfaced and got a fix + regression test for a real bug in the
  write-auto-mkdir path for a writable root that didn't exist yet.

## Not yet on this list

Anything that shells out to an external command with agent-influenced
arguments, or resolves an agent-supplied path against a boundary, belongs
here once it exists - `run_command`'s own executable-discovery and
`WACKYPUB_ALLOWED_AGENTS` cross-agent gating are both candidates that
predate this file and haven't been backfilled with a dedicated escape-test
pass yet.
