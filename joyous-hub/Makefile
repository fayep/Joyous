.PHONY: build test darwin-arm64 linux-arm linux-mips clean install

build:
	CGO_ENABLED=1 go build -o joyous-hub .

# Native macOS build (requires: brew install libheif)
darwin-arm64:
	CGO_ENABLED=1 go build -o joyous-hub .

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
	rm -f joyous-hub joyous-hub-linux-arm joyous-hub-linux-arm64 joyous-hub-linux-mips
