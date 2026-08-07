# LOCAL_TESTING.md

How manual/live testing has actually been done in this repo so far. There is
no mocked LLM backend and, as of writing, no automated coverage for the
OpenAI adapter's reasoning-handling wiring (see TODOS.md) - correctness has
been verified by running the built binary against a real workspace and
either a real backend or a local `httptest` server. This file documents that
workflow so it doesn't have to be rediscovered.

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

## Verifying the outgoing wire payload without spending real API credits

For changes to `pkg/agent/openai_model.go` or the `adk-utils-go` fork's
request-building logic, the fastest way to check the exact JSON sent to the
provider is a throwaway Go program with an `httptest` server standing in for
the backend - no real API call, no cost, and it shows the literal bytes on
the wire instead of inferring behavior from the response:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/colinrgodsey/WackyPubAI/pkg/agent"
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
scratch verification tool, not something to commit. This is how the
`reasoningEgress` modes (native/think_tags/omit) and the
`StripReasoningDetails`/`StripSessionReasoningDetails` behavior were all
confirmed byte-for-byte before ever touching a real backend.

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
  encrypted reasoning block - see `strip-reasoning`).
