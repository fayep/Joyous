#!/usr/bin/env bash
# Install or upgrade joyous-hub as a native launchd service on macOS (Apple silicon).
# Runs inside JoyousHub.app so macOS grants Local Network access for discovery.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_ROOT="${INSTALL_ROOT:-$HOME/joyous-hub}"
SRC_DIR="${SRC_DIR:-$INSTALL_ROOT/src}"
DATA_DIR="${DATA_DIR:-/Volumes/tank/Media/photoframe}"
HTTP_PORT="${HTTP_PORT:-18080}"
MQTT_PORT="${MQTT_PORT:-1883}"
SERVER_ADDR="${SERVER_ADDR:-$(hostname -s | tr '[:upper:]' '[:lower:]').local:${HTTP_PORT}}"
DISCOVER_SUBNETS="${DISCOVER_SUBNETS:-192.168.50}"
JOYOUS_VERSION="${JOYOUS_VERSION:-0.9.0}"
WITH_INKJOY="${WITH_INKJOY:-0}"
WITH_SAMSUNG="${WITH_SAMSUNG:-0}"
WITH_NIXPLAY="${WITH_NIXPLAY:-0}"
INKJOY_MQTT_PORT="${INKJOY_MQTT_PORT:-11883}"
NIXPLAY_ACCOUNT="${NIXPLAY_ACCOUNT:-}"
NIXPLAY_KEYCHAIN_SERVICE="${NIXPLAY_KEYCHAIN_SERVICE:-joyous-hub-nixplay}"
HUB_HOST="${SERVER_ADDR%%:*}"
HUB_HTTP="${HUB_HTTP:-http://${HUB_HOST}:${HTTP_PORT#:}}"
# MDC content URLs must point at the hub HTTP cache (:18080).
SAMSUNG_SERVER_ADDR="${SAMSUNG_SERVER_ADDR:-${HUB_HOST}:${HTTP_PORT#:}}"
INKJOY_UPSTREAM="${INKJOY_UPSTREAM:-13.39.148.101:1883}"
LABEL="com.joyous.hub"
APP_BUNDLE="JoyousHub.app"
APP_DISPLAY_NAME="Joyous Hub"
APP="$INSTALL_ROOT/${APP_BUNDLE}"
PLIST="$HOME/Library/LaunchAgents/${LABEL}.plist"
LOG_DIR="$HOME/Library/Logs/joyous-hub"
CONFIG_DIR="$HOME/Library/Application Support/Joyous"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
INKJOY_CONFIG_FILE="$CONFIG_DIR/inkjoy-config.yaml"
SAMSUNG_CONFIG_FILE="$CONFIG_DIR/samsung-config.yaml"
NIXPLAY_CONFIG_FILE="$CONFIG_DIR/nixplay-config.yaml"
STAGING_BIN="$INSTALL_ROOT/bin/joyous-hub"
ENTITLEMENTS="${ENTITLEMENTS:-$INSTALL_ROOT/entitlements.plist}"
INKJOY_ENTITLEMENTS="${INKJOY_ENTITLEMENTS:-$INSTALL_ROOT/entitlements-inkjoy.plist}"
USER_ID="$(id -u)"
DOMAIN="gui/${USER_ID}"
TARGET="${DOMAIN}/${LABEL}"

if [[ ! -f "$ENTITLEMENTS" ]]; then
	ENTITLEMENTS="$SCRIPT_DIR/../entitlements.plist"
fi
if [[ ! -f "$INKJOY_ENTITLEMENTS" ]]; then
	INKJOY_ENTITLEMENTS="$SCRIPT_DIR/../entitlements-inkjoy.plist"
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

