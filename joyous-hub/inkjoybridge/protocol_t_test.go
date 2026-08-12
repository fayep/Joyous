package inkjoybridge

import "testing"

func TestProtocolTFromVer(t *testing.T) {
	cases := []struct {
		ver  string
		want int
	}{
		{"M H:10 T:9 F:1.0.0", 9},
		{"M H:9 F:1.0.0", 0},          // pre-T firmware
		{"M H:2 F:0.5.6", 0},          // real InkJoy hardware, no H/T at all
		{"", 0},
	}
	for _, c := range cases {
		if got := ProtocolTFromVer(c.ver); got != c.want {
			t.Errorf("ProtocolTFromVer(%q) = %d, want %d", c.ver, got, c.want)
		}
	}
}

func TestPNGCapableLoginGate(t *testing.T) {
	info, err := ParseLoginPayload([]byte(`{"action":"login","data":{"ver":"M H:10 T:9 F:1.0.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !LocalOnlyHardware(info.HardwareModel) || info.ProtocolT < InkJoyPNGProtocolT {
		t.Fatalf("expected PNG-capable clone login, got model=%d T=%d", info.HardwareModel, info.ProtocolT)
	}

	old, err := ParseLoginPayload([]byte(`{"action":"login","data":{"ver":"M H:10 F:1.0.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if old.ProtocolT >= InkJoyPNGProtocolT {
		t.Fatalf("pre-T firmware must not read as PNG-capable, got T=%d", old.ProtocolT)
	}
}
