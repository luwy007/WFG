#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENGINE_DIR="$ROOT_DIR/engine"
MACAPP_DIR="$ROOT_DIR/MacApp"

CONFIGURATION="${CONFIGURATION:-Release}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-$MACAPP_DIR/.derived}"
FINAL_APP_DIR="${FINAL_APP_DIR:-$MACAPP_DIR}"
GOCACHE="${GOCACHE:-$ENGINE_DIR/.cache/go-build}"
SCHEME="${SCHEME:-WFG}"
PROJECT="$MACAPP_DIR/WFG.xcodeproj"
ENGINE_BIN="$ENGINE_DIR/wfg-engine"

echo "==> Building engine"
mkdir -p "$GOCACHE"
(
  cd "$ENGINE_DIR"
  GOCACHE="$GOCACHE" go build -trimpath -o "$ENGINE_BIN" .
)

if [[ ! -x "$ENGINE_BIN" ]]; then
  echo "Engine binary was not created or is not executable: $ENGINE_BIN" >&2
  exit 1
fi

echo "==> Building Mac app ($CONFIGURATION)"
xcodebuild \
  -project "$PROJECT" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  build

APP_PATH="$DERIVED_DATA_PATH/Build/Products/$CONFIGURATION/WFG.app"
BUNDLED_ENGINE="$APP_PATH/Contents/Resources/wfg-engine"
FINAL_APP_PATH="$FINAL_APP_DIR/WFG.app"

if [[ ! -x "$BUNDLED_ENGINE" ]]; then
  echo "Bundled engine is missing or not executable: $BUNDLED_ENGINE" >&2
  exit 1
fi

echo "==> Copying final app"
mkdir -p "$FINAL_APP_DIR"
rm -rf "$FINAL_APP_PATH"
ditto "$APP_PATH" "$FINAL_APP_PATH"

echo "==> Build complete"
echo "App: $FINAL_APP_PATH"
echo "Derived app: $APP_PATH"
echo "Engine: $FINAL_APP_PATH/Contents/Resources/wfg-engine"
