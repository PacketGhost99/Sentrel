#!/bin/sh
# Sentrel agent installer for Linux.
#
#   curl -fsSL https://SERVER/install/agent.sh | sudo sh -s -- --server https://SERVER --token ABCD-1234
#
# Options:
#   --server URL            Sentrel server URL (required unless --uninstall)
#   --token TOKEN           one-time enrolment token from "Add host"
#   --ca-fingerprint FP     pin the server CA (self-signed TLS mode)
#   --binary PATH           use a local binary instead of downloading
#   --name NAME             display name for the host
#   --write-jobs MODE       who may authorise jobs that change this host:
#                             server    the server's signature is enough (default)
#                             operator  jobs also need a signature from --operator-key
#                             none      read-only agent; refuses every such job
#   --read-only             shorthand for --write-jobs none
#   --operator-key B64      operator public key (from `sentrel-sign keygen-operator`);
#                           repeatable, used with --write-jobs operator
#   --docker                add the agent user to the docker group (container
#                           monitoring). Opt-in: docker group membership is
#                           root-equivalent. Without it container monitoring
#                           is unavailable.
#   --pin-sha256 B64        self-signed TLS mode: verify the server certificate by
#                           the base64 SHA-256 of its public key (SPKI) instead of a
#                           CA, for every download. Needs curl.
#   --insecure-download     DEPRECATED, not generated any more: skip TLS verification
#                           for the binary download
#                           (the CA fingerprint is still pinned at registration)
#   --uninstall             remove the agent
set -eu

SERVER=""
TOKEN=""
CAFP=""
BINARY=""
NAME=""
DOCKER=0
WRITE_JOBS=""
OPKEYS=""
INSECURE=""
PIN=""
UNINSTALL=0

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    --ca-fingerprint) CAFP="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --write-jobs) WRITE_JOBS="$2"; shift 2 ;;
    --read-only) WRITE_JOBS="none"; shift ;;
    --operator-key) OPKEYS="$OPKEYS $2"; shift 2 ;;
    --docker) DOCKER=1; shift ;;
    --pin-sha256) PIN="$2"; shift 2 ;;
    --insecure-download) INSECURE=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

BIN=/usr/local/bin/sentrel-agent
ETC=/etc/sentrel-agent
STATE=/var/lib/sentrel-agent
USER_NAME=sentrel-agent
UNIT=/etc/systemd/system/sentrel-agent.service
SUDOERS=/etc/sudoers.d/sentrel-agent

say() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root (sudo)"

if [ "$UNINSTALL" = 1 ]; then
  say "removing Sentrel agent"
  systemctl disable --now sentrel-agent 2>/dev/null || true
  # Remove firewall blocks created by the agent.
  if command -v nft >/dev/null 2>&1; then nft delete table inet sentrel 2>/dev/null || true; fi
  rm -f "$UNIT" "$SUDOERS" "$BIN" "$BIN.old"
  systemctl daemon-reload 2>/dev/null || true
  rm -rf "$ETC" "$STATE"
  userdel "$USER_NAME" 2>/dev/null || true
  say "done"
  exit 0
fi

[ -n "$SERVER" ] || die "--server is required"
[ -n "$TOKEN" ] || die "--token is required"
command -v systemctl >/dev/null 2>&1 || die "systemd is required"
case "${WRITE_JOBS:-server}" in
  server|none) [ -z "$OPKEYS" ] || die "--operator-key needs --write-jobs operator" ;;
  operator) [ -n "$OPKEYS" ] || die "--write-jobs operator needs at least one --operator-key" ;;
  *) die "--write-jobs must be server, operator or none" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

SERVER=$(printf '%s' "$SERVER" | sed 's:/*$::')
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [ -z "$BINARY" ]; then
  URL="$SERVER/download/agent/linux/$ARCH"
  say "downloading agent from $URL"
  if [ -n "$PIN" ]; then
    command -v curl >/dev/null 2>&1 || die "--pin-sha256 needs curl (wget cannot pin a public key)"
    curl -fsSL -k --pinnedpubkey "sha256//$PIN" -o "$TMP/sentrel-agent" "$URL" || die "download failed (certificate pin mismatch or server unreachable)"
  elif command -v curl >/dev/null 2>&1; then
    curl -fsSL ${INSECURE:+-k} -o "$TMP/sentrel-agent" "$URL" || die "download failed"
  elif command -v wget >/dev/null 2>&1; then
    wget -q ${INSECURE:+--no-check-certificate} -O "$TMP/sentrel-agent" "$URL" || die "download failed"
  else
    die "curl or wget is required"
  fi
  BINARY="$TMP/sentrel-agent"
