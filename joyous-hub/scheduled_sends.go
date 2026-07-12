package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const scheduledSendCheckInterval = time.Minute

// ScheduledSendConfig is persisted per device under {data-dir}/scheduled_sends/{file}.json.
// At each time in Times (local "HH:MM"), the hub sends a photo from AlbumID to the device,
// cycling through a shuffled copy of the album each pass so every photo is used before any
// repeats — it looks random to the viewer but stays evenly distributed over time.
type ScheduledSendConfig struct {
	DeviceID string   `json:"device_id"`
	AlbumID  string   `json:"album_id"`
	Times    []string `json:"times"` // "HH:MM", 24h local time, sorted, deduplicated
	Enabled  bool     `json:"enabled"`

	Queue          []string          `json:"queue,omitempty"`            // remaining shuffled image IDs for the current cycle
	LastFiredDates map[string]string `json:"last_fired_dates,omitempty"` // "HH:MM" -> "2006-01-02" last fired, to avoid double-firing within the same minute across ticks
}

func scheduledSendsEnabled(cfg ScheduledSendConfig) bool {
	return cfg.Enabled && cfg.AlbumID != "" && len(cfg.Times) > 0
}

// ScheduledSendStore manages per-device schedule config on disk, one JSON file per device.
type ScheduledSendStore struct {
	dir string
}

// NewScheduledSendStore creates a ScheduledSendStore rooted at dataDir/scheduled_sends.
func NewScheduledSendStore(dataDir string) *ScheduledSendStore {
	return &ScheduledSendStore{dir: filepath.Join(dataDir, "scheduled_sends")}
}

// scheduledSendFileName maps a device ID to a safe filename. Device IDs come from our own
// registry (MACs, "samsung:ip", "samsung:mac", nixplay playlist ids) and can contain
// characters (":", ".") that aren't safe to use directly as a path component, so anything
// outside [A-Za-z0-9_-] is replaced — this also rules out path traversal by construction.
func scheduledSendFileName(deviceID string) string {
	var b strings.Builder
	for _, r := range deviceID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String() + ".json"
}

func (s *ScheduledSendStore) ensureDir() error { return os.MkdirAll(s.dir, 0755) }

func (s *ScheduledSendStore) path(deviceID string) string {
	return filepath.Join(s.dir, scheduledSendFileName(deviceID))
}

// Get returns the persisted config for a device, or a disabled zero-value config if none exists.
func (s *ScheduledSendStore) Get(deviceID string) (ScheduledSendConfig, error) {
	if deviceID == "" {
		return ScheduledSendConfig{}, fmt.Errorf("device id required")
	}
	data, err := os.ReadFile(s.path(deviceID))
	if os.IsNotExist(err) {
		return ScheduledSendConfig{DeviceID: deviceID}, nil
	}
	if err != nil {
		return ScheduledSendConfig{}, err
	}
	var cfg ScheduledSendConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ScheduledSendConfig{}, err
	}
	return cfg, nil
}

// Save persists cfg for cfg.DeviceID.
func (s *ScheduledSendStore) Save(cfg ScheduledSendConfig) error {
	if cfg.DeviceID == "" {
		return fmt.Errorf("device id required")
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(cfg.DeviceID), b, 0644)
}

