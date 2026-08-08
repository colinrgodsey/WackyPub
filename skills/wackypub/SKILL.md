---
name: wackypub
description: Overview of wackypub CLI and self-discovery of core subcommands via --help
always_load: true
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
wackypub agent strip-reasoning --help
```

### 3. Syntax & Execution Conventions
- **Positional Agent Dispatch**: Both `wackypub agent <cmd> <agent_id> [args...]` and `wackypub agent <agent_id> <cmd> [args...]` (agent ID first) are supported.
- **`--help` Ordering Caveat**: `--help` resolution does *not* honor the flexible agent-id-first ordering above — it only shows subcommand-specific help when the subcommand name comes directly after `agent` (e.g. `wackypub agent prompt --help`, not `wackypub agent <agent_id> prompt --help`). If `--help` on a subcommand unexpectedly shows the generic `agent` help instead, this ordering is almost always why.
- **Workspace Discovery (`--ws`)**: Specify `--ws <path>` to target a workspace directory containing `WACKYPUB_ROOT`. If omitted, `wackypub` automatically walks up from CWD to find the nearest workspace root.
- **Cross-Agent Access is Gated**: An agent can only target other agents listed in its own `WACKYPUB_ALLOWED_AGENTS` file (default is deny-all if that file doesn't exist). Attempting to reach an unauthorized agent — including yourself, unless explicitly listed — fails with a clear authorization error rather than succeeding.

### 4. Hints
- `agent add` and `agent generate` are for advanced usage, simple request->response flows should generally use `agent prompt`.