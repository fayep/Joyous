package main

import (
	"encoding/json"
	"testing"
)

// TestParseMQTTConfig: parse a real mqtt_config payload into UpstreamConfig.
func TestParseMQTTConfig(t *testing.T) {
	payload := []byte(`{
		"action": "mqtt_config",
		"msgid": "1234567890",
		"stamac": "AA:BB:CC:DD:EE:FF",
		"data": {
			"host": "192.168.1.50",
			"port": 1883,
			"usr":  "myuser",
			"pwd":  "mypassword"
		}
	}`)

	cfg, err := ParseMQTTConfig(payload)
	if err != nil {
		t.Fatalf("ParseMQTTConfig: %v", err)
	}
	if cfg.Host != "192.168.1.50" {
		t.Errorf("Host: got %q want %q", cfg.Host, "192.168.1.50")
	}
	if cfg.Port != 1883 {
		t.Errorf("Port: got %d want 1883", cfg.Port)
	}
	if cfg.Username != "myuser" {
		t.Errorf("Username: got %q want %q", cfg.Username, "myuser")
	}
	if cfg.Password != "mypassword" {
		t.Errorf("Password: got %q want %q", cfg.Password, "mypassword")
	}
}

// TestParseMQTTConfigMissingFields: missing fields return an error.
func TestParseMQTTConfigMissingFields(t *testing.T) {
	cases := []string{
		`{"action":"mqtt_config","data":{}}`,
		`{"action":"mqtt_config","data":{"host":"x"}}`,
		`{"action":"mqtt_config"}`,
	}
	for _, c := range cases {
		_, err := ParseMQTTConfig([]byte(c))
		if err == nil {
			t.Errorf("expected error for payload %s", c)
		}
	}
}

// TestUpstreamAllowDefault: default frame→broker list.
func TestUpstreamAllowDefault(t *testing.T) {
	allow := DefaultUpstreamAllow()

	mustPass := []string{"login", "heart", "play_ack", "fpga_ota_ack", "shutdown", "image_refresh_ack", "ota_ack", "wifi_sleep_ack", "mqtt_config_ack"}
	mustBlock := []string{"image_refresh", "play", "ota", "mqtt_config", "shutdown_ack", "fpga"}

	for _, action := range mustPass {
		if !allow.Allows(action) {
			t.Errorf("default allow list should pass %q", action)
		}
	}
	for _, action := range mustBlock {
		if allow.Allows(action) {
			t.Errorf("default allow list should block %q", action)
		}
	}
}

// TestUpstreamAllowCustom: custom list from comma-separated string.
func TestUpstreamAllowCustom(t *testing.T) {
	allow := ParseUpstreamAllow("play,ota,heart")

	if !allow.Allows("play") || !allow.Allows("ota") || !allow.Allows("heart") {
		t.Error("custom allow list should pass configured actions")
	}
	if allow.Allows("login") {
		t.Error("custom allow list should block actions not in the list")
	}
}

// TestBuildMQTTConfigPayload: payload built for sending mqtt_config to a frame.
func TestBuildMQTTConfigPayload(t *testing.T) {
	mac := "AABBCCDDEEFF"
	cfg := UpstreamConfig{Host: "10.0.0.1", Port: 1883, Username: "u", Password: "p"}
	payload := BuildMQTTConfigPayload(mac, cfg)

	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg["action"] != "mqtt_config" {
		t.Errorf("action: got %v", msg["action"])
	}
	data, ok := msg["data"].(map[string]any)
	if !ok {
		t.Fatal("data field missing or wrong type")
	}
	if data["host"] != "10.0.0.1" {
		t.Errorf("data.host: got %v", data["host"])
	}
	if data["port"] != float64(1883) {
		t.Errorf("data.port: got %v", data["port"])
	}
}

func TestBuildAckPayloadFor(t *testing.T) {
	payload := buildAckPayloadFor("AABBCCDDEEFF", "wifi_sleep_ack", "1781902333622", nil)
	var msg struct {
		Action   string `json:"action"`
		Clientid string `json:"clientid"`
		Stamac   string `json:"stamac"`
		Data     struct {
			AckMsgid string `json:"ack_msgid"`
			Result   int    `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg.Action != "wifi_sleep_ack" {
		t.Errorf("action: got %q", msg.Action)
	}
	if msg.Clientid != "AABBCCDDEEFF" || msg.Stamac != "AABBCCDDEEFF" {
		t.Errorf("clientid/stamac: got %q / %q", msg.Clientid, msg.Stamac)
	}
	if msg.Data.AckMsgid != "1781902333622" {
		t.Errorf("ack_msgid: got %q", msg.Data.AckMsgid)
	}
	if msg.Data.Result != 106 {
		t.Errorf("result: got %d want 106", msg.Data.Result)
	}
}

func TestMQTTMsgid(t *testing.T) {
	if got := mqttMsgid([]byte(`{"action":"wifi_sleep","msgid":"1781902333622"}`)); got != "1781902333622" {
		t.Errorf("string msgid: got %q", got)
	}
	if got := mqttMsgid([]byte(`{"action":"wifi_sleep","msgid":1781902333622}`)); got != "1781902333622" {
		t.Errorf("numeric msgid: got %q", got)
	}
}
