#!/usr/bin/env bash
# Run on the Mac mini console (not over SSH) to trigger Local Network permission for Joyous Hub.
set -euo pipefail
INSTALL_ROOT="${INSTALL_ROOT:-$HOME/joyous-hub}"
BIN="$INSTALL_ROOT/JoyousHub.app/Contents/MacOS/joyous-hub"
IP="${1:-192.168.1.108}"

if [[ ! -x "$BIN" ]]; then
	echo "missing $BIN — run scripts/install-local.sh first" >&2
	exit 1
fi

echo "Probing $IP:1515 via Joyous Hub (approve the macOS dialog if shown)..."
exec "$BIN" --probe-network="$IP"
