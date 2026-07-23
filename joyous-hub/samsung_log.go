package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const samsungLogMax = 200

// SamsungLogEntry is one discover/MDC handshake line for the Samsung tab UI.
// Photo/content HTTP pulls stay on the hub and are intentionally not recorded here.
type SamsungLogEntry struct {
	Time  string `json:"time"`
	Phase string `json:"phase"` // discover | reachability | mdc | wol | samsung
	Peer  string `json:"peer,omitempty"`
	Text  string `json:"text"`
}

// SamsungLogBuffer keeps the last N handshake lines for the web UI.
type SamsungLogBuffer struct {
	mu   sync.RWMutex
	max  int
	ents []SamsungLogEntry
}

// NewSamsungLogBuffer creates a ring buffer (default 200).
func NewSamsungLogBuffer(max int) *SamsungLogBuffer {
	if max <= 0 {
		max = samsungLogMax
	}
	return &SamsungLogBuffer{max: max}
}

// Add appends one log line.
func (b *SamsungLogBuffer) Add(phase, peer, text string) {
	if b == nil || text == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ents = append(b.ents, SamsungLogEntry{
		Time:  time.Now().Format("15:04:05.000"),
		Phase: phase,
		Peer:  peer,
		Text:  text,
	})
	for len(b.ents) > b.max {
		b.ents = b.ents[1:]
	}
}

// Snapshot returns a copy of current entries (oldest first).
func (b *SamsungLogBuffer) Snapshot() []SamsungLogEntry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]SamsungLogEntry, len(b.ents))
	copy(out, b.ents)
	return out
}

// Package-level buffer shared by hub and samsung-bridge processes.
var samsungHandshakeLog = NewSamsungLogBuffer(samsungLogMax)

func samsungLogPhase(msg string) (phase, peer string) {
	switch {
	case strings.HasPrefix(msg, "discover "):
		phase = "discover"
	case strings.HasPrefix(msg, "reachability "):
		phase = "reachability"
	case strings.HasPrefix(msg, "mdc "):
		phase = "mdc"
	case strings.HasPrefix(msg, "wol "):
		phase = "wol"
	case strings.HasPrefix(msg, "samsung "):
		phase = "samsung"
	default:
		// Skip hub photo/content HTTP and unrelated outbound lines.
		return "", ""
	}
	peer = extractLogKV(msg, "ip")
	if peer == "" {
		peer = extractLogKV(msg, "id")
	}
	return phase, peer
}

func extractLogKV(msg, key string) string {
	token := key + "="
	i := strings.Index(msg, token)
	if i < 0 {
		return ""
	}
	rest := msg[i+len(token):]
	for j, r := range rest {
		if r == ' ' {
			return rest[:j]
		}
	}
	return rest
}

func recordSamsungOutbound(format string, args []any) {
	msg := fmt.Sprintf(format, args...)
	phase, peer := samsungLogPhase(msg)
	if phase == "" {
		return
	}
	samsungHandshakeLog.Add(phase, peer, msg)
}
