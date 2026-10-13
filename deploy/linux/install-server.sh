#!/bin/sh
# Sentrel server installer for Linux (systemd).
#
#   curl -fsSL https://example.com/install-server.sh | sudo sh -s -- --url https://.../sentrel-server-linux-amd64
#   sudo sh install-server.sh --binary ./sentrel-server
#
# Options:
#   --binary PATH     install a local binary
#   --url URL         download the binary from URL
#   --sha256 HEX      verify the downloaded binary
#   --listen ADDR     listen address (default 0.0.0.0:8080)
#   --data-dir DIR    data directory (default /var/lib/sentrel)
#   --uninstall       stop and remove (keeps the data directory)
#   --updater-units-only  (re)install only the one-click updater units
#
# Re-running the script on an existing install updates the server unit and
# installs the updater units (sentrel-updater.path/.service) that let the
# server apply a signed update from Settings -> Updates.
set -eu

BINARY=""
URL=""
SHA=""
LISTEN="0.0.0.0:8080"
DATA=/var/lib/sentrel
UNINSTALL=0
UNITS_ONLY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --url) URL="$2"; shift 2 ;;
    --sha256) SHA="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --data-dir) DATA="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    --updater-units-only) UNITS_ONLY=1; shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

BIN=/usr/local/bin/sentrel-server
UNIT=/etc/systemd/system/sentrel.service
UPD_PATH=/etc/systemd/system/sentrel-updater.path
UPD_SVC=/etc/systemd/system/sentrel-updater.service
say() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root (sudo)"
command -v systemctl >/dev/null 2>&1 || die "systemd is required (or use the Docker image)"

# The root updater: sentrel-updater.path watches <data>/update/request.json,
# which the (unprivileged) server writes after it has downloaded and verified a
# release. sentrel-updater.service then runs the installed binary, which
# re-verifies the Ed25519 signature with its own embedded key before swapping
# itself, and rolls back if the new version does not become healthy.
install_updater_units() {
  install -d -o sentrel -g sentrel -m 0750 "$DATA/update"
  cat > "$UPD_SVC" <<EOF
[Unit]
Description=Sentrel server updater (applies a staged, signed update)
ConditionPathExists=$DATA/update/request.json

[Service]
Type=oneshot
User=root
ExecStart=$BIN self-update apply --data-dir $DATA
TimeoutStartSec=420
ProtectSystem=strict
ReadWritePaths=/usr/local/bin $DATA
ProtectHome=yes
PrivateTmp=yes
EOF
  cat > "$UPD_PATH" <<EOF
[Unit]
Description=Watch for a staged Sentrel server update

[Path]
PathExists=$DATA/update/request.json
PathChanged=$DATA/update/request.json
Unit=sentrel-updater.service

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now sentrel-updater.path >/dev/null 2>&1
}

if [ "$UNITS_ONLY" = 1 ]; then
  [ -x "$BIN" ] || die "$BIN is not installed"
  id sentrel >/dev/null 2>&1 || die "user sentrel does not exist; run the full installer"
  install_updater_units
  say "updater units installed"
  exit 0
fi

if [ "$UNINSTALL" = 1 ]; then
  systemctl disable --now sentrel-updater.path 2>/dev/null || true
  systemctl disable --now sentrel 2>/dev/null || true
  rm -f "$UNIT" "$UPD_PATH" "$UPD_SVC" "$BIN" "$BIN.old"
  systemctl daemon-reload
  say "removed; data kept in $DATA"
  exit 0
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
if [ -z "$BINARY" ]; then
  [ -n "$URL" ] || die "--binary or --url is required"
  say "downloading $URL"
  curl -fsSL -o "$TMP/sentrel-server" "$URL" || die "download failed"
  BINARY="$TMP/sentrel-server"
fi
if [ -n "$SHA" ]; then
  echo "$SHA  $BINARY" | sha256sum -c - >/dev/null || die "checksum mismatch"
fi
chmod 0755 "$BINARY"
"$BINARY" version >/dev/null || die "binary does not run on this system"

say "creating user sentrel"
id sentrel >/dev/null 2>&1 || useradd --system --no-create-home --home-dir "$DATA" --shell /usr/sbin/nologin sentrel
install -d -o sentrel -g sentrel -m 0750 "$DATA"

if systemctl is-active --quiet sentrel 2>/dev/null; then systemctl stop sentrel; fi
install -o root -g root -m 0755 "$BINARY" "$BIN"

# ICMP ping checks need raw sockets; ports 80/443 are needed for Let's Encrypt.
if command -v setcap >/dev/null 2>&1; then
  setcap 'cap_net_raw,cap_net_bind_service+ep' "$BIN" || say "warning: setcap failed; ping checks need cap_net_raw"
else
  say "warning: setcap not found (install libcap2-bin); using the systemd capability grant instead"
fi

cat > "$UNIT" <<EOF
[Unit]
Description=Sentrel monitoring server
After=network-online.target
Wants=network-online.target

[Service]
User=sentrel
Group=sentrel
ExecStart=$BIN serve --data-dir $DATA --listen $LISTEN
Restart=always
RestartSec=3
# CAP_NET_RAW: privileged ICMP for ping checks (pro-bing falls back to UDP
# ping if it is missing). CAP_NET_BIND_SERVICE: ports 80/443 for TLS and ACME.
AmbientCapabilities=CAP_NET_RAW CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_RAW CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=$DATA
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now sentrel >/dev/null 2>&1
install_updater_units

say "waiting for the server to start"
PORT=${LISTEN##*:}
i=0
while [ $i -lt 30 ]; do
  "$BIN" healthcheck --url "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break
  sleep 1; i=$((i+1))
done
systemctl is-active --quiet sentrel || { journalctl -u sentrel -n 30 --no-pager; die "server failed to start"; }

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
echo
say "Sentrel is running: http://${IP:-localhost}:$PORT/"
if [ -f "$DATA/setup-token" ]; then
  say "one-time setup token: $(cat "$DATA/setup-token")"
fi
say "logs: journalctl -u sentrel -f    backups: $BIN backup --data-dir $DATA"
