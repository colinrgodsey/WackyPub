# 🎭 WackyPub

A CLI and Go SDK for folder-based AI agents — built on Google's **Agent Development Kit (ADK) v2** — where every agent is just a directory, every capability is a plain file, and the same command interface an agent uses to explore its own tools is the one you use from your terminal.

---

## What it is

An agent in WackyPub is a folder. `AGENTS.md` is its system prompt, `MEMORY.md` is what it remembers long-term, `session.jsonl` is its turn history, `runtime.json` says which model backend it talks to. That's the whole foundation — no database, no bespoke config DSL, nothing that isn't a file you could open in a text editor and understand immediately.

On top of that foundation, an agent can be handed capabilities the same way: a `tools/` folder full of executables it can run, a `skills/` folder full of distilled knowledge it can load on demand, and a persistent scratchpad for passing data between tool calls without ever paying to regenerate it. And because every agent is just a workspace directory next to every other agent, one agent can call another — with real authorization and deadlock safety, not just crossed fingers — turning a single roleplay agent into a coordinating cast, or a lone assistant into a small swarm of specialty agents.

---

## Quick Start

**Prerequisites:** Go and Docker.

```bash
./scripts/run_container.sh /path/to/your/runtime.json
```

That's it — from the repo root, this builds `wackypub`, builds and launches a small Ubuntu container as a daemon, and drops you straight into an interactive REPL talking to `main`, an agent running inside it. (First run needs a real `runtime.json` — see [`examples/runtimes/`](examples/runtimes) for a template to copy and fill in a key. Every run after that just needs `./scripts/run_container.sh`, no argument.)

What makes this worth trying isn't the container - it's how little is actually in it. `main` has two sub-agents (`sub1`, `sub2`) it's allowed to delegate to, and that's the entire environment:

- **One skill**: the `wackypub` skill itself for the main agent - a short, always-loaded primer on how to self-discover the CLI via `--help`. Nothing else is pre-loaded.
- **A handful of lines of actual instruction** in each agent's `AGENTS.md` - "you manage an ubuntu system with full root access," how to invoke `bash`, and (for `main` only) "don't let your sub-agents cheat." See [`agents/container/`](agents/container) for the exact text - it's short enough to read in full.
- **Three commands wired up as tools**: `bash`, `sed`, and `wackypub` itself (for reaching sub-agents directly, rather than through a `bash -c "wackypub ..."` detour). `bash` alone would technically be enough - it can reach anything on the container's `PATH` - but linking specific commands in directly is more efficient for a model to call than always routing through a shell.
- **The built-in scratchpad and skill tools** (`create_scratchpad`, `get_scratchpad`, `list_scratchpads`, `load_skill`) - the same ones every agent gets for free, nothing container-specific.

No file-editing tool, no curated command list, no task-specific scaffolding. Ask `main` to do something real - write a script, look something up, coordinate its sub-agents on a piece of work - and it figures out the rest from `--help` and first principles. When you're done, `./scripts/destroy_container.sh` tears down the container, image, and workspace.

Example prompts (these even work on Haiku and Gemma 4 A4B):
- `can you install sqlite and make a test database. verify you can use queries to create a table. while thats happening, can you have a sub-agent write and run a go script to compute the first 20 prime numbers, and have the other sub-agent pull the most up-to-date wikipedia article for dogs and give me the word count of the article plus 5 interesting facts.`

---

## Philosophy

**The CLI is the interface — for you and for the agent.** Most agent frameworks give the model a bespoke tool schema and hide the CLI behind an SDK. WackyPub does the opposite: the thing an agent gets access to *is* `wackypub` itself, constrained to run one command at a time. `--help` at every level has to be complete enough to drive correctly with zero external documentation, because that's genuinely all an agent has. If a human can figure out the tool from `--help` alone, so can a model — and that constraint has caught real, non-hypothetical bugs (a `--help` routing gap, a misleading error label) that would never have surfaced from an SDK-only design.

**Plain files over infrastructure.** Every piece of agent state — identity, memory, history, config, tools, skills, scratchpad — is something you can `cat`, edit by hand, `git diff`, or `symlink`. Swapping a model backend is repointing one symlink. Sharing a toolset or a skill across agents is one more symlink. Nothing here needs a server, a database, or a special editor to inspect or modify.

