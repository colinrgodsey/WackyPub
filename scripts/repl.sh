#!/bin/bash
#
# Simple interactive REPL for a single agent - avoids having to quote every
# message when driving it manually via `wackypub agent prompt`.
#
# Usage: ./repl.sh [agent_id]   (default: main)

AGENT_ID="${1:-main}"

echo "wackypub REPL - agent '$AGENT_ID'. Ctrl+D to quit."
while IFS= read -r -p "$AGENT_ID> " line; do
    [ -z "$line" ] && continue
    wackypub agent prompt "$AGENT_ID" "$line"
    echo
done
echo
