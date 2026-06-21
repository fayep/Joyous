// embed-linkmeta prints go build -ldflags for version-linked metadata.
//
// Usage:
//
//	JOYOUS_SEAL=... VERSION=1.0.0 go run ./cmd/embed-linkmeta
//	go build -ldflags "$(go run ./cmd/embed-linkmeta)" -o joyous-hub .
package main

import (
	"fmt"
	"os"
	"strings"

	"joyous-hub/internal/linkmeta"
)

func sealInput() string {
	if v := strings.TrimSpace(os.Getenv("JOYOUS_SEAL")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("INKJOY_SIGN_KEY"))
}

func main() {
	version := strings.TrimSpace(os.Getenv("VERSION"))
	if version == "" {
		version = strings.TrimSpace(os.Getenv("JOYOUS_VERSION"))
	}
	if version == "" {
		fmt.Fprintln(os.Stderr, "VERSION or JOYOUS_VERSION required")
		os.Exit(1)
	}
	input := sealInput()
	if input == "" {
		fmt.Fprintln(os.Stderr, "JOYOUS_SEAL required")
		os.Exit(1)
	}
	sealed, err := linkmeta.SealAux(version, input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(linkmeta.LDFlags(version, sealed))
}
