#!/bin/bash
#
# Sets up ./container-ws: a demo workspace with one main agent and two
# sub-agents it's allowed to delegate to. Safe to call repeatedly - does
# nothing if container-ws already exists.
#
# Usage: init_container_env.sh <path-to-runtime.json>

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

if [ -d "$REPO_ROOT/container-ws" ]; then
    echo "container-ws already exists - skipping initialization."
    exit 0
fi

if [ -z "$1" ]; then
    echo "Error: first-time setup requires a path to a complete runtime.json." >&2
    echo "Usage: $0 <path-to-runtime.json>" >&2
    exit 1
fi

if [ ! -f "$1" ]; then
    echo "Error: runtime.json not found at $1" >&2
    exit 1
fi

# Resolve before cd'ing into container-ws, so a relative path passed in
# (relative to wherever the caller invoked this script from) still works.
RUNTIME_JSON_PATH="$(realpath "$1")"

cd "$REPO_ROOT"
mkdir container-ws
cd container-ws
mkdir main sub1 sub2

cp ../agents/container/MAIN.md main/AGENTS.md
cp ../agents/container/SUB.md sub1/AGENTS.md
cp ../agents/container/SUB.md sub2/AGENTS.md

mkdir main/skills
cp -r ../skills/wackypub main/skills/

mkdir main/tools
ln -sf /usr/bin/bash main/tools/bash
ln -sf /usr/bin/sed main/tools/sed
ln -sf /bin/wackypub main/tools/wackypub
ln -s ../main/tools sub1/tools
ln -s ../main/tools sub2/tools

touch WACKYPUB_ROOT
printf 'sub1\nsub2\n' > main/WACKYPUB_ALLOWED_AGENTS

cp "$RUNTIME_JSON_PATH" runtime.json
ln -s ../runtime.json main/runtime.json
ln -s ../runtime.json sub1/runtime.json
ln -s ../runtime.json sub2/runtime.json

cp "$SCRIPT_DIR/repl.sh" repl.sh
chmod +x repl.sh

echo "Initialized container-ws/ (main, sub1, sub2) using $RUNTIME_JSON_PATH"
