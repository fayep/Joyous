package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"joyous-hub/inkjoybridge"
)

func TestResolveCaptureDir(t *testing.T) {
	if got := resolveCaptureDir("", "/data"); got != filepath.Join("/data", "capture") {
		t.Fatalf("auto: got %q", got)
	}
	if got := resolveCaptureDir("auto", "/data"); got != filepath.Join("/data", "capture") {
		t.Fatalf("auto explicit: got %q", got)
	}
	if got := resolveCaptureDir("/tmp/cap", "/data"); got != "/tmp/cap" {
		t.Fatalf("custom: got %q", got)
	}
	if got := resolveCaptureDir("off", "/data"); got != "" {
		t.Fatalf("off: got %q", got)
	}
}

func TestMessageCaptureRecord(t *testing.T) {
	dir := t.TempDir()
	upstream := inkjoybridge.ParseAllowList("login,heart")
	downstream := inkjoybridge.ParseAllowList("play,login_ack")
	intercept := inkjoybridge.ParseAllowList("mqtt_config")
	c := NewMessageCapture(dir, upstream, downstream, intercept)

	if err := c.RecordUpstream("AABBCCDDEEFF", "login", []byte(`{"action":"login"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, captureDirFrameToBroker, "login.jsonl")); err == nil {
		t.Fatal("known upstream action should not be captured")
	}

	payload := []byte(`{"action":"device_config_ack","data":{"result":0}}`)
	if err := c.RecordUpstream("AABBCCDDEEFF", "device_config_ack", payload); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, captureDirFrameToBroker, "device_config_ack.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &rec); err != nil {
		t.Fatal(err)
	}
	if rec["mac"] != "AABBCCDDEEFF" || rec["direction"] != captureDirFrameToBroker {
		t.Fatalf("record: %+v", rec)
	}

	if err := c.RecordDownstream("AABBCCDDEEFF", "mqtt_config", []byte(`{"action":"mqtt_config"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, captureDirBrokerToFrame, "mqtt_config.jsonl")); err == nil {
		t.Fatal("intercepted action should not be captured")
	}

	if err := c.RecordDownstream("AABBCCDDEEFF", "wifimode", []byte(`{"action":"wifimode"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, captureDirBrokerToFrame, "wifimode.jsonl")); err != nil {
		t.Fatal("unknown downstream should be captured", err)
	}
}

func TestMessageCaptureRecordIntercepted(t *testing.T) {
	dir := t.TempDir()
	c := NewMessageCapture(dir, inkjoybridge.ParseAllowList(""), inkjoybridge.ParseAllowList(""), inkjoybridge.ParseAllowList(""))
	payload := []byte(`{"action":"mqtt_config","data":{"host":"x"}}`)
	if err := c.RecordIntercepted("AABBCCDDEEFF", "mqtt_config", payload); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, captureDirIntercept, "mqtt_config.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &rec); err != nil {
		t.Fatal(err)
	}
	if rec["mac"] != "AABBCCDDEEFF" || rec["direction"] != captureDirIntercept {
		t.Fatalf("record: %+v", rec)
	}
}

func TestSanitizeCaptureName(t *testing.T) {
	if sanitizeCaptureName("") != "_empty" {
		t.Fatal()
	}
	if sanitizeCaptureName("device/config") != "device_config" {
		t.Fatal()
	}
}
