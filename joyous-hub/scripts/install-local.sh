#!/usr/bin/env bash
# Install or upgrade joyous-hub as a native launchd service on macOS (Apple silicon).
# Runs inside JoyousHub.app so macOS grants Local Network access for discovery.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_ROOT="${INSTALL_ROOT:-$HOME/joyous-hub}"
SRC_DIR="${SRC_DIR:-$INSTALL_ROOT/src}"
DATA_DIR="${DATA_DIR:-/Volumes/tank/Media/photoframe}"
HTTP_PORT="${HTTP_PORT:-18080}"
MQTT_PORT="${MQTT_PORT:-11883}"
SERVER_ADDR="${SERVER_ADDR:-$(hostname -s | tr '[:upper:]' '[:lower:]').local:${HTTP_PORT}}"
DISCOVER_SUBNETS="${DISCOVER_SUBNETS:-192.168.50}"
LABEL="com.joyous.hub"
APP_BUNDLE="JoyousHub.app"
APP_DISPLAY_NAME="Joyous Hub"
APP="$INSTALL_ROOT/${APP_BUNDLE}"
PLIST="$HOME/Library/LaunchAgents/${LABEL}.plist"
LOG_DIR="$HOME/Library/Logs/joyous-hub"
CONFIG_DIR="$HOME/Library/Application Support/Joyous"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
STAGING_BIN="$INSTALL_ROOT/bin/joyous-hub"
ENTITLEMENTS="${ENTITLEMENTS:-$INSTALL_ROOT/entitlements.plist}"
USER_ID="$(id -u)"
DOMAIN="gui/${USER_ID}"
TARGET="${DOMAIN}/${LABEL}"

if [[ ! -f "$ENTITLEMENTS" ]]; then
	ENTITLEMENTS="$SCRIPT_DIR/../entitlements.plist"
fi

stop_service() {
	echo "==> stopping any existing ${LABEL} service ..."
	launchctl bootout "$TARGET" 2>/dev/null || true
	launchctl bootout "$DOMAIN" "$PLIST" 2>/dev/null || true
	launchctl unload -w "$PLIST" 2>/dev/null || true
	if pgrep -f "${INSTALL_ROOT}.*joyous-hub" >/dev/null 2>&1; then
		pkill -f "${INSTALL_ROOT}.*joyous-hub" 2>/dev/null || true
		sleep 1
	fi
}

load_service() {
	echo "==> loading launchd service..."
	if ! plutil -lint "$PLIST" >/dev/null; then
		echo "invalid launchd plist:" >&2
		plutil -lint "$PLIST" >&2
		exit 1
	fi
	if [[ ! -x "$BIN" ]]; then
		echo "missing executable: $BIN" >&2
		exit 1
	fi
	if [[ ! -d "$APP" ]]; then
		echo "missing app bundle: $APP" >&2
		exit 1
	fi

	stop_service

	if launchctl bootstrap "$DOMAIN" "$PLIST" 2>/dev/null; then
		:
	elif launchctl print "$TARGET" >/dev/null 2>&1; then
		echo "==> service already registered; reloading ..."
		stop_service
		if ! launchctl bootstrap "$DOMAIN" "$PLIST"; then
			echo "launchctl bootstrap failed; try: launchctl bootout ${TARGET}" >&2
			exit 1
		fi
	else
		echo "launchctl bootstrap failed" >&2
		launchctl bootstrap "$DOMAIN" "$PLIST"
		exit 1
	fi

	launchctl enable "$TARGET" 2>/dev/null || true
	launchctl kickstart -k "$TARGET"
}

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "install-local.sh is macOS-only" >&2
	exit 1
fi

if [[ -x /opt/homebrew/bin/brew ]]; then
	eval "$(/opt/homebrew/bin/brew shellenv)"
elif [[ -x /usr/local/bin/brew ]]; then
	eval "$(/usr/local/bin/brew shellenv)"
fi

if command -v brew >/dev/null; then
	if ! brew list libheif &>/dev/null; then
		echo "==> installing libheif runtime..."
		brew install libheif
	fi
elif [[ ! -d /opt/homebrew/opt/libheif && ! -d /usr/local/opt/libheif ]]; then
	echo "libheif is required (brew install libheif)" >&2
	exit 1
fi

mkdir -p "$INSTALL_ROOT/bin" "$LOG_DIR" "$DATA_DIR"
stop_service

if command -v docker &>/dev/null; then
	for compose in "$INSTALL_ROOT/docker-compose.yml" "$SRC_DIR/docker-compose.yml"; do
		if [[ -f "$compose" ]]; then
			echo "==> stopping docker compose ($compose)..."
			docker compose -f "$compose" down 2>/dev/null || true
		fi
	done
fi

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
	if ! command -v go >/dev/null; then
		echo "Go is required: brew install go" >&2
		exit 1
	fi
	echo "==> building $STAGING_BIN (CGO + libheif)..."
	(
		cd "$SRC_DIR"
		export CGO_ENABLED=1
		if prefix="$(brew --prefix libheif 2>/dev/null)"; then
			export CGO_CFLAGS="${CGO_CFLAGS:-} -I${prefix}/include"
			export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L${prefix}/lib -lheif"
		fi
		go build -o "$STAGING_BIN" .
	)
elif [[ ! -x "$STAGING_BIN" ]]; then
	echo "missing executable: $STAGING_BIN (set SKIP_BUILD=0 to compile on host)" >&2
	exit 1
