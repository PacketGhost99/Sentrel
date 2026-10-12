#!/bin/sh
# Runs after install and upgrade (deb: configure, rpm: $1 = 1 or 2).
set -e
ETC=@AGENT_ETC@
STATE=@AGENT_STATE@
U=@AGENT_USER@
SVC=@AGENT_BIN@

if ! id "$U" >/dev/null 2>&1; then
  useradd --system --no-create-home --home-dir "$STATE" --shell /usr/sbin/nologin "$U" 2>/dev/null \
    || useradd -r -M -d "$STATE" -s /sbin/nologin "$U"
fi
# Read auth logs / journal for brute-force detection. The docker group is
# never added automatically: it is root-equivalent (see the docs).
for g in adm systemd-journal; do
  if getent group "$g" >/dev/null 2>&1; then usermod -aG "$g" "$U" || true; fi
done
install -d -o root -g "$U" -m 0750 "$ETC"
install -d -o "$U" -g "$U" -m 0700 "$STATE"

# sudoers: validate before installing so a bad file can never break sudo.
if [ -d /etc/sudoers.d ]; then
  if command -v visudo >/dev/null 2>&1 && ! visudo -cf @AGENT_SHARE@/sudoers >/dev/null 2>&1; then
    echo "warning: sudoers file failed validation; privileged jobs disabled" >&2
  else
    install -o root -g root -m 0440 @AGENT_SHARE@/sudoers "/etc/sudoers.d/$SVC"
  fi
else
  echo "warning: /etc/sudoers.d missing (install sudo); upgrades and IP blocking will not work" >&2
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  # Upgrade: restart a running agent on the new binary.
  if systemctl is-active --quiet "$SVC"; then systemctl restart "$SVC" || true; fi
fi

if [ ! -f "$ETC/agent.yaml" ]; then
  cat <<MSG

@PRODUCT_TITLE@ agent installed but NOT registered. Register it with the one-time
token from "Add host" in the web UI, then start it:

  sudo $SVC register --server https://YOUR-SERVER --token ABCD-1234
  sudo systemctl enable --now $SVC

Add --ca-fingerprint FP if the server uses a self-signed certificate.

MSG
fi
exit 0
