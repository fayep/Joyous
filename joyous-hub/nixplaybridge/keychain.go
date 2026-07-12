package nixplaybridge

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Credentials holds a Nixplay account login pulled from macOS Keychain.
type Credentials struct {
	Email    string
	Password string
}

// LoadCredentials reads the Nixplay account password from the macOS Keychain
// (Passwords app) generic-password item created with:
//
//	security add-generic-password -a "<email>" -s "<service>" -w "<password>"
//
// account must be the Nixplay account email (the Keychain "account" field).
func LoadCredentials(service, account string) (Credentials, error) {
	if runtime.GOOS != "darwin" {
		return Credentials{}, fmt.Errorf("keychain credential lookup is macOS-only (GOOS=%s)", runtime.GOOS)
	}
	if strings.TrimSpace(service) == "" || strings.TrimSpace(account) == "" {
		return Credentials{}, fmt.Errorf("keychain_service and keychain_account must both be set in nixplay-config.yaml")
	}
	cmd := exec.Command("security", "find-generic-password", "-a", account, "-s", service, "-w")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Credentials{}, fmt.Errorf("security find-generic-password (service=%q account=%q): %w: %s", service, account, err, strings.TrimSpace(stderr.String()))
	}
	password := strings.TrimRight(out.String(), "\n")
	if password == "" {
		return Credentials{}, fmt.Errorf("empty password returned from Keychain for service=%q account=%q", service, account)
	}
	return Credentials{Email: account, Password: password}, nil
}
