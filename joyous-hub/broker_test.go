package main

import "testing"

// TestIsFrameClientID: 12 hex chars accepted, anything else rejected.
func TestIsFrameClientID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"AABBCCDDEEFF", true},
		{"30EDA0E3FBE8", true},
		{"000000000000", true},
		{"AABBCCDDEEFF", true},
		{"aabbccddeeff", true},  // lowercase also valid
		{"D0CF13EF408", false},  // 11 chars
		{"AABBCCDDEEFF0", false}, // 13 chars
		{"D0CF13EF408G", false},  // non-hex char
		{"", false},
		{"AA:BB:CC:DD:EE:FF", false}, // colons not allowed
	}
	for _, c := range cases {
		got := IsFrameClientID(c.id)
		if got != c.want {
			t.Errorf("IsFrameClientID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

// TestExtractTopicMAC: pull MAC out of known topic patterns.
func TestExtractTopicMAC(t *testing.T) {
	cases := []struct {
		topic   string
		wantMAC string
		wantOK  bool
	}{
		{"/device/report/AABBCCDDEEFF", "AABBCCDDEEFF", true},
		{"/inkjoyap/AABBCCDDEEFF", "AABBCCDDEEFF", true},
		{"/device/report/30EDA0E3FBE8", "30EDA0E3FBE8", true},
		{"/inkjoyap/", "", false},
		{"/other/topic", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		mac, ok := ExtractTopicMAC(c.topic)
		if ok != c.wantOK || mac != c.wantMAC {
			t.Errorf("ExtractTopicMAC(%q) = (%q, %v), want (%q, %v)",
				c.topic, mac, ok, c.wantMAC, c.wantOK)
		}
	}
}

// TestTopicDirection: classify topic as frame→cloud or cloud→frame.
func TestTopicDirection(t *testing.T) {
	cases := []struct {
		topic string
		want  TopicDir
	}{
		{"/device/report/AABBCCDDEEFF", DirFrameToCloud},
		{"/inkjoyap/AABBCCDDEEFF", DirCloudToFrame},
		{"/other/topic", DirUnknown},
	}
	for _, c := range cases {
		got := TopicDirection(c.topic)
		if got != c.want {
			t.Errorf("TopicDirection(%q) = %v, want %v", c.topic, got, c.want)
		}
	}
}

// TestParseHeartPayload: extract device telemetry from a heart message.
func TestParseHeartPayload(t *testing.T) {
	payload := []byte(`{
		"action": "heart",
		"msgid": "1234",
		"stamac": "AA:BB:CC:DD:EE:FF",
		"data": {
			"battery": 85,
			"rssi": -62,
			"firmware": "0.5.6",
			"orientation": 1
		}
	}`)
	info, err := ParseHeartPayload(payload)
	if err != nil {
		t.Fatalf("ParseHeartPayload: %v", err)
	}
	if info.Battery != 85 {
		t.Errorf("Battery: got %d want 85", info.Battery)
	}
	if info.RSSI != -62 {
		t.Errorf("RSSI: got %d want -62", info.RSSI)
	}
	if info.Firmware != "0.5.6" {
		t.Errorf("Firmware: got %q want %q", info.Firmware, "0.5.6")
	}
}

// TestParseLoginPayload: extract clientid from login message.
func TestParseLoginPayload(t *testing.T) {
	payload := []byte(`{
		"action": "login",
		"msgid": "1000",
		"stamac": "AA:BB:CC:DD:EE:FF",
		"data": {
			"clientid": "AABBCCDDEEFF",
			"firmware": "0.5.6"
		}
	}`)
	info, err := ParseLoginPayload(payload)
	if err != nil {
		t.Fatalf("ParseLoginPayload: %v", err)
	}
	if info.ClientID != "AABBCCDDEEFF" {
		t.Errorf("ClientID: got %q", info.ClientID)
	}
	if info.Firmware != "0.5.6" {
		t.Errorf("Firmware: got %q", info.Firmware)
	}
}

// TestShouldInterceptCloudToFrame: mqtt_config is always intercepted; others pass through.
func TestShouldInterceptCloudToFrame(t *testing.T) {
	intercept := []string{"mqtt_config"}
	passThrough := []string{"play", "ota", "heart_ack", "login_ack", "device_config", "fpga"}

	for _, a := range intercept {
		if !ShouldIntercept(a) {
			t.Errorf("ShouldIntercept(%q) should be true", a)
		}
	}
	for _, a := range passThrough {
		if ShouldIntercept(a) {
			t.Errorf("ShouldIntercept(%q) should be false", a)
		}
	}
}
