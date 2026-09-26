#!/bin/sh
# Example operator hook. "acquire" exits 75 (pending) on the first call and
# succeeds afterwards, demonstrating the controller's pending-retry path.
# "release" is idempotent: removing a missing marker is success.
set -e
marker="${LLMBENCH_HOOK_STATE:-/tmp/llmbench-hook-state}"
case "$1" in
  acquire)
    if [ ! -f "$marker" ]; then
      : > "$marker"
      echo "lease not ready yet" >&2
      exit 75
    fi
    echo "lease acquired"
    ;;
  release)
    rm -f "$marker"
    echo "lease released"
    ;;
  *)
    echo "usage: $0 acquire|release" >&2
    exit 2
    ;;
esac
