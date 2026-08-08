#!/bin/bash
#
# Tears down everything run_container.sh creates: the running container,
# the built image, and the local container-ws workspace (including its
# session history/scratchpad state). Safe to run even if some or all of
# these don't exist - each step is a no-op if there's nothing to remove.
#
# Usage: ./scripts/destroy_container.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
IMAGE_NAME="wackypub-demo"
CONTAINER_NAME="wackypub-demo"

if docker container inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
    echo "Removing container $CONTAINER_NAME..."
    docker rm -f "$CONTAINER_NAME" >/dev/null
fi

if docker image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
    echo "Removing image $IMAGE_NAME..."
    docker rmi "$IMAGE_NAME" >/dev/null
fi

if [ -d "$REPO_ROOT/container-ws" ]; then
    echo "Removing $REPO_ROOT/container-ws..."
    rm -rf "$REPO_ROOT/container-ws"
fi

echo "Done."