stop_bridge_service() {
	local label="$1"
	local executable="$2"
	local plist="$HOME/Library/LaunchAgents/${label}.plist"
	local target="${DOMAIN}/${label}"

	echo "==> stopping any existing ${label} service ..."
	launchctl bootout "$target" 2>/dev/null || true
	launchctl bootout "$DOMAIN" "$plist" 2>/dev/null || true
	launchctl unload -w "$plist" 2>/dev/null || true
	if pgrep -f "${INSTALL_ROOT}.*${executable}" >/dev/null 2>&1; then
		pkill -f "${INSTALL_ROOT}.*${executable}" 2>/dev/null || true
		sleep 1
	fi
}

load_bridge_service() {
	local label="$1"
	local plist="$2"
	local bin="$3"
	local app="$4"
	local executable
	local target="${DOMAIN}/${label}"
	executable="$(basename "$bin")"

	if ! plutil -lint "$plist" >/dev/null; then
		echo "invalid launchd plist:" >&2
		plutil -lint "$plist" >&2
		exit 1
	fi
	if [[ ! -x "$bin" ]]; then
		echo "missing executable: $bin" >&2
		exit 1
	fi
	if [[ ! -d "$app" ]]; then
		echo "missing app bundle: $app" >&2
		exit 1
	fi

	stop_bridge_service "$label" "$executable"

	if launchctl bootstrap "$DOMAIN" "$plist" 2>/dev/null; then
		:
	elif launchctl print "$target" >/dev/null 2>&1; then
		stop_bridge_service "$label" "$executable"
		launchctl bootstrap "$DOMAIN" "$plist"
	else
		launchctl bootstrap "$DOMAIN" "$plist"
	fi

	launchctl enable "$target" 2>/dev/null || true
	launchctl kickstart -k "$target"
}

write_bridge_app() {
	local app_bundle="$1"
	local executable="$2"
	local display_name="$3"
	local bundle_id="$4"
	local network_desc="$5"
	local staging_bin="$6"
	local bluetooth_desc="${7:-}"       # empty = omit NSBluetoothAlwaysUsageDescription (only InkJoy needs BLE)
	local entitlements="${8:-$ENTITLEMENTS}" # only InkJoy passes one with com.apple.security.device.bluetooth

	echo "==> building ${app_bundle} ..."
	local macos="$INSTALL_ROOT/${app_bundle}/Contents/MacOS"
	mkdir -p "$macos"
	cp "$staging_bin" "$macos/${executable}"
	chmod +x "$macos/${executable}"
	printf 'APPL????' >"$INSTALL_ROOT/${app_bundle}/Contents/PkgInfo"
	local bluetooth_block=""
	if [[ -n "$bluetooth_desc" ]]; then
		bluetooth_block="	<key>NSBluetoothAlwaysUsageDescription</key>
	<string>${bluetooth_desc}</string>
"
	fi
	cat >"$INSTALL_ROOT/${app_bundle}/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>${executable}</string>
	<key>CFBundleIdentifier</key>
	<string>${bundle_id}</string>
	<key>CFBundleName</key>
	<string>${display_name}</string>
	<key>CFBundleDisplayName</key>
	<string>${display_name}</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>${JOYOUS_VERSION}</string>
	<key>CFBundleVersion</key>
	<string>${JOYOUS_VERSION}</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSLocalNetworkUsageDescription</key>
	<string>${network_desc}</string>
${bluetooth_block}	<key>NSBonjourServices</key>
	<array>
		<string>_upnp._tcp</string>
	</array>
</dict>
</plist>
EOF

	local app="$INSTALL_ROOT/${app_bundle}"
	if [[ -f "$entitlements" ]]; then
		codesign --force --sign - --entitlements "$entitlements" --timestamp=none "$app"
	else
		codesign --force --sign - --timestamp=none "$app"
	fi
	codesign --verify --verbose=2 "$app" >/dev/null
}

