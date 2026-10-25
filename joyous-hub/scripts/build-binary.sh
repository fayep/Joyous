#!/usr/bin/env bash
# Build joyous-hub (or a tagged bridge binary) with its version linked in.
# Usage: build-binary.sh <output-path> [build-tags]
# Env: SRC_DIR (repo root), JOYOUS_VERSION. HEIC decoding (goheif) compiles its
# own HEVC decoder with CGO, so it needs a C/C++ compiler but no system library;
# CGO_ENABLED=0 (with GOOS/GOARCH) cross-builds without HEIC support.
set -euo pipefail

OUT="${1:?output path required}"
BUILD_TAGS="${2:-}"
SRC_DIR="${SRC_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
# Timestamp suffix so a direct invocation without JOYOUS_VERSION set still gets a distinct
# version per build — see install.sh's JOYOUS_VERSION default and ui_revision.go's uiRevision.
JOYOUS_VERSION="${JOYOUS_VERSION:-dev-$(date -u +%Y%m%d%H%M%S)}"

export CGO_ENABLED="${CGO_ENABLED:-1}"
# Vector exp2/log2 kernel for the batch LAB conversions (pow_batch_simd.go);
# without it the build falls back to math.Pow (pow_batch_scalar.go).
export GOEXPERIMENT="${GOEXPERIMENT:-simd}"

(
	cd "$SRC_DIR"
	LDFLAGS="-X joyous-hub/internal/linkmeta.Version=${JOYOUS_VERSION}"
	if [[ -n "$BUILD_TAGS" ]]; then
		go build -tags "$BUILD_TAGS" -ldflags "$LDFLAGS" -o "$OUT" .
	else
		go build -ldflags "$LDFLAGS" -o "$OUT" .
	fi
)
