#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENGINE_DIR="$ROOT_DIR/engine"
ENGINE_SRC="$ENGINE_DIR/wfg-engine"
SERVICE_ID="com.wfg.engine"
INSTALL_BIN="/Library/PrivilegedHelperTools/wfg-engine"
PLIST_PATH="/Library/LaunchDaemons/$SERVICE_ID.plist"
LOG_DIR="/Library/Logs/WFG"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  export WFG_INSTALL_USER="$(id -un)"
  export WFG_INSTALL_HOME="$HOME"
  exec sudo -E "$0" "$@"
fi

INSTALL_USER="${WFG_INSTALL_USER:-${SUDO_USER:-$(logname 2>/dev/null || echo "")}}"
INSTALL_HOME="${WFG_INSTALL_HOME:-}"
if [[ -z "$INSTALL_HOME" && -n "$INSTALL_USER" ]]; then
  INSTALL_HOME="$(dscl . -read "/Users/$INSTALL_USER" NFSHomeDirectory | awk '{print $2}')"
fi
if [[ -z "$INSTALL_HOME" || ! -d "$INSTALL_HOME" ]]; then
  echo "Cannot determine target user home. Set WFG_INSTALL_HOME=/Users/<name>." >&2
  exit 1
fi

DATA_DIR="${WFG_DATA_DIR:-$INSTALL_HOME/.wfg}"

if [[ ! -x "$ENGINE_SRC" ]]; then
  echo "Engine binary not found, building it first..."
  mkdir -p "$ENGINE_DIR/.cache/go-build"
  (
    cd "$ENGINE_DIR"
    GOCACHE="$ENGINE_DIR/.cache/go-build" go build -trimpath -o "$ENGINE_SRC" .
  )
fi

echo "Installing $SERVICE_ID"
mkdir -p "$(dirname "$INSTALL_BIN")" "$LOG_DIR" "$DATA_DIR"
cp "$ENGINE_SRC" "$INSTALL_BIN"
chown root:wheel "$INSTALL_BIN"
chmod 0755 "$INSTALL_BIN"

cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$SERVICE_ID</string>
  <key>ProgramArguments</key>
  <array>
    <string>$INSTALL_BIN</string>
    <string>-port</string>
    <string>19090</string>
    <string>-data-dir</string>
    <string>$DATA_DIR</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>WFG_DATA_DIR</key>
    <string>$DATA_DIR</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$LOG_DIR/engine.log</string>
  <key>StandardErrorPath</key>
  <string>$LOG_DIR/engine.log</string>
</dict>
</plist>
PLIST

chown root:wheel "$PLIST_PATH"
chmod 0644 "$PLIST_PATH"

launchctl bootout system "$PLIST_PATH" >/dev/null 2>&1 || true
launchctl bootstrap system "$PLIST_PATH"
launchctl kickstart -k "system/$SERVICE_ID"

echo "Installed and started $SERVICE_ID"
echo "Data dir: $DATA_DIR"
echo "Log: $LOG_DIR/engine.log"
