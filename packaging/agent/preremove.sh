#!/bin/sh
# deb: "remove"/"upgrade"/...; rpm: 0 = erase, 1 = upgrade. Only act on removal.
case "$1" in
  remove|0)
    if command -v systemctl >/dev/null 2>&1; then
      systemctl disable --now @AGENT_BIN@ >/dev/null 2>&1 || true
    fi
    ;;
esac
exit 0