fi
chmod 0755 "$BINARY"
"$BINARY" version >/dev/null 2>&1 || die "agent binary does not run on this system"

say "creating user $USER_NAME"
if ! id "$USER_NAME" >/dev/null 2>&1; then
  useradd --system --no-create-home --home-dir "$STATE" --shell /usr/sbin/nologin "$USER_NAME" 2>/dev/null \
    || useradd -r -M -d "$STATE" -s /sbin/nologin "$USER_NAME"
fi
# Read auth logs / journal for brute-force detection; docker for container stats.
for g in adm systemd-journal; do
  getent group "$g" >/dev/null 2>&1 && usermod -aG "$g" "$USER_NAME" || true
done
if [ "$DOCKER" = 1 ] && getent group docker >/dev/null 2>&1; then
  usermod -aG docker "$USER_NAME"
fi

say "installing $BIN"
if [ -f "$BIN" ]; then systemctl stop sentrel-agent 2>/dev/null || true; fi
install -o root -g root -m 0755 "$BINARY" "$BIN"
install -d -o root -g "$USER_NAME" -m 0750 "$ETC"
install -d -o "$USER_NAME" -g "$USER_NAME" -m 0700 "$STATE"

# Validate the sudo rules before registering, so a failure here does not
# consume the one-time token. Only `sentrel-agent priv ...` may run as root;
# the root-owned helper validates arguments and job signatures itself.
say "checking sudo rules (privileged helper only)"
if [ -d /etc/sudoers.d ]; then
  cat > "$TMP/sudoers" <<EOF
# Sentrel agent: the agent runs unprivileged and may only invoke its own
# root-owned helper, which validates arguments and job signatures itself.
$USER_NAME ALL=(root) NOPASSWD: $BIN priv *
EOF
  if command -v visudo >/dev/null 2>&1 && ! visudo -cf "$TMP/sudoers" >/dev/null; then
    die "generated sudoers file failed validation"
  fi
else
  say "warning: /etc/sudoers.d missing; upgrades and IP blocking will not work"
fi

say "registering with $SERVER"
set -- register --server "$SERVER" --token "$TOKEN" --config "$ETC/agent.yaml" --state-dir "$STATE"
[ -n "$CAFP" ] && set -- "$@" --ca-fingerprint "$CAFP"
[ -n "$NAME" ] && set -- "$@" --name "$NAME"
[ -n "$WRITE_JOBS" ] && set -- "$@" --write-jobs "$WRITE_JOBS"
for k in $OPKEYS; do set -- "$@" --operator-key "$k"; done
"$BIN" "$@"
chown "$USER_NAME:$USER_NAME" "$STATE"/* 2>/dev/null || true
chmod 0640 "$ETC/agent.yaml"; chown root:"$USER_NAME" "$ETC/agent.yaml"
# Root-owned copy of the job signing key: the privileged helper trusts only this.
chmod 0644 "$ETC/job.pub"; chown root:root "$ETC/job.pub"

if [ -f "$TMP/sudoers" ]; then
  install -o root -g root -m 0440 "$TMP/sudoers" "$SUDOERS"
fi

say "installing systemd unit"
cat > "$UNIT" <<EOF
[Unit]
Description=Sentrel monitoring agent
Documentation=https://github.com/notspark1999/Sentrel
After=network-online.target
Wants=network-online.target

[Service]
User=$USER_NAME
Group=$USER_NAME
ExecStart=$BIN run --config $ETC/agent.yaml
Restart=always
RestartSec=5
# Cap the agent so it can never hurt the host. Package upgrades started by the
# privileged helper run in their own transient scope, outside these limits,
# and survive agent restarts. (No ProtectSystem: sudo children inherit the
# mount namespace and package managers must write /usr.)
MemoryMax=192M
CPUQuota=25%
Nice=10

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now sentrel-agent >/dev/null 2>&1
sleep 2
if systemctl is-active --quiet sentrel-agent; then
  say "Sentrel agent is running. It should appear in the web UI within a minute."
else
  journalctl -u sentrel-agent -n 20 --no-pager || true
  die "agent failed to start"
fi
