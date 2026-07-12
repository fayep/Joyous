#!/usr/bin/env bash
# Deploy joyous-hub natively to a Mac (default: hubhost) via launchd.
#
# Usage: install.sh [options]
#   --with-inkjoy     Also install inkjoy-bridge as a launchd service
#   --with-samsung    Also install samsung-bridge as a launchd service
#   --with-nixplay    Also install nixplay-bridge as a launchd service
#
# Examples:
#   ./install.sh
#   ./install.sh --with-inkjoy
#   ./install.sh --with-inkjoy --with-samsung
#   ./install.sh --with-nixplay
set -euo pipefail

REMOTE=${REMOTE:-hubhost}
REMOTE_DIR=${REMOTE_DIR:-~/joyous-hub}
SERVER_ADDR=${SERVER_ADDR:-hubhost.local:18080}
DISCOVER_SUBNETS=${DISCOVER_SUBNETS:-192.168.50}
JOYOUS_VERSION=${JOYOUS_VERSION:-0.9.0}
WITH_INKJOY=0
WITH_SAMSUNG=0
WITH_NIXPLAY=0
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

usage() {
	sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--with-inkjoy)
		WITH_INKJOY=1
		;;
	--with-samsung)
		WITH_SAMSUNG=1
		;;
	--with-nixplay)
		WITH_NIXPLAY=1
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		usage >&2
		exit 1
		;;
	esac
	shift
done

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

BINS=("$SCRIPT_DIR/joyous-hub")
if [[ "$WITH_INKJOY" == "1" ]]; then
	echo "==> building inkjoy-bridge..."
	bash "$SCRIPT_DIR/scripts/build-binary.sh" "$SCRIPT_DIR/inkjoy-bridge" inkjoybridge
	BINS+=("$SCRIPT_DIR/inkjoy-bridge")
fi
if [[ "$WITH_SAMSUNG" == "1" ]]; then
	echo "==> building samsung-bridge..."
	bash "$SCRIPT_DIR/scripts/build-binary.sh" "$SCRIPT_DIR/samsung-bridge" samsungbridge
	BINS+=("$SCRIPT_DIR/samsung-bridge")
fi
if [[ "$WITH_NIXPLAY" == "1" ]]; then
	echo "==> building nixplay-bridge..."
	bash "$SCRIPT_DIR/scripts/build-binary.sh" "$SCRIPT_DIR/nixplay-bridge" nixplaybridge
	BINS+=("$SCRIPT_DIR/nixplay-bridge")
fi

echo "==> syncing to ${REMOTE}:${REMOTE_DIR} ..."
ssh "$REMOTE" "mkdir -p ${REMOTE_DIR}/bin ${REMOTE_DIR}/src ${REMOTE_DIR}/scripts"
rsync -av "${BINS[@]}" "$REMOTE:${REMOTE_DIR}/bin/"
rsync -av \
	--exclude joyous-hub \
	--exclude inkjoy-bridge \
	--exclude samsung-bridge \
	--exclude nixplay-bridge \
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
	WITH_INKJOY="$WITH_INKJOY" \
	WITH_SAMSUNG="$WITH_SAMSUNG" \
	WITH_NIXPLAY="$WITH_NIXPLAY" \
	NIXPLAY_ACCOUNT="${NIXPLAY_ACCOUNT:-}" \
	bash "$REMOTE_DIR/scripts/install-local.sh"

echo "==> recent hub logs:"
ssh "$REMOTE" "tail -n 8 ~/Library/Logs/joyous-hub/stderr.log 2>/dev/null || true"
if [[ "$WITH_INKJOY" == "1" ]]; then
	echo "==> recent inkjoy-bridge logs:"
	ssh "$REMOTE" "tail -n 8 ~/Library/Logs/joyous-inkjoy-bridge/stderr.log 2>/dev/null || true"
fi
if [[ "$WITH_SAMSUNG" == "1" ]]; then
	echo "==> recent samsung-bridge logs:"
	ssh "$REMOTE" "tail -n 8 ~/Library/Logs/joyous-samsung-bridge/stderr.log 2>/dev/null || true"
fi
if [[ "$WITH_NIXPLAY" == "1" ]]; then
	echo "==> recent nixplay-bridge logs:"
	ssh "$REMOTE" "tail -n 8 ~/Library/Logs/joyous-nixplay-bridge/stderr.log 2>/dev/null || true"
fi
