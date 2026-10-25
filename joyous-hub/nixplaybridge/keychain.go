package nixplaybridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// Credentials holds a Nixplay account login from the system credential store.
type Credentials struct {
	Email    string
	Password string
}

// LoadCredentials reads the Nixplay account password from the system
// credential store: the macOS Keychain, the Secret Service on Linux (GNOME
// Keyring, KWallet) or the Windows Credential Manager. service is the item's
// service name and account the Nixplay account email. Store it with
// SaveCredentials (nixplay-bridge -set-password); on macOS an item made with
//
//	security add-generic-password -a "<email>" -s "<service>" -w
//
// is read the same way.
func LoadCredentials(service, account string) (Credentials, error) {
	if err := checkItem(service, account); err != nil {
		return Credentials{}, err
	}
	password, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return Credentials{}, fmt.Errorf("no password stored for service=%q account=%q", service, account)
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("credential store (service=%q account=%q): %w", service, account, err)
	}
	if password == "" {
		return Credentials{}, fmt.Errorf("empty password stored for service=%q account=%q", service, account)
	}
	return Credentials{Email: account, Password: password}, nil
}

// SaveCredentials stores the Nixplay account password in the system
// credential store, replacing any earlier one; see LoadCredentials.
func SaveCredentials(service, account, password string) error {
	if err := checkItem(service, account); err != nil {
		return err
	}
	if password == "" {
		return errors.New("empty password")
	}
	return keyring.Set(service, account, password)
}

func checkItem(service, account string) error {
	if strings.TrimSpace(service) == "" || strings.TrimSpace(account) == "" {
		return errors.New("keychain_service and keychain_account must both be set in nixplay-config.yaml")
	}
	return nil
}
