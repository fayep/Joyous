package main

import (
	"testing"
	"time"
)

func TestMdcDailyRefreshSetPacket(t *testing.T) {
	pkt := mdcDailyRefreshSetPacket(7, 30)
	if len(pkt) < 9 {
		t.Fatalf("packet too short: % x", pkt)
	}
	if pkt[0] != 0xAA || pkt[1] != mdcCmdBattery {
		t.Fatalf("header: % x", pkt[:4])
	}
	if pkt[4] != mdcSubCmdDailyRefresh || pkt[5] != 0x80 || pkt[6] != 0x02 {
		t.Fatalf("payload prefix: % x", pkt[4:8])
	}
	if pkt[7] != 7 || pkt[8] != 30 {
		t.Fatalf("time bytes: % x", pkt[7:9])
	}
}

func TestParseMDCDailyRefreshPayload(t *testing.T) {
	h, m, ok := parseMDCDailyRefreshPayload([]byte{0x81, 0x02, 6, 15})
	if !ok || h != 6 || m != 15 {
		t.Fatalf("TLV parse: ok=%v h=%d m=%d", ok, h, m)
	}
	h, m, ok = parseMDCDailyRefreshPayload([]byte{6, 15})
	if !ok || h != 6 || m != 15 {
		t.Fatalf("raw parse: ok=%v h=%d m=%d", ok, h, m)
	}
}

func TestParseMDCDailyRefreshResponse(t *testing.T) {
	resp := []byte{0xAA, 0xFF, 0x00, 0x06, 'A', mdcCmdBattery, mdcSubCmdDailyRefresh, 0x81, 0x02, 8, 0, 0}
	sum := 0
	for i := 1; i < len(resp)-1; i++ {
		sum += int(resp[i])
	}
	resp[len(resp)-1] = byte(sum & 0xFF)
	h, m, err := parseMDCDailyRefreshResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if h != 8 || m != 0 {
		t.Fatalf("got %02d:%02d", h, m)
	}
}

func TestInMorningRestoreWindow(t *testing.T) {
	loc := time.FixedZone("test", 0)
	end := "07:00"
	begin := "22:00"

	beforeWindow := time.Date(2026, 6, 20, 6, 49, 0, 0, loc)
	if inMorningRestoreWindow(beforeWindow, begin, end) {
		t.Fatal("expected false before 10-minute window")
	}
	inWindow := time.Date(2026, 6, 20, 6, 55, 0, 0, loc)
	if !inMorningRestoreWindow(inWindow, begin, end) {
		t.Fatal("expected inside pre-refresh window")
	}
	atEnd := time.Date(2026, 6, 20, 7, 0, 0, 0, loc)
	if !inMorningRestoreWindow(atEnd, begin, end) {
		t.Fatal("expected true at inactive end")
	}
	afterEnd := time.Date(2026, 6, 20, 7, 1, 0, 0, loc)
	if inMorningRestoreWindow(afterEnd, begin, end) {
		t.Fatal("expected false after inactive end")
	}
	duringInactive := time.Date(2026, 6, 20, 23, 0, 0, 0, loc)
	if inMorningRestoreWindow(duringInactive, begin, end) {
		t.Fatal("expected false during inactive window (not tail)")
	}
	preRefresh := time.Date(2026, 6, 20, 8, 54, 0, 0, loc)
	if !inMorningRestoreWindow(preRefresh, begin, "09:00") {
		t.Fatal("expected true during frame pre-refresh window before 09:00 inactive end")
	}
}

func TestShouldTriggerMorningStandbyRestore(t *testing.T) {
	loc := time.FixedZone("test", 0)
	now := time.Date(2026, 6, 20, 6, 55, 0, 0, loc)
	cfg := SamsungFrameConfig{
		InactiveBegin:   "22:00",
		InactiveEnd:     "07:00",
		DeepSleepActive: true,
	}
	if !shouldTriggerMorningStandbyRestore(cfg, now) {
		t.Fatal("expected trigger")
	}
	cfg.MorningStandbyRestoredAt = time.Date(2026, 6, 20, 6, 56, 0, 0, loc)
	if shouldTriggerMorningStandbyRestore(cfg, now) {
		t.Fatal("expected skip after restore this window")
	}
	cfg.MorningStandbyRestoredAt = time.Time{}
	cfg.DeepSleepActive = false
	if shouldTriggerMorningStandbyRestore(cfg, now) {
		t.Fatal("expected skip when not in deep sleep")
	}
}
