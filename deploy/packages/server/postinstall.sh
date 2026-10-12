#!/bin/sh
set -e
BIN=@SERVER_PATH@
DATA=@SERVER_DATA@
U=@SERVER_USER@
UNIT=@SERVER_UNIT@

id "$U" >/dev/null 2>&1 || useradd --system --no-create-home --home-dir "$DATA" --shell /usr/sbin/nologin "$U" 2>/dev/null \
  || useradd -r -M -d "$DATA" -s /sbin/nologin "$U"
install -d -o "$U" -g "$U" -m 0750 "$DATA"

# ICMP ping checks need raw sockets; ports 80/443 are needed for Let's Encrypt.
if command -v setcap >/dev/null 2>&1; then
  setcap 'cap_net_raw,cap_net_bind_service+ep' "$BIN" || echo "warning: setcap failed; the systemd capability grant is used instead" >&2
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  if systemctl is-active --quiet "$UNIT"; then systemctl restart "$UNIT" || true; fi
fi

if ! systemctl is-active --quiet "$UNIT" 2>/dev/null; then
  cat <<MSG

@PRODUCT_TITLE@ server installed. Start it with:

  sudo systemctl enable --now $UNIT

then open http://HOST:8080/ and read the one-time setup token with:

  sudo cat $DATA/setup-token

MSG
fi
exit 0
