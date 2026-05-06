#!/usr/bin/env bash
set -euo pipefail

SERVICE_ID="com.wfg.engine"
INSTALL_BIN="/Library/PrivilegedHelperTools/wfg-engine"
PLIST_PATH="/Library/LaunchDaemons/$SERVICE_ID.plist"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  exec sudo "$0" "$@"
fi

launchctl bootout system "$PLIST_PATH" >/dev/null 2>&1 || true
rm -f "$PLIST_PATH" "$INSTALL_BIN"

echo "Uninstalled $SERVICE_ID"
