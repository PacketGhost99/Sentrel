#!/bin/sh
case "$1" in
  remove|0)
    if command -v systemctl >/dev/null 2>&1; then
      systemctl disable --now @SERVER_UNIT@ >/dev/null 2>&1 || true
    fi
    ;;
esac
exit 0