install_inkjoy_bridge() {
	local label="com.joyous.inkjoy-bridge"
	local app_bundle="InkJoyBridge.app"
	local executable="inkjoy-bridge"
	local staging_bin="$INSTALL_ROOT/bin/inkjoy-bridge"
	local log_dir="$HOME/Library/Logs/joyous-inkjoy-bridge"
	local plist="$HOME/Library/LaunchAgents/${label}.plist"
	local data_dir="$INSTALL_ROOT/data-inkjoy"
	local app="$INSTALL_ROOT/${app_bundle}"
	local bin="$app/Contents/MacOS/${executable}"
	local target="${DOMAIN}/${label}"

	if [[ ! -x "$staging_bin" ]]; then
		echo "missing executable: $staging_bin (re-run install.sh --with-inkjoy)" >&2
		exit 1
	fi

	mkdir -p "$log_dir" "$data_dir"
	stop_bridge_service "$label" "$executable"

	mkdir -p "$CONFIG_DIR"
	if [[ ! -f "$INKJOY_CONFIG_FILE" ]]; then
		echo "==> creating default inkjoy config at $INKJOY_CONFIG_FILE"
		cat >"$INKJOY_CONFIG_FILE" <<EOF
hub_mqtt: "tcp://127.0.0.1:${MQTT_PORT#:}"
listen_mqtt: ":${INKJOY_MQTT_PORT#:}"
hub_http: "${HUB_HTTP}"
upstream: "${INKJOY_UPSTREAM}"
upstream_usr: ""
upstream_pwd: ""
upstream_allow: "login,heart,play_ack,fpga_ota_ack,sleep,image_refresh_ack,ota_ack,wifi_sleep_ack,mqtt_config_ack"
downstream_allow: "login_ack,heart_ack,play,device_config,shutdown_ack,image_refresh_ack,wifi_sleep"
intercept: "mqtt_config,wifi_sleep,ota,fpga"
data_dir: "${data_dir}"
hub_data_dir: "${DATA_DIR}"
capture_dir: ""
ota_dir: ""
log_dir: ""
EOF
		chmod 600 "$INKJOY_CONFIG_FILE"
	else
		echo "==> using existing inkjoy config $INKJOY_CONFIG_FILE"
	fi

	write_bridge_app "$app_bundle" "$executable" "InkJoy Bridge" "$label" \
		"InkJoy Bridge connects photo frames to Joyous Hub over MQTT and discovers frames on your LAN." \
		"$staging_bin" \
		"InkJoy Bridge uses Bluetooth to adopt new e-paper frames." \
		"$INKJOY_ENTITLEMENTS"

	cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${label}</string>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>${label}</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>${bin}</string>
		<string>-config</string>
		<string>${INKJOY_CONFIG_FILE}</string>
		<string>-hub-data-dir</string>
		<string>${DATA_DIR}</string>
	</array>
	<key>WorkingDirectory</key>
	<string>${INSTALL_ROOT}</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
		<key>INKJOY_MQTT_USER</key>
		<string></string>
		<key>INKJOY_MQTT_PASSWORD</key>
		<string></string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>${log_dir}/stdout.log</string>
	<key>StandardErrorPath</key>
	<string>${log_dir}/stderr.log</string>
</dict>
</plist>
EOF

	if [[ "${SKIP_LAUNCHD:-0}" == "1" ]]; then
		echo "==> ${app_bundle} ready (SKIP_LAUNCHD=1)"
		return 0
	fi

	load_bridge_service "$label" "$plist" "$bin" "$app"
	echo "==> inkjoy-bridge is up (frame MQTT :${INKJOY_MQTT_PORT#:}, play relay via hub ${HUB_HTTP})"
	echo "    Cloud credentials: edit ${INKJOY_CONFIG_FILE} (upstream_usr / upstream_pwd), then:"
	echo "      launchctl kickstart -k ${target}"
}

