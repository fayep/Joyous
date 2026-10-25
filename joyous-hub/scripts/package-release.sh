#!/usr/bin/env bash
# Build the hub and its three bridges for one platform and pack them as a
# release archive.
# Usage: package-release.sh <goos> <goarch> <version> <out-dir>
# Build natively on the target platform: CGO compiles the HEIC decoder (and,
# on macOS, links CoreBluetooth for the InkJoy bridge). Packing for another
# platform cross-builds with CGO_ENABLED=0, which drops HEIC support (and
# can't build the macOS InkJoy bridge at all).
set -euo pipefail

GOOS_T="${1:?goos}"
GOARCH_T="${2:?goarch}"
VERSION="${3:?version}"
OUT_DIR="${4:?out dir}"
SRC_DIR="$(cd "$(dirname "$0")/.." && pwd)"

name="joyous-${VERSION}-${GOOS_T}-${GOARCH_T}"
stage="$(mktemp -d)/$name"
mkdir -p "$stage/bin" "$OUT_DIR"

ext=""
[[ "$GOOS_T" == "windows" ]] && ext=".exe"
if [[ "$GOOS_T/$GOARCH_T" != "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" ]]; then
	echo "warning: cross-building $GOOS_T/$GOARCH_T without CGO: no HEIC support" >&2
	export CGO_ENABLED=0
fi
export GOOS="$GOOS_T" GOARCH="$GOARCH_T" JOYOUS_VERSION="$VERSION"

for spec in joyous-hub: inkjoy-bridge:inkjoybridge samsung-bridge:samsungbridge nixplay-bridge:nixplaybridge; do
	bin="${spec%%:*}"
	tags="${spec#*:}"
	echo "==> $name: $bin"
	bash "$SRC_DIR/scripts/build-binary.sh" "$stage/bin/$bin$ext" "$tags"
done

cp "$SRC_DIR"/*.yaml.example "$SRC_DIR/LICENSE" "$SRC_DIR/THIRD_PARTY_NOTICES.md" "$stage/"
cp "$SRC_DIR/scripts/RELEASE-INSTALL.md" "$stage/INSTALL.md"
if [[ "$GOOS_T" == "darwin" ]]; then
	mkdir -p "$stage/scripts"
	cp "$SRC_DIR/entitlements.plist" "$SRC_DIR/entitlements-inkjoy.plist" "$stage/"
	cp "$SRC_DIR/scripts/install-local.sh" "$SRC_DIR/scripts/uninstall-local.sh" \
		"$SRC_DIR/scripts/grant-local-network.sh" "$stage/scripts/"
fi

parent="$(dirname "$stage")"
if [[ "$GOOS_T" == "windows" ]]; then
	if command -v zip >/dev/null; then
		(cd "$parent" && zip -qr "$OUT_DIR/$name.zip" "$name")
	else
		(cd "$parent" && 7z a -bd "$OUT_DIR/$name.zip" "$name" >/dev/null)
	fi
	echo "==> $OUT_DIR/$name.zip"
else
	tar -C "$parent" -czf "$OUT_DIR/$name.tar.gz" "$name"
	echo "==> $OUT_DIR/$name.tar.gz"
fi
rm -rf "$parent"
