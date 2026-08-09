---
name: wackypub
description: Overview of wackypub CLI and self-discovery of core subcommands via --help. Load before using wackypub.
always_load: false
---
# WackyPub AI CLI & Self-Discovery Guide

`wackypub` is a Go CLI and SDK for managing folder-based AI agents built on Google Agent Development Kit (ADK) v2. Each agent's runtime configuration, system prompt, long-term memory, turn history, persistent scratchpad, tools, and skills live in plain files under a workspace directory (`<ws_dir>/<agent_id>/`).

## Command Discovery via `--help`

The CLI is a thin wrapper over `AgentSDK`. Every subcommand is self-documenting — use `--help` to inspect exact command signatures, argument definitions, flag options, and preconditions without making assumptions about syntax.

### 1. Workspace Self-Inspection
Before running operations on an agent, use `workspace` to inspect the workspace layout or diagnose an individual agent's state:
```bash
# List all discovered agent directories and top-level diagnostic summary
wackypub workspace

# Report detailed on-disk state, missing files, and issues for <agent_id>
wackypub workspace <agent_id>
```

### 2. Auto-Discovering Commands & Options
Use `--help` at any level of the command hierarchy to explore usage:
```bash
# Global flags (--ws <dir>, --config <file>, -m/--model <model>, --api-key <key>)
wackypub --help

# List all agent management subcommands
wackypub agent --help

# Detailed usage for specific agent operations
wackypub agent prompt --help
wackypub agent generate --help
wackypub agent add --help
wackypub agent read-session --help
wackypub agent read-memory --help
wackypub agent render-prompt --help
wackypub agent compact --help
wackypub agent strip-signatures --help
```

### 3. Syntax & Execution Conventions
- **Positional Agent Dispatch**: Both `wackypub agent <cmd> <agent_id> [args...]` and `wackypub agent <agent_id> <cmd> [args...]` (agent ID first) are supported.
- **Flag Ordering Caveat**: flags do *not* work with the agent-id-first ordering above — not just `--help`, *any* subcommand-specific flag (`--message`, `--skip-lines`, `--regex`, etc.). `wackypub agent prompt --help` and `wackypub agent <agent_id> add --message "..."` work; `wackypub agent <agent_id> prompt --help` and `wackypub agent <agent_id> add --message "..."` (agent ID before the subcommand) fail with `unknown flag`, because the outer `agent` command's own flag parsing runs before the agent-id-first form ever dispatches to the subcommand's `RunE` - the subcommand's flags were never registered on the outer command. If a command takes a flag, put the subcommand name directly after `agent`, agent ID after that: `wackypub agent <cmd> <agent_id> [flags]`. Positional-only calls (no flags) work fine in either order.
- **Workspace Discovery (`--ws`)**: Specify `--ws <path>` to target a workspace directory containing `WACKYPUB_ROOT`. If omitted, `wackypub` automatically walks up from CWD to find the nearest workspace root.
- **Cross-Agent Access is Gated**: An agent can only target other agents listed in its own `WACKYPUB_ALLOWED_AGENTS` file (default is deny-all if that file doesn't exist). Attempting to reach an unauthorized agent — including yourself, unless explicitly listed — fails with a clear authorization error rather than succeeding.

### 4. Hints
- `agent add` and `agent generate` are for advanced usage, simple request->response flows should generally use `agent prompt`.

## Setting Up an Agent & Swarm From Scratch

Everything below is on-disk convention, not CLI flags — `wackypub workspace <agent_id>` will
report what's present/missing for an existing agent, but won't tell you how to create these
in the first place. `wackypub workspace` and `wackypub workspace <agent_id>` are still the
first things to run — use them to check progress as you set each piece up.

1. **New workspace needs a root marker.** `wackypub` refuses to treat a directory as a
   workspace until it contains a `WACKYPUB_ROOT` file (any content, usually empty):
   ```bash
   mkdir -p ws && touch ws/WACKYPUB_ROOT
   ```
2. **`<ws_dir>/<agent_id>/runtime.json`** — the model backend config, required before an agent
   can generate anything. Minimal example:
   ```json
   { "provider": "gemini", "model": "gemini-2.5-flash", "apiKey": "...", "sessionCompactPct": 50.0, "contextWindow": 200000 }
   ```
   `provider` is `"gemini"`, `"anthropic"`, or `"openai"`/`"openai-compatible"` (needs an
   `endpoint` too). Full schema: `docs/agents.md` §3, if present in this checkout — not
   guaranteed to ship alongside the binary. To point several agents at the same backend
   config without duplicating it, symlink: `ln -s ../runtimes/shared.json ws/<agent_id>/runtime.json`.
3. **`<ws_dir>/<agent_id>/tools/`** — how an agent gets tools. Any executable file (or a
   symlink to one) placed in this directory, recursively, becomes a callable tool named after
   the filename. To give an agent the `wackypub` CLI itself as a tool (needed for orchestrator/
   coordinator agents that call sub-agents):
   ```bash
   ln -s "$(command -v wackypub)" ws/coordinator/tools/wackypub
   ```
   Two files resolving to the same tool name shadow each other (a warning, not an error —
   `wackypub workspace <agent_id>` reports it).
4. **`<ws_dir>/<agent_id>/skills/<skill_name>/SKILL.md`** — how an agent gets skills, same
   shape as this file: YAML frontmatter with `name`, `description`, and `always_load`
   (`true` injects it into every prompt unconditionally, like this file; `false` — the
   default — makes it available on demand via the `load_skill` tool, discoverable by its
   `description`).
5. **`<ws_dir>/<agent_id>/WACKYPUB_ALLOWED_AGENTS`** — required for any cross-agent calling.
   Plain text, one target agent ID per line; blank lines and `#`-prefixed lines are ignored:
   ```
   # sub-agents this coordinator may call
   sub1
   sub2
   ```
   Missing file = deny-all, even for calling yourself.
6. **`<ws_dir>/<agent_id>/AGENTS.md`** — optional; a generic `"You are agent <id>."` prompt
   is used if it's missing. This is where an agent's persona/instructions/role go.
