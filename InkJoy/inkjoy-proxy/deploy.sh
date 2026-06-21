#!/bin/bash
# deploy.sh — build and deploy inkjoy-proxy to a MIPS32LE router
#
# Usage:
#   cp .env.example .env   # set ROUTER_SSH=user@your-router
#   ./deploy.sh                           # deploy + run in spy mode
#   ./deploy.sh --replace-bin http://...  # deploy + replace play URLs
#   ./deploy.sh --stop                    # remove iptables rule + kill proxy
#
# Environment (or InkJoy/inkjoy-proxy/.env):
#   ROUTER_SSH     required — SSH target for the router (user@host)
#   BROKER_IP      optional — match iptables by broker destination (default below)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
if [[ -f "$SCRIPT_DIR/.env" ]]; then
	set -a
	# shellcheck disable=SC1091
	source "$SCRIPT_DIR/.env"
	set +a
fi

ROUTER_SSH="${ROUTER_SSH:-}"
if [[ -z "$ROUTER_SSH" ]]; then
	echo "ROUTER_SSH is required (e.g. export ROUTER_SSH=user@router or copy .env.example → .env)" >&2
	exit 1
fi

SSH_OPTS=(-o ConnectTimeout=10 -o ServerAliveInterval=5 -o ServerAliveCountMax=2)
BROKER_IP="${BROKER_IP:-13.39.148.101}"
PROXY_PORT=18830
UPSTREAM_PORT=1883
BINARY=inkjoy-proxy-mipsle
REMOTE_BIN=/tmp/inkjoy-proxy
ROUTER_HOST="${ROUTER_SSH#*@}"

router_ssh() {
	ssh "${SSH_OPTS[@]}" "$ROUTER_SSH" "$@"
}

# ── Stop mode ────────────────────────────────────────────────────────────────
if [[ "${1:-}" == "--stop" ]]; then
	echo "Removing iptables rule and killing proxy on $ROUTER_SSH ..."
	router_ssh "
		set -e
		if ! sudo -n true 2>/dev/null; then
			echo 'ERROR: passwordless sudo required on router (or run: ssh -t $ROUTER_SSH)' >&2
			exit 1
		fi
		sudo -n iptables -t nat -D PREROUTING -d $BROKER_IP -p tcp --dport $UPSTREAM_PORT \
			-j REDIRECT --to-port $PROXY_PORT 2>/dev/null || true
		sudo -n killall inkjoy-proxy 2>/dev/null || sudo -n pkill -x inkjoy-proxy 2>/dev/null || true
		echo 'Stopped.'
	"
	exit 0
fi

# ── Build ─────────────────────────────────────────────────────────────────────
echo "Building for MIPS32LE..."
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o "$BINARY" .
echo "  → $BINARY ($(du -sh "$BINARY" | cut -f1))"

# ── Deploy ────────────────────────────────────────────────────────────────────
echo "Uploading to $ROUTER_SSH:$REMOTE_BIN ..."
scp "${SSH_OPTS[@]}" "$BINARY" "$ROUTER_SSH:$REMOTE_BIN"
router_ssh "chmod +x $REMOTE_BIN"

# ── Install iptables rule ─────────────────────────────────────────────────────
echo "Installing iptables REDIRECT rule (dst=$BROKER_IP:$UPSTREAM_PORT → localhost:$PROXY_PORT)..."
router_ssh "
	sudo -n iptables -t nat -D PREROUTING -d $BROKER_IP -p tcp --dport $UPSTREAM_PORT \
		-j REDIRECT --to-port $PROXY_PORT 2>/dev/null || true
	sudo -n iptables -t nat -I PREROUTING 1 -d $BROKER_IP -p tcp --dport $UPSTREAM_PORT \
		-j REDIRECT --to-port $PROXY_PORT
	echo 'iptables rule installed:'
	sudo -n iptables -t nat -L PREROUTING --line-numbers -n | head -5
"

# ── Launch ────────────────────────────────────────────────────────────────────
echo "Launching proxy in background (logs via: nc ${ROUTER_HOST}:18831)..."
router_ssh "nohup sudo -n $REMOTE_BIN $* > /dev/null 2>&1 &"
echo "Done. Run ./deploy.sh --stop to remove iptables rule and kill proxy."
