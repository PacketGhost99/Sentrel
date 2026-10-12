#!/bin/sh
case "$1" in
  remove|purge|0)
    rm -f /etc/sudoers.d/@AGENT_BIN@
    # Remove firewall blocks created by the agent.
    if command -v nft >/dev/null 2>&1; then nft delete table inet @FW_TABLE@ 2>/dev/null || true; fi
    if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload >/dev/null 2>&1 || true; fi
    ;;
esac
# Config, state and the system user are kept (remove them by hand if wanted).
exit 0
