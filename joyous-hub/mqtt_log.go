package main

import (
	"bytes"
	"encoding/json"
	"sync"
	"time"

	"joyous-hub/inkjoybridge"
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

// MQTTLogBuffer keeps the last N messages for local (frame↔hub) and upstream (hub↔cloud).
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

// AddUpstream records hub↔cloud traffic.
func (b *MQTTLogBuffer) AddUpstream(dir, topic string, payload []byte, note string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.push(&b.upstream, b.entry(dir, topic, payload, note))
	b.mu.Unlock()
}

func (b *MQTTLogBuffer) push(list *[]MQTTLogEntry, entry MQTTLogEntry) {
	*list = append(*list, entry)
	if len(*list) > b.max {
		*list = (*list)[len(*list)-b.max:]
	}
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
