#!/bin/bash
# deploy.sh — build and deploy inkjoy-proxy to EdgeRouter X
#
# Usage:
#   ./deploy.sh                           # deploy + run in spy mode
#   ./deploy.sh --replace-bin http://...  # deploy + replace play URLs
#   ./deploy.sh --stop                    # remove iptables rule + kill proxy
#
# Assumptions:
#   - Router is at 192.168.1.1, ssh as ubnt
#   - Matches by broker destination IP, not interface — works with hardware-switched
#     LAN ports (switch0/br0) where per-port iptables rules would be invisible

set -euo pipefail

ROUTER_SSH="ubnt@192.168.1.1"
BROKER_IP="13.39.148.101"   # match by destination — works regardless of LAN topology
PROXY_PORT=18830
UPSTREAM_PORT=1883
BINARY=inkjoy-proxy-mipsle
REMOTE_BIN=/tmp/inkjoy-proxy

# ── Stop mode ────────────────────────────────────────────────────────────────
if [[ "${1:-}" == "--stop" ]]; then
    echo "Removing iptables rule and killing proxy..."
    ssh "$ROUTER_SSH" "
        sudo iptables -t nat -D PREROUTING -d $BROKER_IP -p tcp --dport $UPSTREAM_PORT \
            -j REDIRECT --to-port $PROXY_PORT 2>/dev/null || true
        sudo pkill -f inkjoy-proxy 2>/dev/null || true
        echo 'Stopped.'
    "
    exit 0
fi

# ── Build ─────────────────────────────────────────────────────────────────────
echo "Building for MIPS32LE (EdgeRouter X)..."
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o "$BINARY" .
echo "  → $BINARY ($(du -sh "$BINARY" | cut -f1))"

# ── Deploy ────────────────────────────────────────────────────────────────────
echo "Uploading to $ROUTER_SSH:$REMOTE_BIN ..."
scp "$BINARY" "$ROUTER_SSH:$REMOTE_BIN"
ssh "$ROUTER_SSH" "chmod +x $REMOTE_BIN"

# ── Install iptables rule ─────────────────────────────────────────────────────
echo "Installing iptables REDIRECT rule (dst=$BROKER_IP:$UPSTREAM_PORT → localhost:$PROXY_PORT)..."
ssh "$ROUTER_SSH" "
    # Remove any stale rule first
    sudo iptables -t nat -D PREROUTING -d $BROKER_IP -p tcp --dport $UPSTREAM_PORT \
        -j REDIRECT --to-port $PROXY_PORT 2>/dev/null || true
    # Insert at position 1 so we run before UPnP/VyOS DNAT hooks
    sudo iptables -t nat -I PREROUTING 1 -d $BROKER_IP -p tcp --dport $UPSTREAM_PORT \
        -j REDIRECT --to-port $PROXY_PORT
    echo 'iptables rule installed:'
    sudo iptables -t nat -L PREROUTING --line-numbers -n | head -5
"

# ── Launch ────────────────────────────────────────────────────────────────────
echo "Launching proxy in background (logs via: nc 192.168.1.1 18831)..."
ssh "$ROUTER_SSH" "nohup sudo $REMOTE_BIN $* > /dev/null 2>&1 &"
echo "Done. Run ./deploy.sh --stop to remove iptables rule and kill proxy."
