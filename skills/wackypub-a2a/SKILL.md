---
name: wackypub-a2a
description: Guide for using the wackypub CLI for agent-to-agent (A2A) communications, command discovery, and inter-agent calling.
always_load: false
---
# WackyPub AI A2A Communication & CLI Guide

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
- **Flag Ordering Caveat**: Flags do *not* work with the agent-id-first ordering above — not just `--help`, *any* subcommand-specific flag (`--message`, `--skip-lines`, `--regex`, etc.). `wackypub agent prompt --help` and `wackypub agent <agent_id> add --message "..."` work; `wackypub agent <agent_id> prompt --help` and `wackypub agent <agent_id> add --message "..."` (agent ID before the subcommand) fail with `unknown flag`, because the outer `agent` command's own flag parsing runs before the agent-id-first form ever dispatches to the subcommand's `RunE`. If a command takes a flag, put the subcommand name directly after `agent`, agent ID after that: `wackypub agent <cmd> <agent_id> [flags]`. Positional-only calls (no flags) work fine in either order.
- **Workspace Discovery (`--ws`)**: Specify `--ws <path>` to target a workspace directory containing `WACKYPUB_ROOT`. If omitted, `wackypub` automatically walks up from CWD to find the nearest workspace root.
- **Cross-Agent Access is Gated**: An agent can only target other agents listed in its own `WACKYPUB_ALLOWED_AGENTS` file (default is deny-all if that file doesn't exist). Attempting to reach an unauthorized agent — including yourself, unless explicitly listed — fails with a clear authorization error.

### 4. Inter-Agent Communication (A2A)
- Simple request->response flows between agents should use `agent prompt`:
  ```bash
  wackypub agent prompt <target_agent_id> "Message content"
  ```
- `AGENT2AGENT` metadata (caller ID, call chain, trace ID, and sender git commit revision) is automatically propagated across environment variables down the execution chain.
