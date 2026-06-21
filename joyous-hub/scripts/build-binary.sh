#!/usr/bin/env bash
# Build joyous-hub with version / link metadata ldflags.
# Usage: build-binary.sh <output-path>
# Env: SRC_DIR (repo root), JOYOUS_VERSION, JOYOUS_SEAL or INKJOY_SIGN_KEY
set -euo pipefail

OUT="${1:?output path required}"
SRC_DIR="${SRC_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
JOYOUS_VERSION="${JOYOUS_VERSION:-dev}"

export CGO_ENABLED=1
if prefix="$(brew --prefix libheif 2>/dev/null)"; then
	export CGO_CFLAGS="${CGO_CFLAGS:-} -I${prefix}/include"
	export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L${prefix}/lib -lheif"
fi

(
	cd "$SRC_DIR"
	if [[ -n "${JOYOUS_SEAL:-${INKJOY_SIGN_KEY:-}}" ]]; then
		SEAL="${JOYOUS_SEAL:-$INKJOY_SIGN_KEY}"
		LDFLAGS="$(VERSION="$JOYOUS_VERSION" JOYOUS_SEAL="$SEAL" go run ./cmd/embed-linkmeta)"
	else
		LDFLAGS="-X joyous-hub/internal/linkmeta.Version=${JOYOUS_VERSION}"
	fi
	go build -ldflags "$LDFLAGS" -o "$OUT" .
)
