.PHONY: build test darwin-arm64 linux-arm linux-mips clean install build-release ldflags-linkmeta build-inkjoy-bridge build-samsung-bridge build-all

VERSION ?= dev

build:
	CGO_ENABLED=1 go build -ldflags "-X joyous-hub/internal/linkmeta.Version=$(VERSION)" -o joyous-hub .

build-inkjoy-bridge:
	CGO_ENABLED=1 go build -tags inkjoybridge -ldflags "-X joyous-hub/internal/linkmeta.Version=$(VERSION)" -o inkjoy-bridge .

build-samsung-bridge:
	CGO_ENABLED=1 go build -tags samsungbridge -ldflags "-X joyous-hub/internal/linkmeta.Version=$(VERSION)" -o samsung-bridge .

build-all: build build-inkjoy-bridge build-samsung-bridge

# Release build with version-linked metadata (JOYOUS_SEAL must be set).
build-release:
	@test -n "$$JOYOUS_SEAL" || (echo "JOYOUS_SEAL required" >&2; exit 1)
	CGO_ENABLED=1 go build -ldflags "$$(VERSION=$(VERSION) JOYOUS_SEAL=$$JOYOUS_SEAL go run ./cmd/embed-linkmeta)" -o joyous-hub .

ldflags-linkmeta:
	@test -n "$$JOYOUS_SEAL" || (echo "JOYOUS_SEAL required" >&2; exit 1)
	@VERSION=$(VERSION) JOYOUS_SEAL=$$JOYOUS_SEAL go run ./cmd/embed-linkmeta

# Native macOS build (requires: brew install libheif)
darwin-arm64:
	CGO_ENABLED=1 go build -ldflags "-X joyous-hub/internal/linkmeta.Version=$(VERSION)" -o joyous-hub .

install:
	./install.sh

test:
	go test -v ./...

test-short:
	go test -short ./...

# Raspberry Pi (ARM32v7)
linux-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o joyous-hub-linux-arm .

# Apple M1/M2 (ARM64) — also works on Raspberry Pi 4/5 running 64-bit OS
linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o joyous-hub-linux-arm64 .

# EdgeRouter X / MT7621 (MIPS32 big-endian, no FPU)
linux-mips:
	GOOS=linux GOARCH=mips GOMIPS=softfloat go build -o joyous-hub-linux-mips .

clean:
	rm -f joyous-hub inkjoy-bridge samsung-bridge joyous-hub-linux-arm joyous-hub-linux-arm64 joyous-hub-linux-mips
