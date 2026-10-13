#!/usr/bin/env bash
# Development: sync sources to the build host, build, and restart the
# systemd-installed server there.
#
#   scripts/dev-deploy.sh [--version X]
#
# The build host's checkout directory (~/heartbeat) keeps its old name; it is
# only a path. The server itself is installed as sentrel-server / sentrel.service.
set -euo pipefail
cd "$(dirname "$0")/.."
# Default: the next patch after the latest release tag, with -dev, so a dev
# install (0.9.1-dev) is offered the real release (0.9.1) but not an older one.
LATEST_TAG=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null | sed 's/^v//' || true)
if [ -n "$LATEST_TAG" ]; then
  DEFAULT_VERSION="${LATEST_TAG%.*}.$(( ${LATEST_TAG##*.} + 1 ))-dev"
else
  DEFAULT_VERSION="0.0.1-dev"
fi
VERSION="${1:-$DEFAULT_VERSION}"
[ "${1:-}" = "--version" ] && VERSION="$2"
scripts/sync.sh push
# ~/bin holds a prebuilt nfpm so the agent .deb/.rpm are built and embedded.
scripts/sync.sh run "PATH=\$PATH:\$HOME/bin scripts/build.sh --version $VERSION >/tmp/hb-build.log 2>&1 || { tail -30 /tmp/hb-build.log; exit 1; }"

# One-time migration from the pre-rename install (heartbeat.service,
# /var/lib/heartbeat, user heartbeat). A no-op once heartbeat.service is gone.
scripts/sync.sh run "bash -s" <<'MIGRATE'
set -e
if [ -f /etc/systemd/system/heartbeat.service ]; then
  echo "migrating heartbeat -> sentrel"
  sudo systemctl stop heartbeat 2>/dev/null || true
  sudo systemctl disable heartbeat 2>/dev/null || true
  id sentrel >/dev/null 2>&1 || sudo useradd --system --no-create-home --home-dir /var/lib/sentrel --shell /usr/sbin/nologin sentrel
  if [ -d /var/lib/heartbeat ] && [ ! -e /var/lib/sentrel ]; then sudo mv /var/lib/heartbeat /var/lib/sentrel; fi
  sudo sh -c 'cd /var/lib/sentrel && for f in heartbeat.db*; do [ -e "$f" ] && mv "$f" "sentrel.db${f#heartbeat.db}"; done; true'
  sudo chown -R sentrel:sentrel /var/lib/sentrel
  sed -e 's/heartbeat/sentrel/g' -e 's/Heartbeat/Sentrel/g' /etc/systemd/system/heartbeat.service | sudo tee /etc/systemd/system/sentrel.service >/dev/null
  sudo rm -f /etc/systemd/system/heartbeat.service /usr/local/bin/heartbeat-server
  sudo systemctl daemon-reload
  sudo systemctl enable sentrel >/dev/null 2>&1
fi
MIGRATE

scripts/sync.sh run "sudo install -m 0755 dist/sentrel-server-linux-amd64 /usr/local/bin/sentrel-server
  sudo setcap 'cap_net_raw,cap_net_bind_service+ep' /usr/local/bin/sentrel-server
  sudo systemctl restart sentrel && sleep 2 && systemctl is-active sentrel && grep -E 'agents|server' /tmp/hb-build.log | tail -5"
# One-click updater units (sentrel-updater.path/.service).
scripts/sync.sh run "sudo sh deploy/linux/install-server.sh --updater-units-only --data-dir /var/lib/sentrel && systemctl is-enabled sentrel-updater.path"
