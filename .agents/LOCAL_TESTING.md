# LOCAL_TESTING.md

How manual/live testing has actually been done in this repo. There is no
mocked LLM backend for full end-to-end runs, and `go test ./...` can't tell
you whether a real provider actually *accepts* a given request shape (that
took a live 404 against OpenRouter to discover - see DECISIONS.md D6). The
`httptest`-mocked wire-payload tests in `pkg/agent/openai_model_test.go`
(`TestNewOpenAIModel_ReasoningEgress`, `TestNewOpenAIModel_SupportsReasoningDetails`,
etc.) do cover the wiring itself now - see them for the pattern before
writing a throwaway scratch program for the same kind of check. This file
covers what's left: driving the built binary against a real workspace and
either a real backend or an ad hoc `httptest` server, for the things only a
live call (or a full CLI run) can confirm.

## Two workspaces, two different jobs

### `testws/` - gitignored scratch workspace

For exploratory/destructive testing. Safe to put real API keys in its
`runtime.json` files - it's excluded from git entirely (see `.gitignore`).
Nothing under `testws/` is ever committed, so it's fine to mutate, corrupt,
or reset freely.

### `test_agents/` - committed, safe example workspace

For anyone (agent or human) who wants a known-good workspace to point
`--ws` at without setting anything up. Committed to git, so it must never
contain a real API key or other secret. `test_agents/bob/` is the reference
example:

```
test_agents/bob/
├── AGENTS.md          # System prompt, @-includes IDENTITY.md
├── IDENTITY.md         # Character sheet (name, role, personality, directives)
├── runtime.json        # Safe placeholder config: local endpoint, empty apiKey
└── session.jsonl       # A real, multi-turn conversation history in the
                         # current genai.Content wire format
```

There is no `MEMORY.md` in this fixture - that's a valid, normal state (an
agent with no compacted memory yet), not an oversight. There's no
`session.lock` either; `AcquireSessionLock` creates it on first use of any
`agent` command against this directory, so it'll appear locally the moment
you run anything against `test_agents/`. It's in `.gitignore` (`session.lock`,
matched at every directory level), so it won't get committed even if you
`git add -A` - but if you ever see it staged, that's a signal `.gitignore`
regressed, not something to commit through.

`test_agents/bob/session.jsonl` is maintained over time as testing evolves,
not a frozen snapshot from when it was first created - if you use it as a
baseline and the session shape changes (new part types, new fields), expect
this file to have moved forward too. It is also useful as a **format
reference**: if you're unsure what a specific reasoning shape (a `Thought`
part, a `partMetadata` block, multi-part merged user turns after
compaction) looks like on the wire, grep this file for a real example rather
than constructing one by hand.

