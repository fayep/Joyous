package main

import (
	"net"
	"testing"
)

func TestIPv4DirectedBroadcast(t *testing.T) {
	_, ipnet, err := net.ParseCIDR("192.168.50.0/23")
	if err != nil {
		t.Fatal(err)
	}
	bc := ipv4DirectedBroadcast(ipnet)
	if bc.String() != "192.168.51.255" {
		t.Fatalf("got %s want 192.168.51.255", bc)
	}

	_, ipnet24, err := net.ParseCIDR("192.168.50.0/24")
	if err != nil {
		t.Fatal(err)
	}
	bc24 := ipv4DirectedBroadcast(ipnet24)
	if bc24.String() != "192.168.50.255" {
		t.Fatalf("got %s want 192.168.50.255", bc24)
	}
}

func TestWolBroadcastForFrame(t *testing.T) {
	_, ipnet, err := net.ParseCIDR("192.168.50.0/23")
	if err != nil {
		t.Fatal(err)
	}
	bc := ipv4DirectedBroadcast(ipnet)
	if !bc.Equal(net.IPv4(192, 168, 51, 255)) {
		t.Fatalf("directed broadcast for /23: got %s", bc)
	}
	// Frame at .50.x and hub at .51.x share the same /23 broadcast.
	if !ipnet.Contains(net.ParseIP("192.168.50.108")) || !ipnet.Contains(net.ParseIP("192.168.51.7")) {
		t.Fatal("test CIDR should cover both .50 and .51 hosts")
	}
}
