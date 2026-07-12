#!/usr/bin/env bash
set -euo pipefail

USER_ID="$(id -u)"
DOMAIN="gui/${USER_ID}"

remove_service() {
	local label="$1"
	local plist="$HOME/Library/LaunchAgents/${label}.plist"
	local target="${DOMAIN}/${label}"

	launchctl bootout "$target" 2>/dev/null || true
	rm -f "$plist"
	echo "Removed launchd service ${label}"
}

remove_service "com.joyous.hub"
remove_service "com.joyous.inkjoy-bridge"
remove_service "com.joyous.samsung-bridge"
remove_service "com.joyous.nixplay-bridge"
