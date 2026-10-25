package nixplaybridge

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestCredentialsRoundTrip(t *testing.T) {
	keyring.MockInit()
	if _, err := LoadCredentials("joyous-hub-nixplay", "me@example.com"); err == nil {
		t.Fatal("loaded a password that was never stored")
	}
	if err := SaveCredentials("joyous-hub-nixplay", "me@example.com", "s3cret"); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCredentials("joyous-hub-nixplay", "me@example.com")
	if err != nil || c.Email != "me@example.com" || c.Password != "s3cret" {
		t.Fatalf("got %+v, %v", c, err)
	}
	if _, err := LoadCredentials("joyous-hub-nixplay", ""); err == nil {
		t.Fatal("accepted an empty account")
	}
}