**Every tool is a command.** There's no plugin system, no capability-registration API. Past the handful of built-ins (mostly scratchpad and skill loading), everything an agent can do is an executable linked into its `tools/` folder. Link in only the specific commands you want it to have, or link in `bash` to give it access to everything, or both - link a few specific commands directly (more efficient for the model to call) alongside `bash` as a fallback for everything else. Want it to orchestrate other agents? Link `wackypub` itself back in - it's not special-cased, it's just another executable an agent happens to invoke.

**Capabilities are composable primitives, not a monolith.** `run_command` is one generic tool that dispatches to anything in `tools/` — drop in any executable and it's usable, no custom schema required. Skills follow the same shape other agent harnesses already use (`SKILL.md` with YAML frontmatter), so skills written elsewhere work here with no translation. The scratchpad exists because generation is the expensive part of a token budget, not consumption — an agent can pipe one command's output directly into another's input, or fork one payload out to several downstream calls, without a single one of those bytes ever being generated or re-read by the model itself.

**Trust, but verify — even for agents.** Cross-agent access is default-deny: an agent can only reach the peers explicitly listed in its own `WACKYPUB_ALLOWED_AGENTS`, and a separate call-chain mechanism refuses any cycle before it can deadlock, regardless of what an allowlist would otherwise permit. Two different concerns, two different mechanisms, neither one standing in for the other. And true to the "every tool is a command" point above, none of this required special-casing `wackypub` itself as a tool - the only wackypub-specific machinery anywhere in the codebase is a couple of carefully scoped environment variables carrying the authorization/cycle-detection state between invocations, not a bespoke integration.

---

## Why it's simple

- An agent's entire identity and behavior lives in files you already know how to read: Markdown for prompts and memory, JSON Lines for history, JSON for config.
- Adding a tool is dropping or linking an executable in a folder. Adding a skill is dropping or linking a `SKILL.md` folder in the same way. No registration step, no schema to hand-author.
- The CLI *is* the SDK's surface — every `wackypub agent ...` subcommand has a matching `AgentSDK` Go method, so there's exactly one behavior to learn, not two.
- Nothing about the system depends on a specific model provider. The same OpenAI-compatible adapter talks to OpenAI, OpenRouter, DeepSeek, Kimi, vLLM, Ollama, llama.cpp, or LM Studio, and reconciles their different ways of expressing reasoning/thinking content.

## Why it's great

- **Agents can talk to agents, for real.** Not a scripted illusion — an agent can autonomously chain multiple calls to a peer agent within a single turn, using the peer's actual response to formulate its own follow-up, with the full exchange persisted and inspectable afterward.
- **Data can move without ever costing tokens to move it.** Pull a large command's output into a scratchpad entry, then pipe that entry straight into three different downstream calls — the model never regenerates or re-reads the payload itself, it just references it.
- **It's self-describing enough to actually work unattended.** A model with nothing but `--help`, tool descriptions, and a workspace has been shown, live, to explore its own environment, discover the correct invocation syntax through trial and correction, invoke a peer agent, and pipe data between two separate tool calls — all without being told the exact mechanics in advance. Skills aren't required for any of this - they're a refinement on top, for making a specific tool or task faster and more reliable to use than figuring it out cold every time, not a prerequisite for an agent doing anything at all.
- **A session isn't locked to the model that started it.** `session.jsonl` is a plain, model-agnostic wire format - moving an agent's entire history to a different backend is repointing `runtime.json` (often just one symlink), nothing about the conversation itself has to change.
- **Nothing here is a black box.** Every claim above has been verified by literally reading the session transcript afterward, not just trusting the summary — and the honest gaps (a `--help` ordering quirk, a lock that needed to not exist) get written down and fixed, not glossed over.

---

## Use cases

- **Multi-character roleplay and narrative campaigns** — each character is its own agent with its own memory and voice; a narrator or player can interview them, and they can interview each other.
- **Agent swarms** — a coordinator agent delegating to specialist agents (a researcher, a writer, a critic), each with its own tools and skills, talking to each other through the same authorized-invocation mechanism.
- **Tool-calling evaluation and coherency testing** — stand up an agent whose entire job is stress-testing your own tools and reporting back on what confused it (this is genuinely how several real bugs in this project were found).
- **Personal automation with real system tools** — symlink a toolset of read-only (or read-write, if you trust it) system utilities into an agent's `tools/` folder and let it operate your machine within whatever boundary you've drawn.
- **Distilled, reusable knowledge across agents** — write a skill once (how to use a particular CLI, a house style guide, domain-specific guidance), symlink it into every agent that needs it, and update it in one place.

