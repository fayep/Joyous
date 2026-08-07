package inkjoybridge

import "testing"

func TestLocalOnlyHardware(t *testing.T) {
	if !LocalOnlyHardware(HardwareModelSeeed) {
		t.Fatal("Seeed H:9 should be local-only")
	}
	if LocalOnlyHardware(2) || LocalOnlyHardware(3) || LocalOnlyHardware(0) {
		t.Fatal("real InkJoy models must still publish upstream")
	}
}

func TestSeeedLoginMarksLocalOnly(t *testing.T) {
	bs := &bridgeSet{}
	info, err := ParseLoginPayload([]byte(`{"action":"login","data":{"ver":"M H:9 F:1.0.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !LocalOnlyHardware(info.HardwareModel) {
		t.Fatalf("model=%d", info.HardwareModel)
	}
	bs.markLocalOnly("94A990D08BE0")
	if !bs.isLocalOnly("94A990D08BE0") {
		t.Fatal("expected local-only after Seeed login")
	}
	if bs.isLocalOnly("AABBCCDDEEFF") {
		t.Fatal("other MACs must not be local-only")
	}
}
