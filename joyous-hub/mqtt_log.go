package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"joyous-hub/inkjoybridge"
	"joyous-hub/protocol"
)

const mqttLogBodyMax = 4096

// MQTTLogEntry is one MQTT message shown in the web UI.
type MQTTLogEntry struct {
	Time   string `json:"time"`
	Dir    string `json:"dir"`
	Topic  string `json:"topic"`
	Action string `json:"action,omitempty"`
	Note   string `json:"note,omitempty"`
	Body   string `json:"body"`
}

// MQTTLogBuffer keeps the last N messages per side for the web UI.
type MQTTLogBuffer struct {
	mu       sync.RWMutex
	max      int
	local    []MQTTLogEntry
	upstream []MQTTLogEntry
}

// NewMQTTLogBuffer creates a ring buffer with max entries per side.
func NewMQTTLogBuffer(max int) *MQTTLogBuffer {
	if max <= 0 {
		max = 20
	}
	return &MQTTLogBuffer{max: max}
}

// AddLocal records frame↔hub traffic.
func (b *MQTTLogBuffer) AddLocal(dir, topic string, payload []byte, note string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.push(&b.local, b.entry(dir, topic, payload, note))
	b.mu.Unlock()
}

// AddUpstream records the upstream column (hub→bridge on the hub broker, or hub→cloud on inkjoy-bridge).
func (b *MQTTLogBuffer) AddUpstream(dir, topic string, payload []byte, note string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.push(&b.upstream, b.entry(dir, topic, payload, note))
	b.mu.Unlock()
}

// AddJoyousBridgeToHub records bridge→hub traffic on the Joyous MQTT broker.
func (b *MQTTLogBuffer) AddJoyousBridgeToHub(topic string, payload []byte) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.pushWithEviction(&b.local, b.joyousEntry("bridge→hub", topic, payload), joyousMQTTNoisy)
	b.mu.Unlock()
}

// AddJoyousHubToBridge records hub→bridge traffic on the Joyous MQTT broker.
func (b *MQTTLogBuffer) AddJoyousHubToBridge(topic string, payload []byte) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.pushWithEviction(&b.upstream, b.joyousEntry("hub→bridge", topic, payload), joyousMQTTNoisy)
	b.mu.Unlock()
}

func joyousMQTTNoisy(action string) bool {
	if strings.HasPrefix(action, "ui.") {
		return true
	}
	switch action {
	case protocol.TypeHello, protocol.TypeDevices, protocol.TypeUIState:
		return true
	}
	return false
}

func inkjoyFrameMQTTNoisy(action string) bool {
	switch action {
	case "login", "heart", "login_ack", "heart_ack":
		return true
	}
	return false
}

func (b *MQTTLogBuffer) push(list *[]MQTTLogEntry, entry MQTTLogEntry) {
	b.pushWithEviction(list, entry, inkjoyFrameMQTTNoisy)
}

func (b *MQTTLogBuffer) pushWithEviction(list *[]MQTTLogEntry, entry MQTTLogEntry, noisyFn func(string) bool) {
	*list = append(*list, entry)
	for len(*list) > b.max {
		if noisyFn != nil && evictOldestNoisy(list, noisyFn) {
			continue
		}
		*list = (*list)[1:]
	}
}

func evictOldestNoisy(list *[]MQTTLogEntry, noisyFn func(string) bool) bool {
	for i, e := range *list {
		if noisyFn(e.Action) {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return true
		}
	}
	return false
}

func (b *MQTTLogBuffer) entry(dir, topic string, payload []byte, note string) MQTTLogEntry {
	return MQTTLogEntry{
		Time:   time.Now().Format("15:04:05.000"),
		Dir:    dir,
		Topic:  topic,
		Action: inkjoybridge.MQTTAction(payload),
		Note:   note,
		Body:   formatMQTTLogBody(payload),
	}
}

func joyousEntryAction(payload []byte) string {
	env, err := protocol.DecodeEnvelope(payload)
	if err != nil {
		return ""
	}
	action := env.Type
	if env.Type == protocol.TypeCmd {
		if cmd, err := protocol.DecodePayload[protocol.CmdPayload](env); err == nil && cmd.Cmd != "" {
			action = env.Type + " · " + cmd.Cmd
		}
	}
	if env.Type == protocol.TypeSendComplete {
		if sc, err := protocol.DecodePayload[protocol.SendCompletePayload](env); err == nil && sc.Phase != "" {
			action = env.Type + " · " + sc.Phase
		}
	}
	return action
}

func (b *MQTTLogBuffer) joyousEntry(dir, topic string, payload []byte) MQTTLogEntry {
	action := joyousEntryAction(payload)
	return MQTTLogEntry{
		Time:   time.Now().Format("15:04:05.000"),
		Dir:    dir,
		Topic:  topic,
		Action: action,
		Body:   formatMQTTLogBody(payload),
	}
}

// Snapshot returns copies of both log sides (oldest first).
func (b *MQTTLogBuffer) Snapshot() (local, upstream []MQTTLogEntry) {
	if b == nil {
		return nil, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	local = append([]MQTTLogEntry(nil), b.local...)
	upstream = append([]MQTTLogEntry(nil), b.upstream...)
	return local, upstream
}

func formatMQTTLogBody(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(payload, &v); err == nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if enc.Encode(v) == nil {
			s := bytes.TrimSpace(buf.Bytes())
			if len(s) > mqttLogBodyMax {
				return string(s[:mqttLogBodyMax]) + "\n… truncated"
			}
			return string(s)
		}
	}
	s := string(payload)
	if len(s) > mqttLogBodyMax {
		return s[:mqttLogBodyMax] + "… truncated"
	}
	return s
}
