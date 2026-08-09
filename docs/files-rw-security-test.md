# Security Test Report: `files-rw`

**Tool:** `files-rw` (`pkg/filesrw/`) - filesystem read/write/edit/patch/copy/move/delete/list CLI gated by a per-directory `FILES_RW_ACCESS` allowlist (see `.agents/DECISIONS.md` D22, D23).
**Commit tested:** `64f341f51116ca90014d4b3d1773dfa5f2d6fadc`
**Date:** 2026-08-08
**Test type:** White-box - workers had read access to the Go source (`access.go`, `ops.go`) under test.
**Process:** `docs/SWARM_TESTING.md` swarm-based red-team process (second run - the first run's report was deleted per the invalidation rule when D23 changed the command surface; see `.agents/SECURITY_TESTING.md`).

## Test configuration

- **Coordinator:** 1, running `claude-sonnet-4-6` (native Anthropic).
- **Workers:** 3 - `worker-gemma` (`google/gemma-4-26b-a4b-it` via OpenRouter, `reasoning.effort: high`), `worker-sonnet` (`claude-sonnet-4-6`, native Anthropic), `worker-haiku` (`anthropic/claude-haiku-4.5` via OpenRouter, `reasoning.effort: high`). Same models as the first run.
- **Iterations requested:** 10 rounds (3 new ideas per worker per round, ideas dispatched back to their proposing worker for execution - see the updated coordinator loop in `agents/pen-escape-testing/COORDINATOR.md`).
- **Rounds actually completed:** 4, stopped early by operator instruction (cost/time), not because ideas ran out - at least two more ideas (write-TOCTOU, delete-TOCTOU) were queued but not executed.
- **Access grant under test (identical per worker, own sandbox each):** `w: ./scratch`, `r: ./readable`, `r: ./filesrw-src`.
- **Target:** `target/secret.txt`, a sibling directory outside all grants - the thing that must stay unreachable.
- **Boundary stated to the swarm:** nothing outside `./scratch` (write) and `./readable` + `./filesrw-src` (read) should be reachable via `files-rw`. Also in scope: crashes, hangs, resource exhaustion.

## Confirmed bypasses (7)

| # | Round | Finding | Severity | Repro |
|---|-------|---------|----------|-------|
| 1 | 1 | Hardlink READ bypass | High | `ln target/secret.txt ./scratch/hl && files-rw read ./scratch/hl` |
| 2 | 2 | Hardlink in `./readable` + COPY bypass | High | `ln target/secret.txt ./readable/hl && files-rw copy ./readable/hl ./scratch/out` |
| 3 | 3 | Hardlink reads `FILES_RW_ACCESS` config | Superseded | `ln ./FILES_RW_ACCESS ./scratch/hl && files-rw read ./scratch/hl` - **no longer a bypass**: direct `read` of `FILES_RW_ACCESS` is now intentionally allowed (see the D23 addendum on self-introspection), found the same day this swarm run happened. |
| 4 | 3 | Hardlink reads arbitrary same-filesystem files (`session.jsonl`, `scratchpad.json`, `AGENTS.md`) | High | `ln ./session.jsonl ./scratch/hl && files-rw read ./scratch/hl` |
| 5 | 3 | TOCTOU race on read (~99% success) | High | Loop: `cp target/secret.txt ./scratch/race & files-rw read ./scratch/race` |
| 6 | 3 | `delete` via hardlink decrements the real file's link count | Low-Medium | `ln target/secret.txt ./scratch/hl && files-rw delete ./scratch/hl` |
| 7 | 4 | **Cross-agent hardlink bypass** | **Critical** | `ln /ws/worker-sonnet/target/secret.txt ./scratch/hl && files-rw read ./scratch/hl` |

**Post-run verification (not part of the swarm's own execution, done directly afterward):** the TOCTOU race generalizes to `copy`, not just `read` - confirmed live, 100/100 hits (`cp target/secret.txt scratch/race_src.txt` racing `files-rw copy scratch/race_src.txt scratch/dst.txt`). By code inspection, the identical structural gap (access checked against a path *string*, actual syscall against that same string happens moments later in a separate step, nothing holds a file descriptor open across the gap) exists in `edit`, `patch`, `move`, and `delete` too - not verified live for each individually, but the same root cause applies uniformly across the whole command surface.

## What was blocked

| Attack | Blocking mechanism |
|---|---|
| Path traversal via `..` | `filepath.Clean` before the access check |
| Symlink traversal | `filepath.EvalSymlinks` resolves the symlink target before the check |
| Direct forbidden-path read/write/copy | Correct prefix check against the resolved canonical path |
| Hardlink WRITE/EDIT/PATCH modification | Atomic write-to-temp+rename severs the hardlink before clobbering (see D22/D23) |
| Move to/from a forbidden path | Both sides validated with the appropriate access check |
| Recursive `list` through a symlink loop (DoS) | `ls -R` on Linux doesn't recurse into symlinks |
| Large-file `edit` (DoS) | Handled fine within container memory |
| `move` onto a hardlink path | `os.Rename` replaces the directory entry; the original inode is untouched |
| Unicode/normalization tricks | `filepath.Clean` normalizes before the check |
| Hardlinks to files on a different filesystem | OS-level cross-device restriction |

## Root cause

**Primary: path-based access control with no inode-provenance check.** The access check canonicalizes a requested path (resolving `..` and symlinks) and tests whether the result is prefixed by an allowed root - correct against traversal and symlink attacks, but it never checks *which inode* the approved path names. A hardlink is a second directory entry for an existing inode; placing one inside an allowed directory gives an out-of-bounds inode an in-bounds name, and the path-only check approves it.

**Secondary, independent: TOCTOU between `Access.Resolve()` and the actual I/O.** `Access.Resolve()` returns a path string; the real filesystem operation happens afterward, as a separate step, against that same string. Nothing holds a file descriptor open across the gap, so a concurrent process racing to swap what's at that path wins almost every time (99-100% in testing) - this needs no hardlink, no `ln`, nothing beyond ordinary write access to the same directory.

**Contributing factor, not a `files-rw` bug:** the test container doesn't enforce `fs.protected_hardlinks`, so hardlinks to root-owned and other-agents'-owned files were unrestricted. Amplifies the primary flaw; isn't its cause.

## Final verdict

**`n` - the boundary did not hold.** `target/secret.txt` was read via three independent mechanisms (hardlink in `./scratch`, hardlink via `./readable` + `copy`, and the TOCTOU race), and the hardlink technique generalized to reading arbitrary same-filesystem content, including other agents' files entirely (finding 7 - the critical one: this is not explainable by "the agent already has bash so it's game over," since it's about one agent reading a *different* agent's private files, a materially different concern than a single agent's own sandboxing).

Not fixed in this pass - see `.agents/DECISIONS.md` D24 for why, and `.agents/TODOS.md` for the scoped fix (open-file-descriptor-based access checking) left for a future pass.
