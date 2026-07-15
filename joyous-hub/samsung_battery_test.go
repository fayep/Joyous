package main

import (
	"testing"
	"time"
)

func TestSamsungBatteryStoreRecordAndSummary(t *testing.T) {
	store := NewSamsungBatteryStore(t.TempDir())
	id := "samsung:192.168.1.108"

	if !store.Record(id, 100, "usb", samsungBatteryPreSleep) {
		t.Fatal("first record should append")
	}
	if store.Record(id, 100, "usb", samsungBatteryPreSleep) {
		t.Fatal("duplicate within gap should be ignored")
	}
	store.Record(id, 98, "usb", samsungBatteryPreSleep)
	store.Record(id, 95, "usb", samsungBatteryPreSleep)

	sum := store.Summary(id, 5)
	if sum.Samples != 3 {
		t.Fatalf("samples: got %d want 3", sum.Samples)
	}
	if sum.Delta == nil || *sum.Delta != -3 {
		t.Fatalf("delta: got %v want -3", sum.Delta)
	}
	if sum.PushDelta == nil || *sum.PushDelta != -3 {
		t.Fatalf("push delta: got %v want -3", sum.PushDelta)
	}
	if len(sum.Recent) != 3 {
		t.Fatalf("recent: got %d want 3", len(sum.Recent))
	}
}

func TestSamsungBatteryStoreLoadSave(t *testing.T) {
	dir := t.TempDir()
	store := NewSamsungBatteryStore(dir)
	id := "samsung:192.168.1.108"
	store.Record(id, 88, "wireless", samsungBatteryPreSleep)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	store2 := NewSamsungBatteryStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatal(err)
	}
	sum := store2.Summary(id, 5)
	if sum.Samples != 1 || sum.Recent[0].Percent != 88 {
		t.Fatalf("reload: %+v", sum)
	}
}

func TestSamsungBatteryPushDeltaIgnoresPoll(t *testing.T) {
	history := []SamsungBatterySample{
		{At: time.Now().Add(-2 * time.Hour), Percent: 100, Source: samsungBatteryPreSleep},
		{At: time.Now().Add(-time.Hour), Percent: 99, Source: samsungBatteryPoll},
		{At: time.Now(), Percent: 97, Source: samsungBatteryPreSleep},
	}
	d, ok := samsungBatteryPushDelta(history)
	if !ok {
		t.Fatal("expected push delta")
	}
	if d != -3 {
		t.Fatalf("push delta: got %d want -3", d)
	}
}

func TestSamsungBatteryPushDeltaUsesPostPush(t *testing.T) {
	history := []SamsungBatterySample{
		{At: time.Now().Add(-2 * time.Hour), Percent: 100, Source: samsungBatteryPostPush},
		{At: time.Now().Add(-time.Hour), Percent: 99, Source: samsungBatteryPoll},
		{At: time.Now(), Percent: 96, Source: samsungBatteryPostPush},
	}
	d, ok := samsungBatteryPushDelta(history)
	if !ok {
		t.Fatal("expected push delta")
	}
	if d != -4 {
		t.Fatalf("push delta: got %d want -4", d)
	}
}
