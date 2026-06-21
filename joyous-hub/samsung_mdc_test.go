package main

import "testing"

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
