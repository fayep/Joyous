package linkmeta

import (
	"strings"
	"testing"
)

func TestSealOpenRoundtrip(t *testing.T) {
	const version = "1.0.0"
	const input = "test-material"
	sealed, err := SealAux(version, input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := openPatch(version, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got != input {
		t.Fatalf("got %q want %q", got, input)
	}
}

func TestOpenWrongVersion(t *testing.T) {
	sealed, err := SealAux("1.0.0", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openPatch("1.0.1", sealed); err == nil {
		t.Fatal("expected failure for wrong version")
	}
}

func TestOpenAuxFromEnv(t *testing.T) {
	t.Setenv("JOYOUS_SEAL", "from-env")
	got, err := OpenAux()
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAuxEmbedded(t *testing.T) {
	const version = "2.3.4"
	const input = "embedded"
	sealed, err := SealAux(version, input)
	if err != nil {
		t.Fatal(err)
	}
	oldV, oldP := Version, AuxPatch
	Version, AuxPatch = version, sealed
	t.Cleanup(func() { Version, AuxPatch = oldV, oldP })
	t.Setenv("JOYOUS_SEAL", "")
	t.Setenv("INKJOY_SIGN_KEY", "")

	got, err := OpenAux()
	if err != nil {
		t.Fatal(err)
	}
	if got != input {
		t.Fatalf("got %q want %q", got, input)
	}
}

func TestLDFlagsFormat(t *testing.T) {
	flags := LDFlags("1.0.0", "abc+def=")
	if !strings.Contains(flags, `-X joyous-hub/internal/linkmeta.Version=1.0.0`) {
		t.Fatalf("flags %q", flags)
	}
	if !strings.Contains(flags, `-X joyous-hub/internal/linkmeta.AuxPatch=`) {
		t.Fatalf("flags %q", flags)
	}
}