`test_agents/bob/runtime.json` intentionally points at a plausible local
endpoint (`http://localhost:11434/v1`, Ollama's default) with an empty
`apiKey` - it won't do anything useful without a real backend behind it, but
it's a template to copy and edit, and it can't leak a credential because it
never had one.

## Structuring a `runtime.json` for testing multiple backends

`runtime.json` may be a symlink (`filepath.EvalSymlinks` is resolved
automatically - see `docs/agents.md` §3). `testws/bob/` uses this to switch
backends without editing or duplicating the agent's config:

```
testws/
├── bob/
│   ├── runtime.json -> ../runtimes/<name>.json
│   └── ...
└── runtimes/
    ├── local.json              # llama.cpp/Ollama-style local endpoint
    ├── openrouter-auto.json    # OpenRouter, model: "auto"
    └── openrouter-haiku.json   # OpenRouter, pinned to a specific model
```

Repoint the symlink to swap backends for the same agent and session history:

```bash
ln -sf ../runtimes/openrouter-haiku.json testws/bob/runtime.json
```

This is also how the OpenRouter `"auto"` + encrypted-reasoning bug
(DECISIONS.md D6) was found and then reproduced under a pinned model to
confirm the fix - same agent, same `session.jsonl`, different backend
config, one symlink away.

## Everyday commands

```bash
go build -o wackypub .

# Full turn against a real backend
./wackypub --ws testws agent bob prompt "..."

# Inspect without mutating anything
./wackypub --ws testws agent bob read-session
./wackypub --ws testws agent bob read-memory

# Force compaction to test it on demand, instead of accumulating a huge
# session first: lower contextWindow in the runtime.json this agent points
# at (even a very low value like 2000 works), then run a turn or:
./wackypub --ws testws agent bob compact
```

## Verifying `session.jsonl` integrity by hand

Useful after any manual edit, or after a run that errored partway through:

```bash
python3 -c "
import json
with open('testws/bob/session.jsonl') as f:
    lines = []
    for i, line in enumerate(f):
        line = line.strip()
        if not line: continue
        try:
            lines.append(json.loads(line))
        except json.JSONDecodeError as e:
            print(f'line {i}: BROKEN: {e}')
print('total valid turns:', len(lines))
"
```

To inspect reasoning shape on the last turn (thought parts, block metadata):

```bash
tail -1 testws/bob/session.jsonl | python3 -c "
import json, sys
t = json.load(sys.stdin)
for i, p in enumerate(t['parts']):
    print(i, 'thought:', p.get('thought', False), '| text len:', len(p.get('text','')), '| has partMetadata:', 'partMetadata' in p)
"
```

If you hand-edit `session.jsonl` (e.g. to strip a bad turn), verify the file
still ends with a trailing newline - see `.agents/AGENTS.md`'s "Hand-editing
`session.jsonl` can silently corrupt it" gotcha. This has actually happened
during testing (a manual edit dropped the trailing newline, the next
`prompt` call appended onto the same line, and `ReadSessionTurns` silently
skipped the merged line on the next read).

## Tracing a multi-turn tool-calling exchange

`read-session`'s raw JSON is hard to scan once a turn involves several
`FunctionCall`/`FunctionResponse` round trips (tool loops, cross-agent
`wackypub` calls, scratchpad piping). This prints just the shape that
matters - role, tool name + args, response, and text (thoughts labeled
separately) - starting from wherever a specific turn's text appears, so
you don't have to scroll past the whole history:

```bash
python3 -c "
import json
with open('testws/clerk/session.jsonl') as f:
    lines = f.readlines()
target = 'text to find the start of the turn you care about'
start = 0
for i, line in enumerate(lines):
    obj = json.loads(line)
    for p in obj.get('parts', []):
        if 'text' in p and target in p['text']:
            start = i
for i in range(start, len(lines)):
    obj = json.loads(lines[i])
    for p in obj.get('parts', []):
        if 'functionCall' in p:
            print(f'[{i}] CALL: {p[\"functionCall\"]}')
        elif 'functionResponse' in p:
            print(f'[{i}] RESPONSE: {json.dumps(p[\"functionResponse\"].get(\"response\",{}))[:300]}')
        elif 'text' in p:
            tag = 'THOUGHT' if p.get('thought') else 'TEXT'
            print(f'[{i}] {tag}: {p[\"text\"][:200]!r}')
"
```

This is how the scratchpad-piping trials were verified as actually working
(not just producing a plausible-looking final answer) - e.g. confirming a
`run_command` call's `stdin` was a `<SCRATCHPAD_DATA id="..." />` macro
referencing an earlier tool's output, rather than the model having quietly
re-read and repasted the content itself.

## Test from inside the agent's own directory, not the repo root

Several checks are CWD-based, not just workspace-based: `WACKYPUB_ROOT`
walk-up (D15) and `WACKYPUB_ALLOWED_AGENTS` resolution (D16) both start
from the current working directory, not from `--ws`. Running a command
from the repo root (or anywhere outside the agent's own folder) silently
skips these checks entirely, which looks like success but isn't testing
what an agent's own `run_command wackypub ...` subprocess actually
experiences (`cmd.Dir` is set to the agent's own directory - see D17).

```bash
# WRONG for testing CWD-dependent behavior - runs unrestricted, since the
# repo root isn't inside any agent's directory:
./wackypub --ws testws workspace clerk

# RIGHT - reproduces exactly what clerk's own subprocess calls see:
cd testws/clerk && /path/to/wackypub workspace
```

This exact mistake produced a false "the fix works" result once during
testing - rerunning from inside the agent's own directory caught it.

## Reproducing lock contention and deadlocks

`AcquireSessionLock` writes the holding process's PID directly into
`<agent_dir>/session.lock` - if something seems stuck, `cat` the lock file
and cross-reference with `ps aux` before assuming it's just slow:

```bash
cat testws/clerk/session.lock   # holding PID
ps aux | grep -i wackypub       # what's that PID (and its children) doing
```

To deliberately simulate a held session lock (e.g. to verify a command
that's supposed to be lock-free actually doesn't block, or to reproduce a
suspected deadlock before fixing it), hold the flock in a background
subshell and run the command under test against it with a short timeout:

```bash
(
  exec 200>testws/clerk/session.lock
  flock -x 200
  sleep 12
) &
sleep 1   # let the background subshell actually acquire the lock first
timeout 5 ./wackypub --ws testws workspace clerk 2>&1
echo "exit: $?"   # 124 means it hung and timeout killed it
wait
```

This is exactly how the self-deadlock in `InspectAgent` (an agent's own
`run_command wackypub workspace` call, which inspects every agent
including itself, blocking on a lock its own live `GenerateTurn` already
held) was confirmed live before the fix, and re-confirmed fixed afterward
by rerunning the identical repro.

## Reproducing race conditions with a scratch test

For a suspected concurrency bug (not just "this looks unsafe" but "I want
to see it actually corrupt something"), a throwaway `_test.go` with a
`sync.WaitGroup` spawning several goroutines against the function under
test, run with `-race`, proves it either way:

```go
func TestScratchConcurrentCreates(t *testing.T) {
	agentDir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := agent.CreateScratchpad(agentDir, fmt.Sprintf("payload-%d", i), "test"); err != nil {
				t.Errorf("create %d failed: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
}
```

Run with `go test -run TestScratchConcurrentCreates -race -count=1`.
Prefix the filename with `zz` (e.g. `zzscratch_test.go`) so it sorts last
and is obviously a scratch file, and delete it once you're done - same
throwaway-not-committed convention as the `httptest` scratch programs
below. Keep the exact reproduction script around (don't just fix and move
on) so you can rerun it unmodified after the fix - that's what actually
confirms the fix, not just "the new unit test passes."

## Verifying the outgoing wire payload without spending real API credits

`pkg/agent/openai_model_test.go` already covers this for the wiring that
exists today (`reasoningEgress` modes, `ReasoningField`,
`SupportsReasoningDetails`, `ExtraBody`) using exactly this technique as
real `go test` cases - extend those tables first for anything that fits the
same shape (a new `RuntimeConfig` field affecting the outgoing request).

For something that doesn't fit an existing test's shape, or a one-off check
while debugging, the same technique works as a throwaway Go program with an
`httptest` server standing in for the backend - no real API call, no cost,
and it shows the literal bytes on the wire instead of inferring behavior
from the response:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/colinrgodsey/wackypub/pkg/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func main() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var pretty map[string]any
		json.Unmarshal(body, &pretty)
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	m := agent.NewOpenAIModel(&agent.RuntimeConfig{Model: "test-model", Endpoint: srv.URL})

	req := &model.LLMRequest{
		Model: "test-model",
		Contents: []*genai.Content{
			genai.NewContentFromText("hello", "user"),
			// ...construct whatever history shape you're testing...
		},
	}

	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			fmt.Println("error:", err)
		}
	}
}
```

Run with `go run /tmp/whatever.go` and delete it afterward - this is a
scratch verification tool, not something to commit. If what you learn from
it is worth keeping, turn it into a `_test.go` case using
`captureLastRequestBody`/`generateOnce`/`lastAssistantMessage` from
`pkg/agent/openai_model_test.go` rather than leaving it as a one-off script.

## When something needs a real backend

Wire-shape correctness can be checked with the technique above, but whether
a specific provider actually *accepts* a given shape (does it 400 on an
unrecognized field, does it require a field to be present, does an encrypted
block round-trip) can only be confirmed against the real API - this is
exactly how the OpenRouter `"auto"` + encrypted-reasoning 404 was found; no
amount of reading documentation surfaced it ahead of time. When testing
against a real backend:

- Prefer `testws/` (never `test_agents/`) so a real key never ends up in a
  commit.
- Prefer a cheap/fast model for iteration; save the target model for a final
  confirmation pass.
- If a turn fails partway through (e.g. a 400/404 after the user turn was
  already appended), check `read-session` before retrying - the session may
  be in a valid-but-incomplete state (trailing user turn, no response yet),
  which is fine to resume from, or it may need a turn trimmed first if the
  failure was caused by something now baked into history (like a stale
  encrypted reasoning block or cross-provider thought signature - see
  `strip-signatures`).
- A high-reasoning-effort model (e.g. `extraBody.reasoning.effort: "high"`)
  chaining several tool calls in one turn can genuinely take multiple
  minutes for a single `prompt`/`generate` call - this is normal, not a
  hang. Give live trials a generous timeout (`timeout 590 ...` or run in
  the background) rather than assuming a fast response; a command that
  gets killed by an aggressive timeout looks identical to a real hang
  unless you check what actually got persisted to `session.jsonl` first
  (partial tool-call rounds are appended progressively, not all at once
  at the end - see "Tracing a multi-turn tool-calling exchange" above).

## A stale-looking diagnostic isn't always real

If an inline diagnostic/LSP tool reports an error (`undefined: X`, wrong
argument count) right after an edit that should have fixed exactly that,
but a fresh `go build ./...` / `go vet ./...` / `go test ./...` in the
terminal passes clean - trust the terminal. This happened repeatedly
during this project's development: the diagnostic view lagged behind the
actual file state after a same-file edit, reporting symbols as undefined
that a real build resolved without issue. Don't chase a phantom error by
re-editing already-correct code; re-run the actual build/test command
first.
