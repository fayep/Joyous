package main

import "testing"

import "joyous-hub/inkjoybridge"

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
		{"aabbccddeeff", true},
		{"D0CF13EF408", false},
		{"AABBCCDDEEFF0", false},
		{"D0CF13EF408G", false},
		{"", false},
		{"AA:BB:CC:DD:EE:FF", false},
	}
	for _, c := range cases {
		got := inkjoybridge.IsFrameClientID(c.id)
		if got != c.want {
			t.Errorf("IsFrameClientID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

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
		mac, ok := inkjoybridge.ExtractTopicMAC(c.topic)
		if ok != c.wantOK || mac != c.wantMAC {
			t.Errorf("ExtractTopicMAC(%q) = (%q, %v), want (%q, %v)",
				c.topic, mac, ok, c.wantMAC, c.wantOK)
		}
	}
}

func TestTopicDirection(t *testing.T) {
	cases := []struct {
		topic string
		want  inkjoybridge.TopicDir
	}{
		{"/device/report/AABBCCDDEEFF", inkjoybridge.DirFrameToCloud},
		{"/inkjoyap/AABBCCDDEEFF", inkjoybridge.DirCloudToFrame},
		{"/other/topic", inkjoybridge.DirUnknown},
	}
	for _, c := range cases {
		got := inkjoybridge.TopicDirection(c.topic)
		if got != c.want {
			t.Errorf("TopicDirection(%q) = %v, want %v", c.topic, got, c.want)
		}
	}
}

func TestParseHeartPayload(t *testing.T) {
	payload := []byte(`{
		"action": "heart",
		"msgid": "1234",
		"stamac": "AA:BB:CC:DD:EE:FF",
		"data": {
			"battery": 85,
			"wifi_rssi": -62,
			"version": "0.5.6",
			"orientation": 1
		}
	}`)
	info, err := inkjoybridge.ParseHeartPayload(payload)
	if err != nil {
		t.Fatalf("ParseHeartPayload: %v", err)
	}
	if info.Battery != 85 || info.RSSI != -62 || info.Firmware != "0.5.6" {
		t.Errorf("heart info: %+v", info)
	}
}

func TestParseLoginPayload(t *testing.T) {
	payload := []byte(`{
		"action": "login",
		"msgid": "1000",
		"stamac": "AA:BB:CC:DD:EE:FF",
		"data": {
			"clientid": "AABBCCDDEEFF",
			"ver": "0.5.6"
		}
	}`)
	info, err := inkjoybridge.ParseLoginPayload(payload)
	if err != nil {
		t.Fatalf("ParseLoginPayload: %v", err)
	}
	if info.ClientID != "AABBCCDDEEFF" || info.Firmware != "0.5.6" {
		t.Errorf("login info: %+v", info)
	}
}

func TestShouldInterceptCloudToFrame(t *testing.T) {
	intercept := inkjoybridge.DefaultIntercept()
	for _, a := range []string{"mqtt_config", "wifi_sleep", "ota", "fpga"} {
		if !inkjoybridge.ShouldIntercept(a, intercept) {
			t.Errorf("ShouldIntercept(%q) should be true", a)
		}
	}
	for _, a := range []string{"heart_ack", "login_ack", "device_config", "play"} {
		if inkjoybridge.ShouldIntercept(a, intercept) {
			t.Errorf("ShouldIntercept(%q) should be false", a)
		}
	}
}

func TestDefaultDownstreamAllow(t *testing.T) {
	allow := inkjoybridge.DefaultDownstreamAllow()
	intercept := inkjoybridge.DefaultIntercept()
	for _, action := range []string{"login_ack", "heart_ack", "play", "shutdown_ack", "image_refresh_ack"} {
		if !allow.Allows(action) {
			t.Errorf("downstream should allow %q", action)
		}
	}
	if inkjoybridge.ShouldIntercept("play", intercept) {
		t.Error("cloud play should not be intercepted")
	}
}
