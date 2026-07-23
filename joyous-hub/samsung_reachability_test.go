package main

import (
	"errors"
	"testing"
	"time"
)

func TestIngestDiscoveredSamsungMergesNewIPByMAC(t *testing.T) {
	h := buildTestHub(t)
	old := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.1.108", "B0F2F657D5CD")
	macID := samsungRegistryID("B0F2F657D5CD")
	if _, ok := h.devices.Get(macID); !ok {
		t.Fatalf("expected MAC device %s (from %s)", macID, old.ID)
	}

	origQuery := samsungQueryMAC
	samsungQueryMAC = func(ip, pin string) (string, error) {
		if ip == "192.168.1.109" {
			return "B0F2F657D5CD", nil
		}
		return "", nil
	}
	t.Cleanup(func() { samsungQueryMAC = origQuery })

	d := h.ingestDiscoveredSamsung(SSDPDevice{IP: "192.168.1.109", Server: "Samsung MDC"})
	if d == nil {
		t.Fatal("expected ingest result")
	}
	if d.ID != macID {
		t.Fatalf("id=%q want %q", d.ID, macID)
	}
	if d.IP != "192.168.1.109" {
		t.Fatalf("ip=%q want 192.168.1.109", d.IP)
	}
	if _, ok := h.devices.Get(samsungProvisionalRegistryID("192.168.1.109")); ok {
		t.Fatal("provisional new-IP entry should be removed")
	}
}

func TestEnsureSamsungReachableReusesLiveIP(t *testing.T) {
	h := buildTestHub(t)
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.1.108", "B0F2F657D5CD")
	dev, _ := h.devices.Get(samsungRegistryID("B0F2F657D5CD"))

	origClassify := samsungClassifyIP
	origDiscover := samsungDiscoverLAN
	t.Cleanup(func() {
		samsungClassifyIP = origClassify
		samsungDiscoverLAN = origDiscover
	})
	samsungClassifyIP = func(ip string, timeout time.Duration) mdcTargetKind {
		if ip == "192.168.1.108" {
			return mdcTargetLive
		}
		return mdcTargetAbsent
	}
	samsungDiscoverLAN = func(timeout time.Duration) ([]SSDPDevice, int, error) {
		t.Fatal("should not rediscover when probe succeeds")
		return nil, 0, nil
	}

	got, err := h.ensureSamsungReachable(dev)
	if err != nil {
		t.Fatal(err)
	}
	if got.IP != "192.168.1.108" {
		t.Fatalf("ip=%q", got.IP)
	}
}

func TestEnsureSamsungReachableHostDownDoesNotRediscover(t *testing.T) {
	h := buildTestHub(t)
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.1.108", "B0F2F657D5CD")
	dev, _ := h.devices.Get(samsungRegistryID("B0F2F657D5CD"))

	origClassify := samsungClassifyIP
	origDiscover := samsungDiscoverLAN
	t.Cleanup(func() {
		samsungClassifyIP = origClassify
		samsungDiscoverLAN = origDiscover
	})
	samsungClassifyIP = func(ip string, timeout time.Duration) mdcTargetKind { return mdcTargetAbsent }
	samsungDiscoverLAN = func(timeout time.Duration) ([]SSDPDevice, int, error) {
		t.Fatal("host down must not rediscover")
		return nil, 0, nil
	}

	_, err := h.ensureSamsungReachable(dev)
	if !errors.Is(err, errMDCHostDown) {
		t.Fatalf("want errMDCHostDown, got %v", err)
	}
}

func TestEnsureSamsungReachableRediscoversAfterStaleIP(t *testing.T) {
	h := buildTestHub(t)
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.1.108", "B0F2F657D5CD")
	dev, _ := h.devices.Get(samsungRegistryID("B0F2F657D5CD"))

	origClassify := samsungClassifyIP
	origDiscover := samsungDiscoverLAN
	origQuery := samsungQueryMAC
	t.Cleanup(func() {
		samsungClassifyIP = origClassify
		samsungDiscoverLAN = origDiscover
		samsungQueryMAC = origQuery
	})
	samsungClassifyIP = func(ip string, timeout time.Duration) mdcTargetKind {
		if ip == "192.168.1.109" {
			return mdcTargetLive
		}
		return mdcTargetForeign
	}
	samsungDiscoverLAN = func(timeout time.Duration) ([]SSDPDevice, int, error) {
		return []SSDPDevice{{IP: "192.168.1.109", Server: "Samsung MDC"}}, 1, nil
	}
	samsungQueryMAC = func(ip, pin string) (string, error) {
		return "B0F2F657D5CD", nil
	}

	got, err := h.ensureSamsungReachable(dev)
	if err != nil {
		t.Fatal(err)
	}
	if got.IP != "192.168.1.109" {
		t.Fatalf("ip=%q want 192.168.1.109", got.IP)
	}
}

func TestEnsureSamsungReachableRetriesDiscover(t *testing.T) {
	h := buildTestHub(t)
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.1.108", "B0F2F657D5CD")
	dev, _ := h.devices.Get(samsungRegistryID("B0F2F657D5CD"))

	origClassify := samsungClassifyIP
	origDiscover := samsungDiscoverLAN
	origQuery := samsungQueryMAC
	origSleep := samsungDiscoverSleep
	t.Cleanup(func() {
		samsungClassifyIP = origClassify
		samsungDiscoverLAN = origDiscover
		samsungQueryMAC = origQuery
		samsungDiscoverSleep = origSleep
	})
	samsungClassifyIP = func(ip string, timeout time.Duration) mdcTargetKind {
		if ip == "192.168.1.109" {
			return mdcTargetLive
		}
		return mdcTargetForeign
	}
	samsungDiscoverSleep = func(time.Duration) {}
	calls := 0
	samsungDiscoverLAN = func(timeout time.Duration) ([]SSDPDevice, int, error) {
		calls++
		if calls < 2 {
			return nil, 0, nil
		}
		return []SSDPDevice{{IP: "192.168.1.109", Server: "Samsung MDC"}}, 1, nil
	}
	samsungQueryMAC = func(ip, pin string) (string, error) {
		return "B0F2F657D5CD", nil
	}

	got, err := h.ensureSamsungReachable(dev)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("discover calls=%d want 2", calls)
	}
	if got.IP != "192.168.1.109" {
		t.Fatalf("ip=%q want 192.168.1.109", got.IP)
	}
}

func TestEnsureSamsungWakeTargetUsesLastIPAfterDeepSleep(t *testing.T) {
	h := buildTestHub(t)
	_ = h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.50.221", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.50.221", "B0F2F657D5CD")
	h.devices.NoteSamsungSlept("192.168.50.221", true) // clears live IP, keeps LastIP
	dev, ok := h.devices.Get(samsungRegistryID("B0F2F657D5CD"))
	if !ok {
		t.Fatal("missing device")
	}
	if dev.IP != "" || dev.LastIP != "192.168.50.221" {
		t.Fatalf("after deep sleep IP=%q LastIP=%q", dev.IP, dev.LastIP)
	}

	oldDiscover := samsungDiscoverLAN
	t.Cleanup(func() { samsungDiscoverLAN = oldDiscover })
	samsungDiscoverLAN = func(timeout time.Duration) ([]SSDPDevice, int, error) {
		t.Fatal("wake must use LastIP, not rediscover")
		return nil, 0, nil
	}

	got, err := h.ensureSamsungWakeTarget(dev)
	if err != nil {
		t.Fatal(err)
	}
	if got.IP != "192.168.50.221" {
		t.Fatalf("wake target IP=%q want 192.168.50.221 from LastIP", got.IP)
	}
}

