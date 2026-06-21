#!/usr/bin/env bash
# Run joyous-hub in the foreground for debugging (SSH-friendly console logs).
# Stops the launchd service first so only one instance listens on the ports.
set -euo pipefail

INSTALL_ROOT="${INSTALL_ROOT:-$HOME/joyous-hub}"
DATA_DIR="${DATA_DIR:-/Volumes/tank/Media/photoframe}"
HTTP_PORT="${HTTP_PORT:-18080}"
MQTT_PORT="${MQTT_PORT:-11883}"
SERVER_ADDR="${SERVER_ADDR:-$(hostname -s | tr '[:upper:]' '[:lower:]').local:${HTTP_PORT}}"
DISCOVER_SUBNETS="${DISCOVER_SUBNETS:-192.168.50}"
LABEL="com.joyous.hub"
APP="$INSTALL_ROOT/JoyousHub.app"
BIN="$APP/Contents/MacOS/joyous-hub"
USER_ID="$(id -u)"
TARGET="gui/${USER_ID}/${LABEL}"

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "run-debug.sh is macOS-only" >&2
	exit 1
fi
if [[ ! -x "$BIN" ]]; then
	echo "missing $BIN — run scripts/install-local.sh first" >&2
	exit 1
fi

echo "==> stopping launchd service (if loaded)..."
launchctl bootout "$TARGET" 2>/dev/null || true

if pgrep -f "${INSTALL_ROOT}.*joyous-hub.*:${HTTP_PORT#:}" >/dev/null 2>&1; then
	echo "==> stopping existing hub on :${HTTP_PORT}..."
	pkill -f "${INSTALL_ROOT}.*joyous-hub.*:${HTTP_PORT#:}" 2>/dev/null || true
	sleep 1
fi

echo "==> starting hub in foreground (logs → this terminal)"
echo "    http://${SERVER_ADDR}"
echo "    Ctrl+C to stop"
echo ""

exec "$BIN" \
	--data-dir="$DATA_DIR" \
	--listen-mqtt=":${MQTT_PORT}" \
	--listen-http=":${HTTP_PORT}" \
	--server-addr="$SERVER_ADDR" \
	--discover-subnets="$DISCOVER_SUBNETS"
