#!/usr/bin/env bash
# Deploy joyous-hub natively to a Mac (default: hubhost) via launchd.
set -euo pipefail

REMOTE=${REMOTE:-hubhost}
REMOTE_DIR=${REMOTE_DIR:-~/joyous-hub}
SERVER_ADDR=${SERVER_ADDR:-hubhost.local:18080}
DISCOVER_SUBNETS=${DISCOVER_SUBNETS:-192.168.50}
JOYOUS_VERSION=${JOYOUS_VERSION:-0.9.0}
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if ! command -v brew >/dev/null; then
	echo "Homebrew is required locally for libheif: https://brew.sh" >&2
	exit 1
fi
if ! brew list libheif &>/dev/null; then
	echo "==> installing libheif locally..."
	brew install libheif
fi

echo "==> building darwin/arm64 binary (version ${JOYOUS_VERSION})..."
bash "$SCRIPT_DIR/scripts/build-binary.sh" "$SCRIPT_DIR/joyous-hub"

echo "==> syncing to ${REMOTE}:${REMOTE_DIR} ..."
ssh "$REMOTE" "mkdir -p ${REMOTE_DIR}/bin ${REMOTE_DIR}/src ${REMOTE_DIR}/scripts"
rsync -av \
	"$SCRIPT_DIR/joyous-hub" \
	"$REMOTE:${REMOTE_DIR}/bin/"
rsync -av \
	--exclude joyous-hub \
	--exclude data \
	--exclude .git \
	"$SCRIPT_DIR/" \
	"$REMOTE:${REMOTE_DIR}/src/"
rsync -av \
	"$SCRIPT_DIR/entitlements.plist" \
	"$REMOTE:${REMOTE_DIR}/"
rsync -av \
	"$SCRIPT_DIR/scripts/build-binary.sh" \
	"$SCRIPT_DIR/scripts/install-local.sh" \
	"$SCRIPT_DIR/scripts/uninstall-local.sh" \
	"$SCRIPT_DIR/scripts/grant-local-network.sh" \
	"$SCRIPT_DIR/scripts/run-debug.sh" \
	"$REMOTE:${REMOTE_DIR}/scripts/"

echo "==> installing native service on ${REMOTE} ..."
ssh "$REMOTE" \
	INSTALL_ROOT="$REMOTE_DIR" \
	SKIP_BUILD=1 \
	SERVER_ADDR="$SERVER_ADDR" \
	DISCOVER_SUBNETS="$DISCOVER_SUBNETS" \
	JOYOUS_VERSION="$JOYOUS_VERSION" \
	bash "$REMOTE_DIR/scripts/install-local.sh"

echo "==> recent logs:"
ssh "$REMOTE" "tail -n 8 ~/Library/Logs/joyous-hub/stderr.log 2>/dev/null || true"
