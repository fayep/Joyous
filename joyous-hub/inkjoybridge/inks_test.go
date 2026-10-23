package inkjoybridge

import "testing"

func TestPanelInkCount(t *testing.T) {
	for _, c := range []struct {
		model    int
		firmware string
		want     int
	}{
		{HardwareModelInkJoy28, "1.3.7", 8},
		{HardwareModelInkJoy28, "M H:3 F:1.3.8 B:72", 8},
		{HardwareModelInkJoy28, "", 8},
		{HardwareModelInkJoy10, "1.1.0", 6},
		{HardwareModelInkJoy10, "M H:2 F:1.1.0", 6},
		{HardwareModelSeeed, "1.0.0", 6},
		{0, "", 6},
	} {
		if got := PanelInkCount(c.model, c.firmware); got != c.want {
			t.Errorf("PanelInkCount(%d, %q) = %d, want %d", c.model, c.firmware, got, c.want)
		}
	}
}

// A firmware-gated row applies from its version on, in both Firmware forms.
func TestPanelInkCountFirmwareGate(t *testing.T) {
	saved := inkSets[HardwareModelInkJoy10]
	t.Cleanup(func() { inkSets[HardwareModelInkJoy10] = saved })
	inkSets[HardwareModelInkJoy10] = append(saved, struct {
		minFirmware string
		inks        int
	}{"1.4.0", 8})

	for fw, want := range map[string]int{
		"1.3.9":              6,
		"1.4.0":              8,
		"1.10.0":             8,
		"M H:2 F:1.3.12 B:7": 6,
		"M H:2 F:1.4.1":      8,
		"":                   6,
	} {
		if got := PanelInkCount(HardwareModelInkJoy10, fw); got != want {
			t.Errorf("10\" firmware %q: %d inks, want %d", fw, got, want)
		}
	}
}