// Delete removes the persisted config for a device, if any.
func (s *ScheduledSendStore) Delete(deviceID string) error {
	if deviceID == "" {
		return fmt.Errorf("device id required")
	}
	err := os.Remove(s.path(deviceID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// All returns every persisted schedule config (enabled or not).
func (s *ScheduledSendStore) All() ([]ScheduledSendConfig, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ScheduledSendConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var cfg ScheduledSendConfig
		if err := json.Unmarshal(data, &cfg); err != nil || cfg.DeviceID == "" {
			continue
		}
		out = append(out, cfg)
	}
	return out, nil
}

// scheduledSendDueTimes returns the configured times that are due to fire right now
// (current local HH:MM matches and haven't already fired today). Pure function of
// (cfg, now) so it's testable without real timers, mirroring shouldTriggerOvernightDeepSleep.
func scheduledSendDueTimes(cfg ScheduledSendConfig, now time.Time) []string {
	if !scheduledSendsEnabled(cfg) {
		return nil
	}
	nowHM := now.Format("15:04")
	today := now.Format("2006-01-02")
	var due []string
	for _, t := range cfg.Times {
		if t != nowHM {
			continue
		}
		if cfg.LastFiredDates != nil && cfg.LastFiredDates[t] == today {
			continue
		}
		due = append(due, t)
	}
	return due
}

// nextScheduledImage picks the next image to send from candidateIDs, cycling through a
// shuffled copy so every candidate is sent once before any repeat. queue is the remaining
// order from the current cycle (nil/empty starts a fresh shuffle); entries no longer present
// in candidateIDs are dropped. Returns the chosen image ID and the updated queue to persist.
func nextScheduledImage(queue []string, candidateIDs []string, rng *rand.Rand) (string, []string, bool) {
	if len(candidateIDs) == 0 {
		return "", nil, false
	}
	valid := make(map[string]struct{}, len(candidateIDs))
	for _, id := range candidateIDs {
		valid[id] = struct{}{}
	}
	filtered := make([]string, 0, len(queue))
	for _, id := range queue {
		if _, ok := valid[id]; ok {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		filtered = append(filtered, candidateIDs...)
		rng.Shuffle(len(filtered), func(i, j int) { filtered[i], filtered[j] = filtered[j], filtered[i] })
	}
	return filtered[0], filtered[1:], true
}

var scheduledSendRand = rand.New(rand.NewSource(time.Now().UnixNano()))

// checkScheduledSends sweeps every persisted schedule and fires any that are due. Called
// once a minute from startScheduledSendScheduler.
func (h *Hub) checkScheduledSends() {
	if h.scheduledSends == nil {
		return
	}
	now := time.Now()
	cfgs, err := h.scheduledSends.All()
	if err != nil {
		log.Printf("scheduled sends: list configs: %v", err)
		return
	}
	for _, cfg := range cfgs {
		due := scheduledSendDueTimes(cfg, now)
		if len(due) == 0 {
			continue
		}
		h.runScheduledSend(cfg, due[0], now)
	}
}

func (h *Hub) runScheduledSend(cfg ScheduledSendConfig, firedTime string, now time.Time) {
	if _, ok := h.devices.Get(cfg.DeviceID); !ok {
		return
	}
	images, err := h.images.ListAlbumImages(cfg.AlbumID)
	if err != nil || len(images) == 0 {
		log.Printf("scheduled send %s: album %s empty or unavailable: %v", cfg.DeviceID, cfg.AlbumID, err)
		return
	}
	ids := make([]string, len(images))
	for i, im := range images {
		ids[i] = im.ID
	}
	imageID, queue, ok := nextScheduledImage(cfg.Queue, ids, scheduledSendRand)
	if !ok {
		return
	}
	if _, err := h.sendImageToDeviceAuto(cfg.DeviceID, imageID); err != nil {
		log.Printf("scheduled send %s: %v", cfg.DeviceID, err)
		return
	}
	cfg.Queue = queue
	if cfg.LastFiredDates == nil {
		cfg.LastFiredDates = map[string]string{}
	}
	cfg.LastFiredDates[firedTime] = now.Format("2006-01-02")
	if err := h.scheduledSends.Save(cfg); err != nil {
		log.Printf("scheduled send %s: save config: %v", cfg.DeviceID, err)
		return
	}
	log.Printf("scheduled send ok: device=%s album=%s image=%s time=%s", cfg.DeviceID, cfg.AlbumID, imageID, firedTime)
}

// startScheduledSendScheduler starts the once-a-minute sweep. Mirrors
// startSamsungOvernightScheduler's ticker/ctx shape.
func startScheduledSendScheduler(ctx context.Context, h *Hub) {
	ticker := time.NewTicker(scheduledSendCheckInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.checkScheduledSends()
			}
		}
	}()
}

// normalizeScheduledSendTimes validates, normalizes, deduplicates and sorts a list of
// "HH:MM" times.
func normalizeScheduledSendTimes(times []string) ([]string, error) {
	seen := make(map[string]struct{}, len(times))
	var out []string
	for _, t := range times {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		hh, mm, ok := parseHHMM(t)
		if !ok {
			return nil, fmt.Errorf("invalid time %q (expected HH:MM)", t)
		}
		norm := fmt.Sprintf("%02d:%02d", hh, mm)
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	sort.Strings(out)
	return out, nil
}

// handleScheduledSendGet serves GET /api/devices/{id}/schedule.
func (h *Hub) handleScheduledSendGet(w http.ResponseWriter, r *http.Request, deviceID string) {
	if _, ok := h.devices.Get(deviceID); !ok {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	cfg, err := h.scheduledSends.Get(deviceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// handleScheduledSendPut serves PUT /api/devices/{id}/schedule.
func (h *Hub) handleScheduledSendPut(w http.ResponseWriter, r *http.Request, deviceID string) {
	if _, ok := h.devices.Get(deviceID); !ok {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	var body struct {
		AlbumID string   `json:"album_id"`
		Times   []string `json:"times"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	times, err := normalizeScheduledSendTimes(body.Times)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Enabled {
		if body.AlbumID == "" {
			http.Error(w, "album_id required", http.StatusBadRequest)
			return
		}
		if len(times) == 0 {
			http.Error(w, "at least one time required", http.StatusBadRequest)
			return
		}
		if _, err := h.images.GetAlbum(body.AlbumID); err != nil {
			http.Error(w, "album not found", http.StatusBadRequest)
			return
		}
	}
	existing, _ := h.scheduledSends.Get(deviceID)
	cfg := ScheduledSendConfig{
		DeviceID:       deviceID,
		AlbumID:        body.AlbumID,
		Times:          times,
		Enabled:        body.Enabled,
		LastFiredDates: existing.LastFiredDates,
	}
	if existing.AlbumID == body.AlbumID {
		// Keep the in-progress shuffle cycle when the album hasn't changed, so saving
		// (e.g. adding a time) doesn't reset which photos have already been shown.
		cfg.Queue = existing.Queue
	}
	if err := h.scheduledSends.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