---

## Repository Architecture

```
WackyPubAI/
├── main.go                     # Binary entry point
├── cmd/                        # CLI Cobra subcommands
│   ├── root.go                 # Persistent flags (--ws, --config, -m, --api-key, --max-tool-turns)
│   ├── agent.go                # agent <id> add/generate/prompt/read-session/read-memory/render-prompt/compact/strip-reasoning
│   ├── workspace.go            # workspace [agent_id] - read-only diagnostics
│   └── version.go              # Version and build details
└── pkg/                        # Core Go packages
    ├── agent/                  # AgentSDK, FolderAgent, OpenAI ADK model adapter, tools, skills, scratchpad, macros, compaction
    └── config/                 # wackypub.yaml (default model, API key) parser & persistence
```

For the full architecture reference (schemas, lifecycle, compaction mechanics, reasoning handling), see [`docs/agents.md`](docs/agents.md). For orientation when working in this repo, see [`.agents/AGENTS.md`](.agents/AGENTS.md) and the numbered design decisions in [`.agents/DECISIONS.md`](.agents/DECISIONS.md).

---

## Installation & Prerequisites

* **Go**: `go 1.25.7+`

```bash
go build -o wackypub .
```

---

## Agent Folder Structure (`<ws_dir>/<agent_id>/`)

```
<agent_id>/
├── runtime.json                # Model backend config (may be a symlink)
├── AGENTS.md                   # System prompt, supports @<FILE_PATH> macro inclusion
├── IDENTITY.md                 # (optional) character sheet, included via @IDENTITY.md in your AGENTS.md
├── MEMORY.md                   # Long-term memory, updated by compaction
├── session.jsonl               # Turn history, one genai.Content per line
├── scratchpad.json             # Persistent, session-scoped data store (managed by tools, not hand-edited)
├── WACKYPUB_ALLOWED_AGENTS     # (optional) other agent IDs this agent may invoke; absent = deny-all
├── tools/                      # Executables (or symlinked toolpacks) this agent can run via run_command
└── skills/                     # SKILL.md-based distilled knowledge, on-demand or always-loaded
```

`runtime.json`:

```json
{
  "endpoint": "https://api.openai.com/v1",
  "model": "gpt-4o",
  "apiKey": "sk-...",
  "sessionCompactPct": 50.0,
  "contextWindow": 128000
}
```

See [`docs/agents.md`](docs/agents.md) for the full field list, including reasoning/thinking-related settings (`reasoningEgress`, `reasoningField`, `supportsReasoningDetails`, `extraBody`, `preserveThinking`).

`AGENTS.md`:

```markdown
# Character Agent
You are a character in a medieval setting.

@IDENTITY.md

@../rules/conduct.md
```

---

## CLI Usage

Every command is self-documenting — run `wackypub [command] --help` at any level for exact arguments, flags, and preconditions.

```
$ wackypub --help
```

```bash
# Diagnose the whole workspace, or one agent, without changing anything
./wackypub --ws my_workspace workspace
./wackypub --ws my_workspace workspace my_agent

# Add a user turn, or add-and-generate atomically (the recommended way to drive a turn)
./wackypub agent add my_agent "Greetings! What rumors have you heard?"
./wackypub agent prompt my_agent "Tell me about the hidden treasure."

# Inspect without mutating anything
./wackypub agent read-session my_agent
./wackypub agent read-memory my_agent
./wackypub agent render-prompt my_agent

# Manually trigger compaction (normally automatic during generate/prompt)
./wackypub agent compact my_agent

# Strip stale OpenRouter encrypted reasoning blocks after switching models
./wackypub agent strip-reasoning my_agent
```

`wackypub agent <cmd> <agent_id>` and `wackypub agent <agent_id> <cmd>` are both supported for real invocation (though `--help` only resolves correctly with the subcommand name first — put it right after `agent`).

---

## Testing

```bash
go test ./...
```

For live/manual testing against a real backend and the techniques developed for it (tracing tool-calling exchanges, reproducing lock contention, verifying wire payloads without spending API credits), see [`.agents/LOCAL_TESTING.md`](.agents/LOCAL_TESTING.md).

---

## License

[MIT](LICENSE)
