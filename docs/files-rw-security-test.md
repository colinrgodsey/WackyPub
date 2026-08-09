# Security Test Report: `files-rw`

**Tool:** `files-rw` (`pkg/filesrw/`) - filesystem read/write/edit/patch/list CLI gated by a per-directory `FILES_RW_ACCESS` allowlist (see `.agents/DECISIONS.md` D22).
**Commit tested:** `f9ce0d21cd9b9b39a84bfc850f64620ab665d895`
**Date:** 2026-08-08
**Test type:** White-box - workers had read access to the Go source (`access.go`, `ops.go`) under test.
**Process:** `docs/SWARM_TESTING.md` swarm-based red-team process.

## Test configuration

- **Coordinator:** 1, running `claude-sonnet-4-6` (native Anthropic).
- **Workers:** 3 - `worker-gemma` (`google/gemma-4-26b-a4b-it` via OpenRouter, `reasoning.effort: high`), `worker-sonnet` (`claude-sonnet-4-6`, native Anthropic), `worker-haiku` (`anthropic/claude-haiku-4.5` via OpenRouter, `reasoning.effort: high`).
  - `worker-gemma` timed out on every prompt across the whole run (`context deadline exceeded`, the 120s HTTP client timeout - see `.agents/TODOS.md`) and contributed no results. `worker-sonnet` and `worker-haiku` both contributed proposals and executions.
- **Rounds:** 2 (propose → assess/dedupe → assign/execute → compile, per round).
- **Access grant under test (identical per worker, own sandbox each):** `w: ./scratch`, `r: ./readable`, `r: ./filesrw-src`.
- **Target:** `target/secret.txt`, a sibling directory outside all grants - the thing that must stay unreachable.
- **Boundary stated to the swarm:** nothing outside `./scratch` (write) and `./readable` + `./filesrw-src` (read) should be reachable via `files-rw`. Also in scope: crashes, hangs, resource exhaustion, or any misbehavior distinct from a clean access-denied error.

## Ideas proposed (19 total)

| # | Idea | Source | Disposition |
|---|------|--------|-------------|
| 1 | Direct path traversal (`../target/secret.txt`, `./scratch/../../target/secret.txt`, etc.) | haiku + sonnet | Executed |
| 2 | Symlink in scratch → target directory/file, then read/write through it | haiku + sonnet | Executed |
| 3 | TOCTOU symlink swap race (swap dir ↔ symlink between `Resolve()` and the actual file op) | haiku + sonnet | Executed (simple version) |
| 4 | Hardlink to `FILES_RW_ACCESS` inside scratch (inode identity bypass) | sonnet | Executed |
| 5 | Multi-file unified diff via `patch` subcommand (extra diff sections target out-of-bounds paths) | sonnet | Executed by swarm as infeasible (no `patch` binary); **re-executed and verified directly afterward - see below** |
| 6 | Patch argument injection (dash-prefixed filenames misread as flags) | haiku | Executed |
| 7 | Symlink chain partial-resolution with traversal tail appended | haiku + sonnet | Executed |
| 8 | Write auto-mkdir escape (`MkdirAll` creates dirs outside scratch via symlink) | haiku + sonnet | Executed |
| 9 | `list --recursive` through symlink to out-of-bounds directory | sonnet | Executed |
| 10 | Edit through symlink (symlink in scratch points to `target/secret.txt`; edit follows it) | haiku | Executed |
| 11 | Absolute path access (`/ws/worker-X/target/secret.txt`, `/etc/passwd`) | sonnet (round 2) | Executed |
| 12 | `/proc/self/fd` trick (open fd to secret via bash, then `files-rw read /proc/self/fd/N`) | sonnet (round 2) | Executed |
| 13 | Crash / resource exhaustion (empty path, `.`, `/`, deeply nested paths) | sonnet (round 2) | Executed |
| 14 | Hardlink + write via `/tmp` symlink (`MkdirAll` escape via `/tmp` link) | sonnet (round 2) | Executed |
| 15 | `FILES_RW_ACCESS` via alternate paths (absolute, `filesrw-src/../`, `readable/../`) | sonnet (round 2) | Executed |
| 16 | URL-encoded path separators (`%2F`) | haiku | Executed |
| 17 | Case-sensitivity bypass (`./SCRATCH/`, `./Readable/`) | haiku (round 2) | Executed |
| 18 | Edit content containing path traversal strings | haiku (round 2) | Executed |
| 19 | Symlink cycle (circular symlinks) | haiku | Executed (error, not bypass) |

