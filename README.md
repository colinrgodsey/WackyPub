# 🎭 WackyPubAI

A CLI and Go SDK for managing folder-based AI agents, powered by **Google Agent Development Kit (ADK)**.

---

## 🌟 Overview

**WackyPubAI** provides a code-first framework and CLI interface for building and managing folder-based AI agents (roleplay characters, assistants, etc.). Built on Google's **Agent Development Kit (ADK) v2**, WackyPubAI supports folder-based agent environments, an OpenAI-compatible ADK model adapter (official `openai-go` SDK, with reasoning/thinking support across OpenAI, OpenRouter, DeepSeek, Kimi, and other compatible backends), system prompt macro expansion (`@<FILE_PATH>`), and session compaction.

---

## 🚀 Key Features

* **Folder-Based Environment (`--ws`)**: Operates in a workspace folder environment (defaults to CWD, customizable via `--ws <ws_dir>`).
* **Agent Folder Structure (`<ws_dir>/<agent_id>/`)**:
  - `runtime.json`: OpenAI-compatible endpoint, model, auth token, reasoning/compaction settings (supports symlinks).
  - `AGENTS.md`: System prompt supporting file insertion via `@<FILE_PATH>` macros.
  - `MEMORY.md`: Long-term memory store.
  - `session.jsonl`: JSON Lines turn history log (`genai.Content` per line - preserves reasoning/thinking, not just text).
* **OpenAI-Compatible ADK Model Adapter**: Connects to any OpenAI-compatible API endpoint (OpenAI, OpenRouter, DeepSeek, Kimi, vLLM, Ollama, llama.cpp, LM Studio).
* **Automatic Session Compaction**: Triggered when `contextWindow` token limits are exceeded, compacting `sessionCompactPct` of older turns into `MEMORY.md`.
* **CLI + SDK**: Every operation is available both as a `wackypub agent ...` CLI command and as an `AgentSDK` Go method - see `.agents/AGENTS.md`.

For the full architecture reference (schemas, lifecycle diagrams, compaction mechanics, reasoning handling), see [`docs/agents.md`](docs/agents.md). For orientation when working in this repo, see [`.agents/AGENTS.md`](.agents/AGENTS.md).

---

## 📁 Repository Architecture

```
WackyPubAI/
├── main.go                     # Binary entry point
├── cmd/                        # CLI Cobra subcommands
│   ├── root.go                 # Persistent flags (--ws, --config, -m, --api-key)
│   ├── agent.go                # agent <agent_id> add/generate/prompt/strip-reasoning/read-session/read-memory/compact
│   └── version.go              # Version and build details
└── pkg/                        # Core Go packages
    ├── agent/                  # AgentSDK, FolderAgent, OpenAI ADK model adapter, macros, compaction & session store
    └── config/                 # wackypub.yaml (default model, API key) parser & persistence
```

---

## 📦 Installation & Prerequisites

* **Go**: `go 1.25.7+`

### Build Binary
```bash
go build -o wackypub .
```

---

## 💻 CLI Command Usage

### 1. Folder-Based Agent Management

All commands default to current working directory, or specify workspace with `--ws`:

```bash
# Add a user message turn to an agent session (<ws_dir>/<agent_id>/session.jsonl)
./wackypub agent my_agent add "Greetings! What rumors have you heard?"

# Or using --message:
./wackypub agent my_agent add --message "Tell me about the hidden treasure."

# Or piping from stdin:
echo "Analyze the tavern situation." | ./wackypub agent my_agent add

# Generate the agent's turn using Google ADK & OpenAI-compatible endpoint
./wackypub agent my_agent generate

# Or atomically append the user turn and generate in one call:
./wackypub agent my_agent prompt "Tell me about the hidden treasure."

# Inspect session history / memory without modifying anything
./wackypub agent my_agent read-session
./wackypub agent my_agent read-memory

# Manually trigger compaction (normally automatic during generate/prompt)
./wackypub agent my_agent compact

# Strip stale OpenRouter encrypted reasoning blocks after switching models
./wackypub agent my_agent strip-reasoning
```

Run `wackypub agent <command> --help` for full argument/flag documentation on any subcommand.

### 2. Agent Folder Structure (`<ws_dir>/<agent_id>/`)

#### `runtime.json`
```json
{
  "endpoint": "https://api.openai.com/v1",
  "model": "gpt-4o",
  "apiKey": "sk-...",
  "sessionCompactPct": 50.0,
  "contextWindow": 128000
}
```

See [`docs/agents.md`](docs/agents.md) for the full field list, including the reasoning/thinking-related settings (`reasoningEgress`, `reasoningField`, `supportsReasoningDetails`, `extraBody`, `preserveThinking`).

#### `AGENTS.md`
System prompt with macro file inclusion:
```markdown
# Agent Personality: Barnaby
You are Barnaby, a tavern keeper.

@rules/conduct.md
@prompts/gossip.txt
```

---

## 🧪 Testing

Run all package unit tests:
```bash
go test ./...
```

---

## 📄 License

MIT