install_samsung_bridge() {
	local label="com.joyous.samsung-bridge"
	local app_bundle="SamsungBridge.app"
	local executable="samsung-bridge"
	local staging_bin="$INSTALL_ROOT/bin/samsung-bridge"
	local log_dir="$HOME/Library/Logs/joyous-samsung-bridge"
	local plist="$HOME/Library/LaunchAgents/${label}.plist"
	local data_dir="$INSTALL_ROOT/data-samsung"
	local app="$INSTALL_ROOT/${app_bundle}"
	local bin="$app/Contents/MacOS/${executable}"
	local target="${DOMAIN}/${label}"

	if [[ ! -x "$staging_bin" ]]; then
		echo "missing executable: $staging_bin (re-run install.sh --with-samsung)" >&2
		exit 1
	fi

	mkdir -p "$log_dir" "$data_dir"
	stop_bridge_service "$label" "$executable"

	mkdir -p "$CONFIG_DIR"
	if [[ ! -f "$SAMSUNG_CONFIG_FILE" ]]; then
		echo "==> creating default samsung config at $SAMSUNG_CONFIG_FILE"
		cat >"$SAMSUNG_CONFIG_FILE" <<EOF
hub_mqtt: "tcp://127.0.0.1:${MQTT_PORT#:}"
hub_http: "${HUB_HTTP}"
server_addr: "${SAMSUNG_SERVER_ADDR}"
data_dir: "${data_dir}"
hub_data_dir: "${DATA_DIR}"
discover_subnets: "${DISCOVER_SUBNETS}"
log_dir: ""
EOF
		chmod 600 "$SAMSUNG_CONFIG_FILE"
	else
		echo "==> using existing samsung config $SAMSUNG_CONFIG_FILE"
	fi

	write_bridge_app "$app_bundle" "$executable" "Samsung Bridge" "$label" \
		"Samsung Bridge discovers photo frames on your LAN using SSDP and Samsung MDC." \
		"$staging_bin"

	cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${label}</string>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>${label}</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>${bin}</string>
		<string>-config</string>
		<string>${SAMSUNG_CONFIG_FILE}</string>
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
	<string>${log_dir}/stdout.log</string>
	<key>StandardErrorPath</key>
	<string>${log_dir}/stderr.log</string>
</dict>
</plist>
EOF

	if [[ "${SKIP_LAUNCHD:-0}" == "1" ]]; then
		echo "==> ${app_bundle} ready (SKIP_LAUNCHD=1)"
		return 0
	fi

	load_bridge_service "$label" "$plist" "$bin" "$app"
	echo "==> samsung-bridge is up (MDC content via hub ${SAMSUNG_SERVER_ADDR})"
	echo "    Restart: launchctl kickstart -k ${target}"
}