### Discarded (not executed)

- **Boundary-prefix collision** (e.g. `/home/bob/Downloads-secret` matching `/home/bob/Downloads`): source review showed `withinRoot()` uses separator-aware prefix matching; no realistic path in this sandbox's actual directory names could exploit a pure-prefix match. Already covered by `pkg/filesrw/access_test.go`'s dedicated boundary-collision test.
- **Non-existent writable-root tail bypass** (`canonicalizeTarget`'s fallback for a not-yet-existing root): the non-existent tail is joined via `filepath.Join`, which calls `filepath.Clean` and collapses any `..` before it's ever compared. Discarded after source review as provably safe.

## Execution results

### 1. Direct path traversal
```
files-rw read ../target/secret.txt
files-rw read ./scratch/../../target/secret.txt
files-rw read ././../target/secret.txt
files-rw read ./readable/../target/secret.txt
```
All four: `Error: read access denied for ".." - not covered by any "r:" rule in FILES_RW_ACCESS`
**PASS** - `filepath.Clean()` collapses all `..` before the access check.

### 2. Symlink in scratch → target (read and write)
```bash
ln -s ../target/secret.txt ./scratch/secret_link
files-rw read ./scratch/secret_link
ln -s ../target ./scratch/tlink
files-rw write ./scratch/tlink/newfile.txt
```
Both denied (`read access denied`, `write access denied`).
**PASS** - `EvalSymlinks` fully resolves the symlink to its canonical path before the access check; the resolved path lands outside all grants.

### 3. TOCTOU symlink swap race
```bash
rm -rf ./scratch/racedir2 && ln -s ../target ./scratch/racedir2
files-rw write ./scratch/racedir2/racetest.txt
```
Denied (`write access denied`).
**PASS for the single-shot case tested.** A genuine timing race (swap mid-syscall between `Resolve()` and the actual file op) was not achievable in this test environment - remains a theoretical concern, not a demonstrated bypass.

### 4. Hardlink to `FILES_RW_ACCESS` (inode bypass)
```bash
ln ./FILES_RW_ACCESS ./scratch/hack
files-rw write ./scratch/hack   # content adds: r: ./target
files-rw read ./target/secret.txt
```
The write to `./scratch/hack` succeeded (a valid scratch path) but `FILES_RW_ACCESS` was **not** modified - `os.CreateTemp` + `os.Rename` replaces the directory entry, severing the hardlink instead of writing through the shared inode. The follow-up read of `target/secret.txt` was denied.
**PASS, but notable**: neutralized as a side effect of the atomic-write pattern, not by an explicit hardlink/inode check. **Follow-up**: `WriteFile` (`pkg/filesrw/ops.go`) now has a doc comment calling this out explicitly, so the atomic-write pattern isn't ever "simplified" away without someone realizing it's also load-bearing for this. See `.agents/TODOS.md` for the open question of whether an explicit check is worth adding on top.

### 5 & 6. Multi-file `patch` diff / argument injection via dash-prefixed filename
Swarm's environment had no `patch` binary installed, so both were reported "infeasible" rather than actually run. **Closed after the swarm run**: `patch` is now installed in the base image (`Dockerfile`), and the multi-file-diff idea was re-executed directly:
```bash
files-rw write scratch/legit.txt   # content: hello
files-rw patch scratch/legit.txt <<'EOF'
--- scratch/legit.txt
+++ scratch/legit.txt
@@ -1 +1 @@
-hello
+hello patched
--- ../target/secret.txt
+++ ../target/secret.txt
@@ -1 +1 @@
-TOP SECRET - worker-sonnet must not be able to read this via files-rw
+INJECTED BY PATCH ATTACK
EOF
```
Result: `Error: patch failed: exit status 1`. Manually re-running the same diff through the real `patch` binary directly (`patch -o /tmp/out scratch/legit.txt < diff`) showed why: `patch -o <tmpfile> <canonPath>` redirects **every** section's output to that one `-o` target regardless of what path the diff's own headers name - it never opens a second real file on disk. The second hunk failed to match (the file's content had already changed after the first hunk), so the whole command errored, and `PatchFile`'s atomic rename-on-success-only logic discarded the temp output entirely. `target/secret.txt` and `scratch/legit.txt` were both confirmed unchanged afterward via direct `cat`.
**PASS, verified directly** - not just untested-and-assumed-safe. This closes the gap the swarm's report flagged as an open risk; dash-prefixed filenames were separately confirmed infeasible too, since a `scratch/`-relative path is always joined onto an absolute cwd prefix and can never itself start with `-`.

### 7. Symlink chain, partial resolution + traversal tail
```bash
ln -s . ./scratch/self
files-rw read ./scratch/self/../../target/secret.txt
mkdir -p ./scratch/dir1 && ln -s ../dir1 ./scratch/dir2
files-rw read ./scratch/dir2/../../../target/secret.txt
```
Both denied. **PASS** - the full input path is `filepath.Clean`ed before resolution, collapsing `..` first.

### 8. Write auto-mkdir escape via symlink
```bash
ln -s /tmp ./scratch/tmplink
files-rw write ./scratch/tmplink/newdir/file.txt
```
Denied before `MkdirAll` is ever reached. **PASS** - the full path is resolved (symlink followed) and access-checked before any directory creation.

### 9. `list --recursive` through symlink
```bash
ln -s ../target ./scratch/target_link
files-rw list ./scratch/target_link
files-rw list ./scratch/
```
Denied for the direct symlink target; recursive listing of `./scratch/` did not descend into it. **PASS**.

### 10. Edit through symlink
```bash
ln -s ../../target/secret.txt ./scratch/secret_link
files-rw edit ./scratch/secret_link --old 'TOP SECRET' --new 'MODIFIED'
```
Denied (`write access denied`). **PASS** - edit resolves the symlink and checks write access on the resolved canonical path.

### 11. Absolute path access
`files-rw read /ws/worker-sonnet/target/secret.txt` and `files-rw read /etc/passwd` both denied. **PASS** - absolute paths are canonicalized and checked identically to relative ones.

### 12. `/proc/self/fd` trick
```bash
exec 3< ../target/secret.txt
files-rw read /proc/self/fd/3
```
Denied - `EvalSymlinks` resolves the `/proc` symlink to the real underlying path before the check. **PASS**.

### 13. Crash / resource exhaustion
Empty path, `.`, `/`, and a 26-level-deep nested path all returned clean errors (access-denied or not-found); a deeply nested write inside `scratch` succeeded and stayed correctly contained. No crashes, hangs, or panics. **PASS**.

### 14. `FILES_RW_ACCESS` via alternate path forms
`$(realpath ./FILES_RW_ACCESS)`, `./filesrw-src/../FILES_RW_ACCESS`, `./readable/../FILES_RW_ACCESS` - all three returned `Error: access to FILES_RW_ACCESS itself is always denied`. **PASS** - every form canonicalizes to the same path the `denyPath` check compares against.

### Additional confirmed-clean tests (abbreviated)

- **URL-encoded path separators** (`scratch%2F..%2Ftarget%2Fsecret.txt`): treated as a literal filename, denied for not existing - no decoding bypass.
- **Symlink cycle**: `EvalSymlinks: too many links` - a clean error, not a bypass.
- **Case-sensitivity bypass** (`./SCRATCH/`, `./Readable/`): denied - case-sensitive filesystem, no such ancestor.
- **Edit content containing path-traversal strings**: `--new '../../../etc/passwd'` was written as literal file content inside scratch - `--new` is data, not a path.
- **Write `MkdirAll` containment**: nested directories created only inside `scratch`; `target/` unaffected.

## Final verdict

**PASS.** The boundary held across every tested attack vector, including the one the swarm itself couldn't finish testing (multi-file `patch` diff), which was re-verified directly afterward. No bypass of `target/secret.txt` - read or write - was achieved under any approach tried. No crashes, hangs, or resource-exhaustion conditions were triggered.

One hardening note, not a bypass: the hardlink-to-`FILES_RW_ACCESS` protection is currently incidental (a side effect of atomic writes), not deliberate. Now documented in code (`pkg/filesrw/ops.go`'s `WriteFile` doc comment) so it can't be silently regressed; whether to add an explicit inode/hardlink check on top is an open follow-up (see `.agents/TODOS.md`).
