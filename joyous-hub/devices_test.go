package main

import (
	"testing"
	"time"
)

// TestRegisterDevice: new device appears in list after registration.
func TestRegisterDevice(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.MarkConnected("AABBCCDDEEFF")
	devs := reg.List()
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	if devs[0].MAC != "AABBCCDDEEFF" {
		t.Errorf("MAC: got %q", devs[0].MAC)
	}
	if devs[0].ID != "AABBCCDDEEFF" {
		t.Errorf("ID: got %q", devs[0].ID)
	}
	if devs[0].Type != DeviceTypeInkJoy {
		t.Errorf("Type: got %q", devs[0].Type)
	}
	if !devs[0].Connected {
		t.Error("device should be connected")
	}
}

// TestUpdateDeviceHeart: heart payload updates telemetry fields.
func TestUpdateDeviceHeart(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.MarkConnected("AABBCCDDEEFF")
	reg.UpdateHeart("AABBCCDDEEFF", HeartInfo{Battery: 72, RSSI: -55, Firmware: "0.5.6"})

	devs := reg.List()
	d := devs[0]
	if d.Battery != 72 {
		t.Errorf("Battery: got %d want 72", d.Battery)
	}
	if d.RSSI != -55 {
		t.Errorf("RSSI: got %d want -55", d.RSSI)
	}
	if d.Firmware != "0.5.6" {
		t.Errorf("Firmware: got %q", d.Firmware)
	}
}

// TestMarkDisconnected: connected → disconnected changes the flag.
func TestMarkDisconnected(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.MarkConnected("AABBCCDDEEFF")
	reg.MarkDisconnected("AABBCCDDEEFF")
	devs := reg.List()
	if devs[0].Connected {
		t.Error("device should be disconnected")
	}
}

// TestLastSeenUpdated: MarkConnected and UpdateHeart update LastSeen.
func TestLastSeenUpdated(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	before := time.Now().Add(-time.Second)
	reg.MarkConnected("AABBCCDDEEFF")
	devs := reg.List()
	if !devs[0].LastSeen.After(before) {
		t.Error("LastSeen should be recent after MarkConnected")
	}
}

// TestPersistAndReload: devices survive a registry reload from disk.
func TestPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	reg := NewDeviceRegistry(dir)
	reg.MarkConnected("AABBCCDDEEFF")
	reg.UpdateHeart("AABBCCDDEEFF", HeartInfo{Battery: 88, Firmware: "0.5.6"})
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reg2 := NewDeviceRegistry(dir)
	if err := reg2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	devs := reg2.List()
	if len(devs) != 1 {
		t.Fatalf("expected 1 device after reload, got %d", len(devs))
	}
	if devs[0].Battery != 88 {
		t.Errorf("Battery after reload: got %d", devs[0].Battery)
	}
	// Connected flag resets to false on reload (device is not actually connected).
	if devs[0].Connected {
		t.Error("Connected should be false after reload")
	}
}

// TestMultipleDevices: two devices tracked independently.
func TestMultipleDevices(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.MarkConnected("AABBCCDDEEFF")
	reg.MarkConnected("30EDA0E3FBE8")
	reg.UpdateHeart("AABBCCDDEEFF", HeartInfo{Battery: 50})
	reg.UpdateHeart("30EDA0E3FBE8", HeartInfo{Battery: 90})

	devs := reg.List()
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devs))
	}
	byMAC := map[string]Device{}
	for _, d := range devs {
		byMAC[d.MAC] = d
	}
	if byMAC["AABBCCDDEEFF"].Battery != 50 {
		t.Error("wrong battery for first device")
	}
	if byMAC["30EDA0E3FBE8"].Battery != 90 {
		t.Error("wrong battery for second device")
	}
}

func TestDeviceListSortedByName(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.MarkConnected("AABBCCDDEEFF")
	reg.MarkConnected("30EDA0E3FBE8")
	reg.MarkConnected("CCDDEEFF0011")
	reg.SetName("AABBCCDDEEFF", "Zebra")
	reg.SetName("30EDA0E3FBE8", "Alpha")
	// CCDDEEFF0011 keeps MAC as label

	devs := reg.List()
	if len(devs) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(devs))
	}
	if deviceDisplayLabel(devs[0]) != "Alpha" {
		t.Fatalf("first: got %q", deviceDisplayLabel(devs[0]))
	}
	if deviceDisplayLabel(devs[1]) != "CCDDEEFF0011" {
		t.Fatalf("second: got %q", deviceDisplayLabel(devs[1]))
	}
	if deviceDisplayLabel(devs[2]) != "Zebra" {
		t.Fatalf("third: got %q", deviceDisplayLabel(devs[2]))
	}
}

func TestSamsungRecentlySeen(t *testing.T) {
	if !SamsungRecentlySeen(time.Now()) {
		t.Fatal("recent contact should count as seen")
	}
	if SamsungRecentlySeen(time.Now().Add(-SamsungRecentWindow)) {
		t.Fatal("contact at window boundary should not count")
	}
	if SamsungRecentlySeen(time.Time{}) {
		t.Fatal("zero time should not count")
	}
}

func TestTouchSamsungUpdatesLastSeen(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	before := time.Now().Add(-time.Hour)
	reg.mu.Lock()
	reg.m["samsung:192.168.1.108"].LastSeen = before
	reg.mu.Unlock()

	if !reg.TouchSamsung("192.168.1.108", "content.json") {
		t.Fatal("TouchSamsung should succeed for registered frame")
	}
	d, ok := reg.Get("samsung:192.168.1.108")
	if !ok {
		t.Fatal("device missing")
	}
	if !d.LastSeen.After(before) {
		t.Fatalf("LastSeen not updated: %v", d.LastSeen)
	}
	if d.LastAction != "content.json" {
		t.Fatalf("LastAction: got %q", d.LastAction)
	}
	ApplySamsungConnected(d)
	if !d.Connected {
		t.Fatal("frame should be active after touch")
	}
}

func TestUpdateSamsungBattery(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	if !reg.UpdateSamsungBattery("192.168.1.108", 72, "usb") {
		t.Fatal("UpdateSamsungBattery should succeed")
	}
	d, ok := reg.Get("samsung:192.168.1.108")
	if !ok {
		t.Fatal("device missing")
	}
	if d.Battery != 72 {
		t.Fatalf("battery: got %d want 72", d.Battery)
	}
	if d.PowerSource != "usb" {
		t.Fatalf("power source: got %q want usb", d.PowerSource)
	}
	if d.LastAction != "mdc_battery" {
		t.Fatalf("LastAction: got %q", d.LastAction)
	}
	ApplySamsungConnected(d)
	if !d.Connected {
		t.Fatal("frame should be active after battery update")
	}
}

func TestApplySamsungConnectedStale(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	reg.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	reg.mu.Lock()
	reg.m["samsung:192.168.1.108"].LastSeen = time.Now().Add(-SamsungRecentWindow - time.Second)
	reg.mu.Unlock()
	d, _ := reg.Get("samsung:192.168.1.108")
	ApplySamsungConnected(d)
	if d.Connected {
		t.Fatal("stale frame should not be active")
	}
}
