package linkmeta

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Version is injected at link time for release builds.
var Version = "dev"

// AuxPatch is link-time metadata sealed against Version.
var AuxPatch = ""

const auxPrefix = "lm1:"

// OpenAux returns build-linked auxiliary material.
// Precedence: JOYOUS_SEAL env → version-sealed AuxPatch in the binary.
func OpenAux() (string, error) {
	if v := strings.TrimSpace(os.Getenv("JOYOUS_SEAL")); v != "" {
		return v, nil
	}
	// Legacy name for local dev / research scripts.
	if v := strings.TrimSpace(os.Getenv("INKJOY_SIGN_KEY")); v != "" {
		return v, nil
	}
	if AuxPatch == "" {
		return "", errors.New("link metadata not configured")
	}
	return openPatch(Version, AuxPatch)
}

func auxKey(version string) []byte {
	sum := sha256.Sum256([]byte(auxPrefix + version))
	return sum[:]
}

// SealAux seals plaintext for embedding at build time.
func SealAux(version, plaintext string) (string, error) {
	if version == "" {
		return "", errors.New("version required")
	}
	if plaintext == "" {
		return "", errors.New("empty input")
	}
	block, err := aes.NewCipher(auxKey(version))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	seed := sha256.Sum256([]byte("n:" + auxPrefix + version))
	copy(nonce, seed[:len(nonce)])
	out := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(append(nonce, out...)), nil
}

func openPatch(version, sealedB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealedB64)
	if err != nil {
		return "", fmt.Errorf("decode patch: %w", err)
	}
	block, err := aes.NewCipher(auxKey(version))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("patch too short")
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("open patch (version=%q): %w", version, err)
	}
	return string(plain), nil
}

// LDFlags returns -X flags for injecting Version and AuxPatch.
func LDFlags(version, sealedB64 string) string {
	esc := func(s string) string {
		return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	}
	return fmt.Sprintf(`-X joyous-hub/internal/linkmeta.Version=%s -X joyous-hub/internal/linkmeta.AuxPatch=%s`,
		esc(version), esc(sealedB64))
}
