package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"testing"
	"time"
)

func TestMDCBatteryQueryPacket(t *testing.T) {
	pkt := mdcSubCommandQueryPacket(mdcCmdBattery, mdcSubCmdBattery)
	if len(pkt) != 6 {
		t.Fatalf("len %d, want 6: % x", len(pkt), pkt)
	}
	if pkt[0] != 0xAA || pkt[1] != mdcCmdBattery || pkt[2] != 0x00 || pkt[3] != 0x01 || pkt[4] != mdcSubCmdBattery {
		t.Fatalf("unexpected packet: % x", pkt)
	}
	sum := 0
	for i := 1; i < len(pkt)-1; i++ {
		sum += int(pkt[i])
	}
	if pkt[5] != byte(sum&0xFF) {
		t.Fatalf("checksum: got 0x%02x want 0x%02x", pkt[5], sum&0xFF)
	}
}

func TestParseMDCBatteryResponse(t *testing.T) {
	// AA FF 00 09 41 1B 73 00 00 00 55 00 02 CS
	payload := []byte{0x00, 0x00, 0x00, 0x55, 0x00, 0x02}
	resp := buildMDCTestResponse(0x1B, 0x73, payload)
	pct, src, err := parseMDCBatteryResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if pct != 85 {
		t.Fatalf("percent: got %d want 85", pct)
	}
	if src != "usb" {
		t.Fatalf("power source: got %q want usb", src)
	}
}

func buildMDCTestResponse(rCmd, subCmd byte, payload []byte) []byte {
	dataLen := byte(3 + len(payload)) // 'A' + rCmd + subCmd + payload
	body := append([]byte{0x41, rCmd, subCmd}, payload...)
	pkt := append([]byte{0xAA, 0xFF, 0x00, dataLen}, body...)
	sum := 0
	for i := 1; i < len(pkt); i++ {
		sum += int(pkt[i])
	}
	return append(pkt, byte(sum&0xFF))
}

func TestMDCSleepNowPacket(t *testing.T) {
	pkt := mdcSleepNowPacket(true)
	want := []byte{0xAA, 0x11, 0x00, 0x01, 0x00, 0x12}
	if len(pkt) != len(want) {
		t.Fatalf("len %d, want %d: % x", len(pkt), len(want), pkt)
	}
	for i := range want {
		if pkt[i] != want[i] {
			t.Fatalf("byte %d: got 0x%02x want 0x%02x (full % x)", i, pkt[i], want[i], pkt)
		}
	}
}

func TestSamsungWakeMagicKey(t *testing.T) {
	got := samsungWakeMagicKey("aa:bb:cc:dd:ee:ff")
	want := sha256Hex("AA:BB:CC:DD:EE:FF:E-Paper")
	if got != want {
		t.Fatalf("magic key: got %q want %q", got, want)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// closedLocalIP returns a loopback address with nothing listening, so mdcSessionOK's dial
// fails fast (connection refused) instead of waiting out a real timeout.
func closedLocalIP(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	return host
}

// TestWaitForMDCAwakeManualReportsEveryAttempt covers a fix for a frame stuck showing
// "Sending…" in the UI while the bridge log clearly showed it retrying every ~5s waiting for
// a power-button wake: waitForMDCAwakeManual is the one call in the push path that can
// legitimately block for minutes, so onWakeAttempt must fire on every poll of this loop
// itself — an outer retry loop wrapped around the whole (blocking) call never sees the
// individual attempts, which is exactly what previously made no progress reach the hub.
func TestWaitForMDCAwakeManualReportsEveryAttempt(t *testing.T) {
	ip := closedLocalIP(t)
	var attempts []int
	var phases []string
	err := waitForMDCAwakeManual(ip, "", 35*time.Millisecond, 10*time.Millisecond, func(phase string, attempt int) {
		phases = append(phases, phase)
		attempts = append(attempts, attempt)
	})
	if err == nil {
		t.Fatal("expected an error: nothing is listening on this address")
	}
	if len(attempts) < 2 {
		t.Fatalf("expected at least 2 reported attempts before giving up, got %v", attempts)
	}
	for i, a := range attempts {
		if a != i+1 {
			t.Fatalf("attempts not sequential from 1: %v", attempts)
		}
	}
	for _, p := range phases {
		if p != wakePhaseManual {
			t.Fatalf("expected all phases to be %q, got %v", wakePhaseManual, phases)
		}
	}
}

func TestWaitForMDCAwakeManualNilCallbackIsSafe(t *testing.T) {
	ip := closedLocalIP(t)
	err := waitForMDCAwakeManual(ip, "", 15*time.Millisecond, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected an error: nothing is listening on this address")
	}
}

// TestWaitMDCAwakeReportsRemotePhaseFromFirstAttempt covers the gap reported in production: a
// real wake sequence retried the automatic WoL/MDC-magic-wake attempt (waitMDCAwake) silently for
// ~45s before the manual power-button fallback ever reported progress, so the UI showed nothing
// for the entire first phase. onWakeAttempt must fire with phase=wakePhaseRemote starting at
// attempt 1, not only once this phase gives up.
func TestWaitMDCAwakeReportsRemotePhaseFromFirstAttempt(t *testing.T) {
	// waitMDCAwake's probe interval (mdcWakeProbeInterval) is 1s, so a short timeout only ever
	// observes a single attempt — that's the point: unlike the old silent remote-wake phase,
	// attempt 1 must be reported immediately rather than only once the whole phase times out.
	ip := closedLocalIP(t)
	var attempts []int
	var phases []string
	ok := waitMDCAwake(ip, "", "", 35*time.Millisecond, func(phase string, attempt int) {
		phases = append(phases, phase)
		attempts = append(attempts, attempt)
	})
	if ok {
		t.Fatal("expected wake to fail: nothing is listening on this address")
	}
	if len(attempts) < 1 || attempts[0] != 1 {
		t.Fatalf("expected first reported attempt to be 1, got %v", attempts)
	}
	for _, p := range phases {
		if p != wakePhaseRemote {
			t.Fatalf("expected all phases to be %q, got %v", wakePhaseRemote, phases)
		}
	}
}

func TestWaitMDCAwakeNilCallbackIsSafe(t *testing.T) {
	ip := closedLocalIP(t)
	ok := waitMDCAwake(ip, "", "", 15*time.Millisecond, nil)
	if ok {
		t.Fatal("expected wake to fail: nothing is listening on this address")
	}
}
