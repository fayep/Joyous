package main

import (
	"testing"
	"time"
)

func TestMDCNetworkStandbyPacket(t *testing.T) {
	off := mdcNetworkStandbyPacket(false)
	wantOff := []byte{0xAA, 0xB5, 0x00, 0x01, 0x00, 0xB6}
	if len(off) != len(wantOff) {
		t.Fatalf("off len %d want %d: % x", len(off), len(wantOff), off)
	}
	for i := range wantOff {
		if off[i] != wantOff[i] {
			t.Fatalf("off byte %d: got 0x%02x want 0x%02x", i, off[i], wantOff[i])
		}
	}
	on := mdcNetworkStandbyPacket(true)
	wantOn := []byte{0xAA, 0xB5, 0x00, 0x01, 0x01, 0xB7}
	for i := range wantOn {
		if on[i] != wantOn[i] {
			t.Fatalf("on byte %d: got 0x%02x want 0x%02x", i, on[i], wantOn[i])
		}
	}
}

func TestInactiveWindowStartCrossMidnight(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 6, 22, 23, 30, 0, 0, loc)
	start, ok := inactiveWindowStart(now, "22:00", "07:00")
	if !ok {
		t.Fatal("expected window")
	}
	want := time.Date(2026, 6, 22, 22, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("got %v want %v", start, want)
	}
	early := time.Date(2026, 6, 22, 6, 0, 0, 0, loc)
	start2, ok := inactiveWindowStart(early, "22:00", "07:00")
	if !ok {
		t.Fatal("expected early window")
	}
	want2 := time.Date(2026, 6, 21, 22, 0, 0, 0, loc)
	if !start2.Equal(want2) {
		t.Fatalf("early got %v want %v", start2, want2)
	}
}

func TestShouldTriggerOvernightDeepSleep(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 6, 22, 23, 0, 0, 0, loc)
	on := true
	cfg := SamsungFrameConfig{
		InactiveBegin:      "22:00",
		InactiveEnd:        "07:00",
		OvernightDeepSleep: &on,
	}
	if !shouldTriggerOvernightDeepSleep(cfg, now) {
		t.Fatal("expected trigger")
	}
	cfg.OvernightDeepSleepAt = time.Date(2026, 6, 22, 22, 5, 0, 0, loc)
	if shouldTriggerOvernightDeepSleep(cfg, now) {
		t.Fatal("should not retrigger same window")
	}
	cfg.DeepSleepActive = true
	if shouldTriggerOvernightDeepSleep(cfg, now) {
		t.Fatal("already deep sleep")
	}
}
