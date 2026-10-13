#!/usr/bin/env bash
# Development helper: mirror the working tree to a Linux build host over SSH
# and pull generated files back.
#
#   scripts/sync.sh push            copy sources to the build host
#   scripts/sync.sh pull <paths..>  copy paths back from the build host
#   scripts/sync.sh run <cmd>       run a command in the remote checkout
#
# HB_BUILD_HOST (default HeartbeatSandbox) and HB_REMOTE_DIR (default heartbeat)
# override the target.
set -euo pipefail

HOST="${HB_BUILD_HOST:-HeartbeatSandbox}"
DIR="${HB_REMOTE_DIR:-heartbeat}"
cd "$(dirname "$0")/.."

case "${1:-}" in
  push)
    tar --exclude=./web/node_modules --exclude=./web/dist --exclude=./dist \
        --exclude=./.git --exclude='*.exe' -czf - . |
      ssh "$HOST" "mkdir -p ~/$DIR && tar xzf - -C ~/$DIR --warning=no-timestamp"
    ;;
  pull)
    shift
    ssh "$HOST" "cd ~/$DIR && tar czf - $*" | tar xzf -
    ;;
  run)
    shift
    ssh "$HOST" "export PATH=\$PATH:\$HOME/go/bin; cd ~/$DIR && $*"
    ;;
  *)
    echo "usage: $0 push | pull <paths..> | run <cmd>" >&2
    exit 2
    ;;
esac