else
	echo "==> using prebuilt $STAGING_BIN"
fi

# Retire legacy bundle name and loose launchd registrations.
rm -rf "$INSTALL_ROOT/Joyous Hub.app"

echo "==> building ${APP_BUNDLE} ..."
MACOS="$APP/Contents/MacOS"
mkdir -p "$MACOS"
cp "$STAGING_BIN" "$MACOS/joyous-hub"
chmod +x "$MACOS/joyous-hub"
echo "==> bundled $(basename "$STAGING_BIN") ($(stat -f '%Sm %z bytes' -t '%Y-%m-%d %H:%M:%S' "$STAGING_BIN"))"
printf 'APPL????' >"$APP/Contents/PkgInfo"
cat >"$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>joyous-hub</string>
	<key>CFBundleIdentifier</key>
	<string>${LABEL}</string>
	<key>CFBundleName</key>
	<string>${APP_DISPLAY_NAME}</string>
	<key>CFBundleDisplayName</key>
	<string>${APP_DISPLAY_NAME}</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSLocalNetworkUsageDescription</key>
	<string>Joyous Hub discovers photo frames on your LAN using SSDP multicast and Samsung MDC.</string>
	<key>NSBluetoothAlwaysUsageDescription</key>
	<string>Joyous Hub uses Bluetooth to adopt InkJoy e-paper frames and redirect them to the local hub.</string>
	<key>NSBonjourServices</key>
	<array>
		<string>_upnp._tcp</string>
	</array>
</dict>
</plist>
EOF

BIN="$MACOS/joyous-hub"
echo "==> codesigning ${APP_BUNDLE} ..."
PROBE_IP="${PROBE_IP:-192.168.1.108}"

if [[ -f "$ENTITLEMENTS" ]]; then
	codesign --force --sign - --entitlements "$ENTITLEMENTS" --timestamp=none "$APP"
else
	codesign --force --sign - --timestamp=none "$APP"
fi
codesign --verify --verbose=2 "$APP" >/dev/null

echo "==> probing Local Network via app bundle (approve dialog on this Mac if shown)..."
if "$BIN" --probe-network="$PROBE_IP"; then
	echo "==> Local Network probe ok ($PROBE_IP:1515)"
else
	echo "==> Local Network probe failed — open System Settings → Privacy → Local Network and allow ${APP_DISPLAY_NAME}" >&2
	echo "    Ad-hoc codesign changes on each install may require re-approving access." >&2
fi

echo "==> writing launchd plist..."
mkdir -p "$CONFIG_DIR"
if [[ ! -f "$CONFIG_FILE" ]]; then
	echo "==> creating default config at $CONFIG_FILE"
	cat >"$CONFIG_FILE" <<EOF
listen_mqtt: ":${MQTT_PORT}"
listen_http: ":${HTTP_PORT}"
upstream: "13.39.148.101:1883"
upstream_usr: ""
upstream_pwd: ""
upstream_allow: "login,heart,play_ack,fpga_ota_ack,shutdown,image_refresh_ack,ota_ack"
downstream_allow: "login_ack,heart_ack,play,device_config,shutdown_ack,image_refresh_ack,wifi_sleep"
intercept: "mqtt_config,wifi_sleep,ota,fpga"
data_dir: "${DATA_DIR}"
server_addr: "${SERVER_ADDR}"
discover_subnets: "${DISCOVER_SUBNETS}"
log_dir: ""
capture_dir: ""
ota_dir: ""
EOF
	chmod 600 "$CONFIG_FILE"
else
	echo "==> using existing config $CONFIG_FILE"
fi
# Run the signed app binary directly — not `open -W`, which cannot block on LSUIElement
# apps and spams stderr with GetProcessPID errors while KeepAlive restart-loops.
cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${LABEL}</string>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>${LABEL}</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>${BIN}</string>
	</array>
	<key>WorkingDirectory</key>
	<string>${INSTALL_ROOT}</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>${LOG_DIR}/stdout.log</string>
	<key>StandardErrorPath</key>
	<string>${LOG_DIR}/stderr.log</string>
</dict>
</plist>
EOF

if [[ "${SKIP_LAUNCHD:-0}" == "1" ]]; then
	echo "==> app bundle ready (SKIP_LAUNCHD=1; not loading launchd)"
	exit 0
fi

load_service

echo "==> waiting for hub to listen on :${HTTP_PORT} ..."
ready=0
for _ in $(seq 1 20); do
	if curl -fsS -m 2 "http://127.0.0.1:${HTTP_PORT}/api/devices" >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 1
done

if [[ "$ready" != "1" ]]; then
	echo "==> service loaded but HTTP not ready; check logs: tail -f ${LOG_DIR}/stderr.log" >&2
	exit 1
fi

echo "==> hub is up at http://${SERVER_ADDR}"
echo ""
echo "Config: ${CONFIG_FILE}"
echo "  Edit upstream_usr / upstream_pwd there, then:"
echo "    launchctl kickstart -k ${TARGET}"
echo ""
echo "Local Network permission:"
echo "  Run on this Mac's console (Screen Sharing, not SSH):"
echo "    ${INSTALL_ROOT}/scripts/grant-local-network.sh ${PROBE_IP}"
echo "  If still blocked after approving, reset and retry:"
echo "    tccutil reset LocalNetwork ${LABEL}"
echo "    ${INSTALL_ROOT}/scripts/grant-local-network.sh ${PROBE_IP}"
echo "  Then restart: launchctl kickstart -k ${TARGET}"
