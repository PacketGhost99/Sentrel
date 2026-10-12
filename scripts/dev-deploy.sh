#!/usr/bin/env bash
# Development: sync sources to the build host, build, and restart the
# systemd-installed server there.
#
#   scripts/dev-deploy.sh [--version X]
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-0.9.0-dev}"
[ "${1:-}" = "--version" ] && VERSION="$2"
scripts/sync.sh push
scripts/sync.sh run "scripts/build.sh --version $VERSION >/tmp/hb-build.log 2>&1 || { tail -30 /tmp/hb-build.log; exit 1; }
  sudo install -m 0755 dist/heartbeat-server-linux-amd64 /usr/local/bin/heartbeat-server
  sudo setcap 'cap_net_raw,cap_net_bind_service+ep' /usr/local/bin/heartbeat-server
  sudo systemctl restart heartbeat && sleep 2 && systemctl is-active heartbeat && grep -E 'agents|server' /tmp/hb-build.log | tail -5"
