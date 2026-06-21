package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	captureDirFrameToBroker = "frame-to-broker"
	captureDirBrokerToFrame = "broker-to-frame"
	captureDirIntercept     = "intercept"
)

// MessageCapture appends unrecognized MQTT payloads to per-action JSONL files.
type MessageCapture struct {
	dir             string
	upstreamKnown   AllowList
	downstreamKnown AllowList
	intercept       AllowList
	mu              sync.Mutex
}

// NewMessageCapture stores unknown messages under dir. Pass empty dir to disable.
func NewMessageCapture(dir string, upstream, downstream, intercept AllowList) *MessageCapture {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	return &MessageCapture{
		dir:             dir,
		upstreamKnown:   upstream,
		downstreamKnown: downstream,
		intercept:       intercept,
	}
}

func resolveCaptureDir(configured, dataDir string) string {
	configured = strings.TrimSpace(configured)
	switch strings.ToLower(configured) {
	case "", "auto":
		return filepath.Join(dataDir, "capture")
	case "off", "none", "disable", "disabled":
		return ""
	default:
		return configured
	}
}

func (c *MessageCapture) recognizedUpstream(action string) bool {
	if action == "" {
		return false
	}
	return c.upstreamKnown.Allows(action)
}

func (c *MessageCapture) recognizedDownstream(action string) bool {
	if action == "" {
		return false
	}
	return c.downstreamKnown.Allows(action) || c.intercept.Allows(action)
}

// RecordUpstream captures an unrecognized frame→broker message.
func (c *MessageCapture) RecordUpstream(mac, action string, payload []byte) error {
	if c == nil || c.recognizedUpstream(action) {
		return nil
	}
	return c.record(captureDirFrameToBroker, mac, action, payload)
}

// RecordDownstream captures an unrecognized broker→frame message.
func (c *MessageCapture) RecordDownstream(mac, action string, payload []byte) error {
	if c == nil || c.recognizedDownstream(action) {
		return nil
	}
	return c.record(captureDirBrokerToFrame, mac, action, payload)
}

// RecordIntercepted captures a cloud→frame message handled by the intercept list.
func (c *MessageCapture) RecordIntercepted(mac, action string, payload []byte) error {
	if c == nil {
		return nil
	}
	return c.record(captureDirIntercept, mac, action, payload)
}

func (c *MessageCapture) record(direction, mac, action string, payload []byte) error {
	rec := map[string]any{
		"captured_at": time.Now().UTC().Format(time.RFC3339Nano),
		"direction":   direction,
		"mac":         mac,
		"action":      action,
	}
	if json.Valid(payload) {
		rec["payload"] = json.RawMessage(payload)
	} else {
		rec["payload_raw"] = string(payload)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	dir := filepath.Join(c.dir, direction)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	name := sanitizeCaptureName(action) + ".jsonl"
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func sanitizeCaptureName(action string) string {
	if action == "" {
		return "_empty"
	}
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, action)
	if safe == "" {
		return "_unknown"
	}
	return safe
}

func mqttAction(payload []byte) string {
	var env struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return ""
	}
	return env.Action
}

func captureWriteErr(where string, err error) {
	if err != nil {
		log.Printf("capture %s: %v", where, err)
	}
}

func logCaptureReady(dir string) {
	log.Printf("capture: MQTT messages → %s/{frame-to-broker,broker-to-frame,intercept}/", dir)
}
