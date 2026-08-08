#!/bin/bash
#
# One-liner demo environment: initializes ./container-ws if needed, builds
# wackypub, builds/updates the container as a daemon, and attaches the
# main agent's REPL for the user.
#
# Usage:
#   ./scripts/run_container.sh                       # container-ws already exists
#   ./scripts/run_container.sh /path/to/runtime.json  # first run

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
IMAGE_NAME="wackypub-demo"
CONTAINER_NAME="wackypub-demo"

if ! command -v docker >/dev/null 2>&1; then
    echo "Error: docker is required but not found on PATH." >&2
    exit 1
fi

cd "$REPO_ROOT"

if [ ! -d "$REPO_ROOT/container-ws" ]; then
    "$SCRIPT_DIR/init_container_env.sh" "$1"
fi

# Keep repl.sh in sync with the repo's copy even on later runs, not just
# first-time init.
cp "$SCRIPT_DIR/repl.sh" "$REPO_ROOT/container-ws/repl.sh"
chmod +x "$REPO_ROOT/container-ws/repl.sh"

echo "Building wackypub..."
GOOS=linux go build -o wackypub .

if docker container inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
    # Explicitly scoped to containers (not `docker inspect`, which matches
    # images by name too - a leftover image from an earlier build with no
    # matching container broke this check with a template-parsing error,
    # since an image has no .State field).
    echo "Container already exists - refreshing the wackypub binary in place..."
    if [ "$(docker container inspect -f '{{.State.Running}}' "$CONTAINER_NAME")" != "true" ]; then
        docker start "$CONTAINER_NAME" >/dev/null
    fi
    docker cp "$REPO_ROOT/wackypub" "$CONTAINER_NAME:/bin/wackypub"
else
    echo "No existing container - building the image and launching a daemon..."
    docker build -t "$IMAGE_NAME" "$REPO_ROOT"
    docker run -d \
        --name "$CONTAINER_NAME" \
        -v "$REPO_ROOT/container-ws:/ws" \
        --entrypoint tail \
        "$IMAGE_NAME" -f /dev/null
fi

echo "Attaching to main's REPL..."
exec docker exec -it "$CONTAINER_NAME" /ws/repl.sh main
