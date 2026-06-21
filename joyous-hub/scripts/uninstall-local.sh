#!/usr/bin/env bash
set -euo pipefail

LABEL="com.joyous.hub"
PLIST="$HOME/Library/LaunchAgents/${LABEL}.plist"
USER_ID="$(id -u)"
TARGET="gui/${USER_ID}/${LABEL}"

launchctl bootout "$TARGET" 2>/dev/null || true
rm -f "$PLIST"
echo "Removed launchd service ${LABEL}"
