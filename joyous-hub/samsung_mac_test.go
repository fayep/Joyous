package main

import (
	"testing"
)

func TestNormalizeSamsungMAC(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"b0:f2:f6:57:d5:cd", "B0F2F657D5CD", true},
		{"B0-F2-F6-57-D5-CD", "B0F2F657D5CD", true},
		{"B0F2F657D5CD", "B0F2F657D5CD", true},
		{"", "", false},
		{"not-a-mac", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeSamsungMAC(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("normalizeSamsungMAC(%q) = %q, %v want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSamsungFrameIDUsesMAC(t *testing.T) {
	dev := &Device{
		ID:     "samsung:192.168.1.108",
		Type:   DeviceTypeSamsung,
		IP:     "192.168.1.108",
		MDCMAC: "B0F2F657D5CD",
	}
	if got := SamsungFrameID(dev); got != "B0F2F657D5CD" {
		t.Fatalf("SamsungFrameID = %q want B0F2F657D5CD", got)
	}
}

func TestParseMDCWifiMACPayloadASCII(t *testing.T) {
	payload := []byte("b0f2f657d5cd")
	got, err := parseMDCWifiMACPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got != "B0F2F657D5CD" {
		t.Fatalf("got %q", got)
	}
}

func TestParseMDCWifiMACPayloadRaw(t *testing.T) {
	payload := []byte{0xb0, 0xf2, 0xf6, 0x57, 0xd5, 0xcd}
	got, err := parseMDCWifiMACPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got != "B0F2F657D5CD" {
		t.Fatalf("got %q", got)
	}
}

func TestMigrateSamsungFrameStore(t *testing.T) {
	dir := t.TempDir()
	store := NewSamsungStore(dir)
	h := &Hub{
		samsung:        store,
		samsungAliases: loadSamsungFrameAliases(dir),
	}
	oldID := "192-168-1-108"
	newID := "B0F2F657D5CD"
	cfg := defaultSamsungConfig(oldID)
	cfg.WifiMAC = "B0F2F657D5CD"
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.writePNGLocked(oldID, testPNG()); err != nil {
		t.Fatal(err)
	}
	if err := h.migrateSamsungFrameStore(oldID, newID, "B0F2F657D5CD"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadConfig(newID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := store.PNGInfo(newID); !ok {
		t.Fatal("expected png at new id")
	}
	if c := h.samsungAliases.canonical(oldID); c != newID {
		t.Fatalf("alias %q -> %q", oldID, c)
	}
}

func TestResolveSamsungFrameIDAlias(t *testing.T) {
	dir := t.TempDir()
	h := &Hub{
		samsungAliases: loadSamsungFrameAliases(dir),
	}
	h.samsungAliases.add("192-168-1-108", "B0F2F657D5CD")
	if got := h.resolveSamsungFrameID("192-168-1-108"); got != "B0F2F657D5CD" {
		t.Fatalf("resolve = %q", got)
	}
}

func TestMigrateSamsungRegistryToMAC(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	d := reg.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	reg.TouchSamsung("192.168.1.108", "mdc_session")
	merged := reg.MigrateSamsungToMAC(d.ID, "B0F2F657D5CD")
	if merged == nil {
		t.Fatal("expected merged device")
	}
	if merged.ID != "samsung:B0F2F657D5CD" {
		t.Fatalf("id %q", merged.ID)
	}
	if merged.IP != "192.168.1.108" {
		t.Fatalf("ip %q", merged.IP)
	}
	if SamsungFrameID(merged) != "B0F2F657D5CD" {
		t.Fatalf("frame id %q", SamsungFrameID(merged))
	}
	devs := reg.List()
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
}

func TestReconcileSamsungRegistryMACBatteryHistory(t *testing.T) {
	dir := t.TempDir()
	h := &Hub{
		devices:        NewDeviceRegistry(dir),
		samsungBattery: NewSamsungBatteryStore(dir),
		samsung:        NewSamsungStore(dir),
		samsungAliases: loadSamsungFrameAliases(dir),
	}
	d := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	if !h.devices.SetSamsungMAC(d.ID, "B0F2F657D5CD") {
		t.Fatal("SetSamsungMAC")
	}
	macRegistryID := "samsung:B0F2F657D5CD"

	h.samsungBattery.Record(macRegistryID, 92, "usb", samsungBatteryPreSleep)
	_ = h.samsungBattery.Save()

	if sum := h.samsungBatterySummary(d.ID, 5); sum.Samples != 0 {
		t.Fatalf("legacy id samples before reconcile: %d", sum.Samples)
	}

	dev, ok := h.devices.Get(d.ID)
	if !ok {
		t.Fatal("device not found")
	}
	if !h.reconcileSamsungRegistryMAC(*dev) {
		t.Fatal("expected reconcile")
	}
	merged, ok := h.devices.Get(macRegistryID)
	if !ok {
		t.Fatal("expected MAC registry id")
	}
	if sum := h.samsungBatterySummary(merged.ID, 5); sum.Samples != 1 {
		t.Fatalf("samples after reconcile: %d want 1", sum.Samples)
	}
}

func TestStartupMigrationReconcilesBatteryHistory(t *testing.T) {
	dir := t.TempDir()
	h := &Hub{
		devices:        NewDeviceRegistry(dir),
		samsungBattery: NewSamsungBatteryStore(dir),
		samsung:        NewSamsungStore(dir),
		samsungAliases: loadSamsungFrameAliases(dir),
	}
	d := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.devices.SetSamsungMAC(d.ID, "B0F2F657D5CD")

	h.samsungBattery.Record(d.ID, 88, "usb", samsungBatteryPreSleep)
	h.samsungBattery.Record(d.ID, 85, "usb", samsungBatteryPreSleep)
	_ = h.samsungBattery.Save()

	h.migrateSamsungFramesOnStartup()

	macRegistryID := "samsung:B0F2F657D5CD"
	sum := h.samsungBatterySummary(macRegistryID, 5)
	if sum.Samples != 2 {
		t.Fatalf("samples: got %d want 2", sum.Samples)
	}
	if sum := h.samsungBatterySummary(d.ID, 5); sum.Samples != 0 {
		t.Fatalf("legacy key still has %d samples", sum.Samples)
	}
}
