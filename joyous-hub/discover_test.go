package main

import (
	"strings"
	"testing"
)

func TestClassifyFrameTypeSamsung(t *testing.T) {
	cases := []struct {
		name string
		dev  SSDPDevice
		want bool
	}{
		{"samsung server", SSDPDevice{Server: "Samsung E-Paper/1.0", IP: "192.168.1.1"}, true},
		{"epaper usn", SSDPDevice{USN: "uuid:xxx::urn:samsung:epaper:1", IP: "10.0.0.5"}, true},
		{"generic upnp headers only", SSDPDevice{
			IP:     "192.168.1.110",
			Server: "Unspecified, UPnP/1.0, Unspecified",
			ST:     "upnp:rootdevice",
		}, false},
		{"mdc banner discover", SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC", USN: "mdc:192.168.1.108"}, true},
		{"random tv", SSDPDevice{Server: "LG WebOS TV", IP: "192.168.1.2"}, false},
		{"hdhomerun excluded", SSDPDevice{Server: "HDHomeRun/1.0 UPnP/1.0", IP: "192.168.1.3"}, false},
	}
	for _, c := range cases {
		_, ok := ClassifyFrameType(c.dev)
		if ok != c.want {
			t.Errorf("%s: got ok=%v want %v", c.name, ok, c.want)
		}
	}
}

func TestLocationIndicatesSamsung(t *testing.T) {
	if !locationIndicatesSamsung(`<manufacturer>Samsung Electronics</manufacturer>`) {
		t.Fatal("expected samsung manufacturer match")
	}
	if locationIndicatesSamsung(`<modelName>HDHomeRun CONNECT</modelName>`) {
		t.Fatal("unexpected match")
	}
}

func TestSubnetRange(t *testing.T) {
	got := subnetRange("192.168.50")
	if len(got) != 254 || got[0] != "192.168.1.1" || got[253] != "192.168.50.254" {
		t.Fatalf("bare prefix: len=%d first=%q last=%q", len(got), got[0], got[len(got)-1])
	}
	got23 := subnetRange("192.168.50.0/23")
	if len(got23) != 510 {
		t.Fatalf("/23 host count: got %d want 510", len(got23))
	}
	if got23[0] != "192.168.1.1" || got23[len(got23)-1] != "192.168.51.254" {
		t.Fatalf("/23 bounds: first=%q last=%q", got23[0], got23[len(got23)-1])
	}
}

func TestParseDiscoverSubnets(t *testing.T) {
	got := parseDiscoverSubnets("192.168.50.0/24, 192.168.51.0/24")
	if len(got) != 2 || got[0] != "192.168.50.0/24" {
		t.Fatalf("got %v", got)
	}
}

func TestUpsertSamsungDevice(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	d := reg.UpsertSamsung(SSDPDevice{
		IP:     "192.168.1.101",
		USN:    "uuid:abc::urn:samsung:device:1",
		Server: "Samsung/MDC",
	})
	if d.Type != DeviceTypeSamsung {
		t.Fatalf("type %q", d.Type)
	}
	if d.ID != "samsung:192.168.1.101" {
		t.Fatalf("id %q", d.ID)
	}
	if d.Name != "Samsung · 192.168.1.101" {
		t.Fatalf("name %q", d.Name)
	}
	if SamsungFrameID(d) != "192-168-1-101" {
		t.Fatalf("frame id %q", SamsungFrameID(d))
	}
	reg.SetName(d.ID, "Living Room")
	d2 := reg.UpsertSamsung(SSDPDevice{
		IP:     "192.168.1.101",
		USN:    "uuid:abc::urn:samsung:device:1",
		Server: "Samsung/MDC",
	})
	if d2.Name != "Living Room" {
		t.Fatalf("rediscover overwrote name: %q", d2.Name)
	}
	devs := reg.List()
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
}

func TestSSDPDisplayName(t *testing.T) {
	tests := []struct {
		name string
		dev  SSDPDevice
		want string
	}{
		{"mdc sweep", SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"}, "Samsung · 192.168.1.108"},
		{"em32 ssdp", SSDPDevice{IP: "10.0.0.5", Server: "Samsung/EM32DX", USN: "uuid:x::urn:samsung:em32"}, "EM32DX · 10.0.0.5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.dev.DisplayName(); got != tc.want {
				t.Fatalf("DisplayName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMigrateLegacyInkJoyDevice(t *testing.T) {
	d := &Device{MAC: "AABBCCDDEEFF"}
	migrateDevice(d)
	if d.ID != "AABBCCDDEEFF" || d.Type != DeviceTypeInkJoy {
		t.Fatalf("migrate: id=%q type=%q", d.ID, d.Type)
	}
}

func TestMDCContentDownloadPacket(t *testing.T) {
	pkt, err := mdcContentDownloadPacket("http://192.168.1.1/samsung/x/content.json")
	if err != nil {
		t.Fatal(err)
	}
	if pkt[0] != 0xAA || pkt[1] != 0xC7 || pkt[4] != 0x53 || pkt[5] != 0x80 {
		t.Fatalf("bad header: % x", pkt[:8])
	}
}

func TestParseMDCResponse(t *testing.T) {
	if err := parseMDCResponse([]byte{0xAA, 0xFF, 0x00, 0x04, 'A', 0xC7, 0x53, 0x01, 0x6c}); err != nil {
		t.Fatalf("ACK: %v", err)
	}
	if err := parseMDCResponse([]byte{0xAA, 0xFF, 0x00, 0x04, 'N', 0xC7, 0x53, 0x01, 0x6c}); err == nil {
		t.Fatal("expected NAK error")
	}
}

func TestBuildContentJSON(t *testing.T) {
	b := buildContentJSON("http://host/img.png", "A1B2C3D4-E5F6-7890-ABCD-EF1234567890", 12345)
	if len(b) == 0 {
		t.Fatal("empty json")
	}
	if !strings.Contains(string(b), "com.samsung.ios.ePaper") {
		t.Fatal("missing program_id")
	}
	if !strings.Contains(string(b), `http:\/\/host\/img.png`) {
		t.Fatal("expected escaped slashes in image_url")
	}
}
