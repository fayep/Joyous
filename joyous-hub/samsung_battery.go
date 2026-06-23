package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	samsungBatteryHistoryFile = "samsung_battery_history.json"
	samsungBatteryMaxSamples  = 365
	samsungBatteryMinGap      = 30 * time.Second
)

// Samsung battery reading sources.
const (
	samsungBatteryPreSleep = "pre_sleep"
	samsungBatteryPoll     = "poll"
)

// SamsungBatterySample is one persisted battery telemetry point.
type SamsungBatterySample struct {
	At          time.Time `json:"at"`
	Percent     int       `json:"percent"`
	PowerSource string    `json:"power_source,omitempty"`
	Source      string    `json:"source,omitempty"`
}

// SamsungBatterySummary is computed history metadata for API/UI.
type SamsungBatterySummary struct {
	Samples   int                    `json:"battery_samples,omitempty"`
	Delta     *int                   `json:"battery_delta,omitempty"`
	PushDelta *int                   `json:"battery_push_delta,omitempty"`
	LastAt    time.Time              `json:"battery_at,omitempty"`
	Recent    []SamsungBatterySample `json:"battery_history,omitempty"`
}

// SamsungBatteryStore persists append-only Samsung battery readings.
type SamsungBatteryStore struct {
	mu   sync.Mutex
	dir  string
	path string
	m    map[string][]SamsungBatterySample
}

func NewSamsungBatteryStore(dir string) *SamsungBatteryStore {
	return &SamsungBatteryStore{
		dir:  dir,
		path: filepath.Join(dir, samsungBatteryHistoryFile),
		m:    make(map[string][]SamsungBatterySample),
	}
}

func (s *SamsungBatteryStore) Load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var m map[string][]SamsungBatterySample
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m == nil {
		s.m = make(map[string][]SamsungBatterySample)
	} else {
		s.m = m
	}
	return nil
}

func (s *SamsungBatteryStore) Save() error {
	s.mu.Lock()
	snapshot := make(map[string][]SamsungBatterySample, len(s.m))
	for id, samples := range s.m {
		cp := make([]SamsungBatterySample, len(samples))
		copy(cp, samples)
		snapshot[id] = cp
	}
	s.mu.Unlock()

	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0644)
}

// MigrateDeviceID moves battery history from an old registry id to a MAC-based id.
func (s *SamsungBatteryStore) MigrateDeviceID(oldID, newID string) {
	if s == nil || oldID == "" || newID == "" || oldID == newID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hist, ok := s.m[oldID]
	if !ok || len(hist) == 0 {
		return
	}
	existing := s.m[newID]
	merged := append(append([]SamsungBatterySample(nil), hist...), existing...)
	if len(merged) > samsungBatteryMaxSamples {
		merged = merged[len(merged)-samsungBatteryMaxSamples:]
	}
	s.m[newID] = merged
	delete(s.m, oldID)
}

// Record appends a reading unless it duplicates the latest sample within samsungBatteryMinGap.
func (s *SamsungBatteryStore) Record(deviceID string, percent int, powerSource, source string) bool {
	if deviceID == "" {
		return false
	}
	now := time.Now()
	sample := SamsungBatterySample{
		At:          now,
		Percent:     percent,
		PowerSource: powerSource,
		Source:      source,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.m[deviceID]
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Percent == percent && last.Source == source && now.Sub(last.At) < samsungBatteryMinGap {
			return false
		}
	}
	history = append(history, sample)
	if len(history) > samsungBatteryMaxSamples {
		history = history[len(history)-samsungBatteryMaxSamples:]
	}
	s.m[deviceID] = history
	return true
}

func (s *SamsungBatteryStore) Summary(deviceID string, recentLimit int) SamsungBatterySummary {
	if recentLimit <= 0 {
		recentLimit = 8
	}
	s.mu.Lock()
	history := append([]SamsungBatterySample(nil), s.m[deviceID]...)
	s.mu.Unlock()

	out := SamsungBatterySummary{Samples: len(history)}
	if len(history) == 0 {
		return out
	}
	out.LastAt = history[len(history)-1].At
	if len(history) >= 2 {
		d := history[len(history)-1].Percent - history[len(history)-2].Percent
		out.Delta = &d
	}
	if d, ok := samsungBatteryPushDelta(history); ok {
		out.PushDelta = &d
	}
	if len(history) > recentLimit {
		out.Recent = history[len(history)-recentLimit:]
	} else {
		out.Recent = history
	}
	return out
}

func samsungBatteryPushDelta(history []SamsungBatterySample) (int, bool) {
	var push []SamsungBatterySample
	for _, sample := range history {
		if sample.Source == samsungBatteryPreSleep {
			push = append(push, sample)
		}
	}
	if len(push) < 2 {
		return 0, false
	}
	d := push[len(push)-1].Percent - push[len(push)-2].Percent
	return d, true
}

func (h *Hub) samsungBatterySummary(deviceID string, recentLimit int) SamsungBatterySummary {
	if h == nil || h.samsungBattery == nil || deviceID == "" {
		return SamsungBatterySummary{}
	}
	return h.samsungBattery.Summary(deviceID, recentLimit)
}

func applySamsungBatterySummary(d *Device, summary SamsungBatterySummary) {
	if d == nil {
		return
	}
	d.BatterySamples = summary.Samples
	d.BatteryDelta = summary.Delta
	d.BatteryPushDelta = summary.PushDelta
	d.BatteryAt = summary.LastAt
	d.BatteryHistory = summary.Recent
}
