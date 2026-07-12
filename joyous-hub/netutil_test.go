package main

import (
	"net"
	"strings"
	"testing"
)

func TestOutboundLocalIPForSameSubnet(t *testing.T) {
	ip := outboundLocalIPFor("192.168.1.108")
	if ip == nil {
		t.Skip("no matching local interface in test environment")
	}
	if !net.IP([]byte{192, 168, 51, 7}).Equal(ip) && ip.To4()[0] != 192 {
		t.Fatalf("unexpected local ip %v", ip)
	}
}

func TestPublicHTTPHostUsesLANIP(t *testing.T) {
	host := publicHTTPHost("hubhost.local:18080", "192.168.1.108")
	if host == "hubhost.local:18080" {
		t.Skip("no matching local interface in test environment")
	}
	if !strings.HasPrefix(host, "192.168.") {
		t.Fatalf("expected numeric host, got %q", host)
	}
	if !strings.HasSuffix(host, ":18080") {
		t.Fatalf("expected port preserved, got %q", host)
	}
}

func TestInkjoyPlayURLUsesHubCachePath(t *testing.T) {
	mac := "AA:BB:CC:DD:EE:FF"
	url := inkjoyPlayURL("http://joyous.zippysoft.com:18080", "192.168.1.108", mac, "abc123~tok-p", "192.168.51.7")
	if !strings.HasSuffix(url, "/inkjoy/AABBCCDDEEFF/abc123~tok-p.bin") {
		t.Fatalf("unexpected url %q", url)
	}
	if strings.Contains(url, "zippysoft") {
		t.Fatalf("play url must use LAN IP, got %q", url)
	}
	if !strings.Contains(url, "192.168.51.7:18080") {
		t.Fatalf("play url must use hub LAN IP, got %q", url)
	}
}

func TestInkjoyPlayContentHostPrefersFrameHubIP(t *testing.T) {
	host := inkjoyPlayContentHost("http://joyous.zippysoft.com:18080", "192.168.1.108", "192.168.51.7")
	if host != "192.168.51.7:18080" {
		t.Fatalf("expected frame hub IP, got %q", host)
	}
}

func TestSamsungMDCContentURL(t *testing.T) {
	url := samsungMDCContentURL("hubhost.local:18080", "192.168.1.108", "192-168-1-108")
	if strings.Contains(url, "hubhost.local") {
		t.Skip("no matching local interface in test environment")
	}
	if !strings.HasPrefix(url, "http://192.168.") {
		t.Fatalf("unexpected url %q", url)
	}
	if !strings.Contains(url, "/samsung/192-168-1-108/content.json") {
		t.Fatalf("unexpected url %q", url)
	}
}