install_nixplay_bridge() {
	local label="com.joyous.nixplay-bridge"
	local app_bundle="NixplayBridge.app"
	local executable="nixplay-bridge"
	local staging_bin="$INSTALL_ROOT/bin/nixplay-bridge"
	local log_dir="$HOME/Library/Logs/joyous-nixplay-bridge"
	local plist="$HOME/Library/LaunchAgents/${label}.plist"
	local data_dir="$INSTALL_ROOT/data-nixplay"
	local app="$INSTALL_ROOT/${app_bundle}"
	local bin="$app/Contents/MacOS/${executable}"
	local target="${DOMAIN}/${label}"

	if [[ ! -x "$staging_bin" ]]; then
		echo "missing executable: $staging_bin (re-run install.sh --with-nixplay)" >&2
		exit 1
	fi

	mkdir -p "$log_dir" "$data_dir"
	stop_bridge_service "$label" "$executable"

	mkdir -p "$CONFIG_DIR"
	if [[ ! -f "$NIXPLAY_CONFIG_FILE" ]]; then
		echo "==> creating default nixplay config at $NIXPLAY_CONFIG_FILE"
		cat >"$NIXPLAY_CONFIG_FILE" <<EOF
hub_mqtt: "tcp://127.0.0.1:${MQTT_PORT#:}"
hub_http: "${HUB_HTTP}"
data_dir: "${data_dir}"
keychain_service: "${NIXPLAY_KEYCHAIN_SERVICE}"
keychain_account: "${NIXPLAY_ACCOUNT}"
log_dir: ""
EOF
		chmod 600 "$NIXPLAY_CONFIG_FILE"
	else
		echo "==> using existing nixplay config $NIXPLAY_CONFIG_FILE"
	fi

	write_bridge_app "$app_bundle" "$executable" "Nixplay Bridge" "$label" \
		"Nixplay Bridge uploads photos to your Nixplay account so they sync to Nixplay frames." \
		"$staging_bin"

	cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${label}</string>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>${label}</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>${bin}</string>
		<string>-config</string>
		<string>${NIXPLAY_CONFIG_FILE}</string>
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
	<string>${log_dir}/stdout.log</string>
	<key>StandardErrorPath</key>
	<string>${log_dir}/stderr.log</string>
</dict>
</plist>
EOF

	if [[ "${SKIP_LAUNCHD:-0}" == "1" ]]; then
		echo "==> ${app_bundle} ready (SKIP_LAUNCHD=1)"
		return 0
	fi

	if [[ -z "$NIXPLAY_ACCOUNT" ]]; then
		echo "==> WARNING: keychain_account is empty in $NIXPLAY_CONFIG_FILE — edit it, then:"
		echo "      launchctl kickstart -k ${target}"
	fi
	echo "==> nixplay-bridge needs its Keychain item on THIS Mac ($(hostname -s)), in this"
	echo "    user's GUI login session — not wherever install.sh was run from. Run at this"
	echo "    Mac's console (Screen Sharing, not SSH), or via 'launchctl asuser' over SSH:"
	echo "      security add-generic-password -a \"<nixplay-email>\" -s \"${NIXPLAY_KEYCHAIN_SERVICE}\" -T \"${bin}\" -w"

	load_bridge_service "$label" "$plist" "$bin" "$app"
	echo "==> nixplay-bridge is up"
	echo "    Restart: launchctl kickstart -k ${target}"
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
	bash "$SCRIPT_DIR/build-binary.sh" "$STAGING_BIN"
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
	<string>${JOYOUS_VERSION}</string>
	<key>CFBundleVersion</key>
	<string>${JOYOUS_VERSION}</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSLocalNetworkUsageDescription</key>
	<string>Joyous Hub discovers photo frames on your LAN using SSDP multicast and Samsung MDC.</string>
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
data_dir: "${DATA_DIR}"
server_addr: "${SERVER_ADDR}"
discover_subnets: "${DISCOVER_SUBNETS}"
log_dir: ""
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

verify_inkjoy_cache_route() {
	local headers
	headers="$(curl -sI -m 3 "http://127.0.0.1:${HTTP_PORT}/inkjoy/000000000000/__probe__.bin" 2>/dev/null || true)"
	if echo "$headers" | grep -qi 'x-joyous-inkjoy-cache:'; then
		echo "==> inkjoy cache HTTP route ok (hub serves /inkjoy/{mac}/*.bin from disk)"
		return 0
	fi
	if echo "$headers" | grep -q '502'; then
		echo "==> ERROR: /inkjoy/*.bin is hitting the bridge MQTT proxy — joyous-hub is too old or was not restarted after upgrade" >&2
		return 1
	fi
	echo "==> ERROR: inkjoy cache route not active ($(echo "$headers" | head -1 || echo no response))" >&2
	return 1
}

if ! verify_inkjoy_cache_route; then
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

if [[ "$WITH_INKJOY" == "1" ]]; then
	echo ""
	install_inkjoy_bridge
fi
if [[ "$WITH_SAMSUNG" == "1" ]]; then
	echo ""
	install_samsung_bridge
fi
if [[ "$WITH_NIXPLAY" == "1" ]]; then
	echo ""
	install_nixplay_bridge
fi
