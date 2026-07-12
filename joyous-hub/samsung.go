package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"joyous-hub/protocol"
)

const defaultPollInterval = 60

// SamsungFrameConfig is persisted per frame under {data-dir}/samsung/{frameId}.json.
type SamsungFrameConfig struct {
	FrameID               string `json:"frame_id"`
	Name                  string `json:"name,omitempty"`
	WifiMAC               string `json:"wifi_mac,omitempty"` // WiFi MAC for Samsung magic wake (AA:BB:…)
	PollIntervalMinutes   int    `json:"poll_interval_minutes"`
	InactiveBegin         string `json:"inactive_begin"` // "HH:MM" or empty
	InactiveEnd           string `json:"inactive_end"`     // "HH:MM" or empty
	CropFormat            string `json:"crop_format,omitempty"`
	DisplayWidth          int    `json:"display_width,omitempty"`
	DisplayHeight         int    `json:"display_height,omitempty"`
	AutoSleepAfterPush    *bool  `json:"auto_sleep_after_push,omitempty"`
	SleepAfterPushSeconds int    `json:"sleep_after_push_seconds,omitempty"`
	OvernightDeepSleep    *bool  `json:"overnight_deep_sleep,omitempty"`
	DeepSleepActive       bool   `json:"deep_sleep_active,omitempty"`
	OvernightDeepSleepAt  time.Time `json:"overnight_deep_sleep_at,omitempty"`
	DailyRefreshTime      string `json:"daily_refresh_time,omitempty"` // HH:MM on frame
	MorningStandbyRestoredAt time.Time `json:"morning_standby_restored_at,omitempty"`
}

// SamsungStore manages Samsung EM32DX frame images and config on disk.
type SamsungStore struct {
	dir    string
	colors *ColorStore
}

// NewSamsungStore creates a SamsungStore rooted at dir/samsung.
func NewSamsungStore(dir string) *SamsungStore {
	return &SamsungStore{dir: filepath.Join(dir, "samsung")}
}

func (s *SamsungStore) SetColorStore(c *ColorStore) { s.colors = c }

func (s *SamsungStore) colorPipeline() ColorPipeline {
	if s.colors != nil {
		return s.colors.Pipeline()
	}
	return defaultColorPipeline()
}

func (s *SamsungStore) ensureDir() error {
	return os.MkdirAll(s.dir, 0755)
}

func (s *SamsungStore) pngPath(frameID string) string {
	return filepath.Join(s.dir, frameID+".png")
}

func (s *SamsungStore) lockPath(frameID string) string {
	return filepath.Join(s.dir, frameID+".lock")
}

func (s *SamsungStore) configPath(frameID string) string {
	return filepath.Join(s.dir, frameID+".json")
}

func validFrameID(id string) bool {
	if id == "" || strings.ContainsAny(id, "/\\..") {
		return false
	}
	return true
}

func defaultSamsungConfig(frameID string) SamsungFrameConfig {
	def := defaultSamsungDisplayProfile()
	autoSleep := true
	return SamsungFrameConfig{
		FrameID:               frameID,
		PollIntervalMinutes:   defaultPollInterval,
		CropFormat:            def.CropFormat,
		DisplayWidth:          def.Width,
		DisplayHeight:         def.Height,
		AutoSleepAfterPush:    &autoSleep,
		SleepAfterPushSeconds: defaultSleepAfterPushSec,
	}
}

func samsungAutoSleepAfterPush(cfg SamsungFrameConfig) bool {
	if cfg.AutoSleepAfterPush == nil {
		return true
	}
	return *cfg.AutoSleepAfterPush
}

func samsungSleepAfterPushSec(cfg SamsungFrameConfig) int {
	if cfg.SleepAfterPushSeconds <= 0 {
		return defaultSleepAfterPushSec
	}
	return cfg.SleepAfterPushSeconds
}

// LoadConfig reads per-frame config, returning defaults if missing.
func (s *SamsungStore) LoadConfig(frameID string) (SamsungFrameConfig, error) {
	if !validFrameID(frameID) {
		return SamsungFrameConfig{}, fmt.Errorf("invalid frame id")
	}
	data, err := os.ReadFile(s.configPath(frameID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultSamsungConfig(frameID), nil
		}
		return SamsungFrameConfig{}, err
	}
	var cfg SamsungFrameConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return SamsungFrameConfig{}, err
	}
	if cfg.PollIntervalMinutes <= 0 {
		cfg.PollIntervalMinutes = defaultPollInterval
	}
	normalizeSamsungConfig(&cfg)
	cfg.FrameID = frameID
	return cfg, nil
}

func normalizeSamsungConfig(cfg *SamsungFrameConfig) {
	if cfg.CropFormat == "" {
		cfg.CropFormat = defaultSamsungDisplayProfile().CropFormat
	}
	if cfg.DisplayWidth <= 0 || cfg.DisplayHeight <= 0 {
		if cfg.CropFormat == "9:16" || cfg.CropFormat == "3:4" {
			cfg.DisplayWidth, cfg.DisplayHeight = 1440, 2560
		} else {
			def := defaultSamsungDisplayProfile()
			cfg.DisplayWidth, cfg.DisplayHeight = def.Width, def.Height
		}
	}
}

// SaveConfig writes per-frame config.
func (s *SamsungStore) SaveConfig(cfg SamsungFrameConfig) error {
	if !validFrameID(cfg.FrameID) {
		return fmt.Errorf("invalid frame id")
	}
	if cfg.PollIntervalMinutes <= 0 {
		cfg.PollIntervalMinutes = defaultPollInterval
	}
	normalizeSamsungConfig(&cfg)
	if err := s.ensureDir(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.configPath(cfg.FrameID), b, 0644)
}

// IsLocked reports whether a lockfile exists for the frame.
func (s *SamsungStore) IsLocked(frameID string) bool {
	_, err := os.Stat(s.lockPath(frameID))
	return err == nil
}

// PNGInfo returns etag, modified time, and whether the png exists.
func (s *SamsungStore) PNGInfo(frameID string) (etag string, mod time.Time, ok bool) {
	fi, err := os.Stat(s.pngPath(frameID))
	if err != nil {
		return "", time.Time{}, false
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", frameID, fi.Size(), fi.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:8]), fi.ModTime(), true
}

// samsungStoreMetadataIDs are non-frame JSON files kept in the samsung data dir.
var samsungStoreMetadataIDs = map[string]struct{}{
	"aliases": {},
}

func samsungStoreMetadataID(id string) bool {
	_, ok := samsungStoreMetadataIDs[id]
	return ok
}

// ListFrames returns frame IDs discovered from *.json and *.png in the samsung dir.
func (s *SamsungStore) ListFrames() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, e := range entries {
		name := e.Name()
		var id string
		switch {
		case strings.HasSuffix(name, ".json"):
			id = strings.TrimSuffix(name, ".json")
		case strings.HasSuffix(name, ".png"):
			id = strings.TrimSuffix(name, ".png")
		default:
			continue
		}
		if validFrameID(id) && !samsungStoreMetadataID(id) {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// writePNGLocked replaces the frame PNG using lockfile protocol. content.json's manifest id
// (see samsungContentFileID) is derived from these bytes each time it's served, not remembered
// from whenever they were written, so there's nothing to update here when they change.
func (s *SamsungStore) writePNGLocked(frameID string, pngData []byte) error {
	if !validFrameID(frameID) {
		return fmt.Errorf("invalid frame id")
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	lock := s.lockPath(frameID)
	if err := os.WriteFile(lock, []byte{}, 0644); err != nil {
		return err
	}
	defer os.Remove(lock)

	tmp := s.pngPath(frameID) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, werr := f.Write(pngData)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	if serr != nil {
		os.Remove(tmp)
		return serr
	}
	if cerr != nil {
		os.Remove(tmp)
		return cerr
	}
	return os.Rename(tmp, s.pngPath(frameID))
}

// StoreUpload decodes raw image bytes, dithers to Samsung palette, and writes PNG.
func (s *SamsungStore) StoreUpload(frameID string, raw []byte, profile SamsungDisplayProfile) error {
	pngData, err := convertToSamsungPNG(raw, profile, CropRect{}, false, s.colorPipeline())
	if err != nil {
		return err
	}
	return s.writePNGLocked(frameID, pngData)
}

// StorePNG writes pre-dithered PNG bytes (validates dimensions loosely).
func (s *SamsungStore) StorePNG(frameID string, pngData []byte) error {
	if _, err := png.Decode(bytes.NewReader(pngData)); err != nil {
		return fmt.Errorf("invalid png: %w", err)
	}
	return s.writePNGLocked(frameID, pngData)
}

// convertToSamsungPNG applies saved crop metadata (or center-crops), then two-palette
// Stucki: dither in PaletteSamsungDisplay (P2) space, write PaletteSamsungSend (P1) RGB.
func convertToSamsungPNG(raw []byte, profile SamsungDisplayProfile, crop CropRect, hasCrop bool, pipe ColorPipeline) ([]byte, error) {
	tw, th := profile.Width, profile.Height
	if tw <= 0 || th <= 0 {
		tw, th = samsungW, samsungH
	}
	img, err := decodeAnyImage(raw)
	if err != nil {
		return nil, err
	}
	if hasCrop && crop.W > 0 && crop.H > 0 {
		img = applyCrop(img, crop)
	} else {
		img = centerCropToSize(img, tw, th)
	}
	img = resizeTo(img, tw, th)
	indices := StuckiTwoPalette(img, pipe.SamsungDisplay, pipe, false, stuckiOptionsSamsung(pipe))
	out := RenderIndicesToRGB(indices, pipe.SamsungSend)
	return encodePNG(out), nil
}

func renderIndicesToRGB(indices [][]byte, palette [6][3]float64) image.Image {
	h := len(indices)
	if h == 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	w := len(indices[0])
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y, row := range indices {
		for x, idx := range row {
			c := palette[idx]
			dst.Set(x, y, color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
		}
	}
	return dst
}

// centerCropToSize center-crops to target aspect ratio, then scales to exact dimensions.
func centerCropToSize(img image.Image, tw, th int) image.Image {
	b := img.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	targetAR := float64(tw) / float64(th)
	imgAR := iw / ih
	var crop CropRect
	if imgAR > targetAR {
		w := targetAR / imgAR
		crop = CropRect{X: (1 - w) / 2, Y: 0, W: w, H: 1}
	} else {
		h := imgAR / targetAR
		crop = CropRect{X: 0, Y: (1 - h) / 2, W: 1, H: h}
	}
	cropped := applyCrop(img, crop)
	return resizeTo(cropped, tw, th)
}

// ── inactive hours helpers (shared with schedule logic in tests) ─────────────

func parseHHMM(s string) (hour, min int, ok bool) {
	if s == "" {
		return 0, 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func minutesSinceMidnight(t time.Time) int {
	return t.Hour()*60 + t.Minute()
}

func timeFromMinutesSinceMidnight(m int) time.Time {
	h := m / 60
	min := m % 60
	if h >= 24 {
		h = 23
		min = 59
	}
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), h, min, 0, 0, now.Location())
}

// InactiveScheduleEnabled reports whether inactive begin/end define a real window.
// Equal times (e.g. 00:00–00:00) mean the hub should not change network sleep by schedule.
func InactiveScheduleEnabled(begin, end string) bool {
	bh, bm, okB := parseHHMM(begin)
	eh, em, okE := parseHHMM(end)
	if !okB || !okE {
		return false
	}
	return bh*60+bm != eh*60+em
}

// InInactiveWindow reports whether t falls inside [begin, end) inactive window.
// Supports cross-midnight windows (e.g. 22:00–07:00).
func InInactiveWindow(t time.Time, begin, end string) bool {
	bh, bm, okB := parseHHMM(begin)
	eh, em, okE := parseHHMM(end)
	if !okB || !okE {
		return false
	}
	nowM := minutesSinceMidnight(t)
	bM := bh*60 + bm
	eM := eh*60 + em
	if bM == eM {
		return false
	}
	if bM < eM {
		return nowM >= bM && nowM < eM
	}
	// cross-midnight
	return nowM >= bM || nowM < eM
}

// NextInactiveEnd returns the next time inactive window ends after t.
func NextInactiveEnd(t time.Time, begin, end string) time.Time {
	eh, em, okE := parseHHMM(end)
	if !okE {
		return t
	}
	endToday := time.Date(t.Year(), t.Month(), t.Day(), eh, em, 0, 0, t.Location())
	if !InInactiveWindow(t, begin, end) {
		return endToday
	}
	if t.Before(endToday) {
		return endToday
	}
	return endToday.Add(24 * time.Hour)
}

// NextWakeTime computes the next poll wakeup respecting interval and inactive hours.
func NextWakeTime(now time.Time, pollMinutes int, inactiveBegin, inactiveEnd string) time.Time {
	if pollMinutes <= 0 {
		pollMinutes = defaultPollInterval
	}
	if InInactiveWindow(now, inactiveBegin, inactiveEnd) {
		return NextInactiveEnd(now, inactiveBegin, inactiveEnd)
	}
	next := now.Add(time.Duration(pollMinutes) * time.Minute)
	if InInactiveWindow(next, inactiveBegin, inactiveEnd) {
		return NextInactiveEnd(next, inactiveBegin, inactiveEnd)
	}
	return next
}

// ── HTTP handlers ────────────────────────────────────────────────────────────

type samsungStatusResponse struct {
	ETag                  string    `json:"etag"`
	Locked                bool      `json:"locked"`
	Modified              time.Time `json:"modified,omitempty"`
	HasImage              bool      `json:"has_image"`
	Name                  string    `json:"name,omitempty"`
	WifiMAC               string    `json:"wifi_mac,omitempty"`
	PollIntervalMinutes   int       `json:"poll_interval_minutes"`
	InactiveBegin         string    `json:"inactive_begin"`
	InactiveEnd           string    `json:"inactive_end"`
	CropFormat            string    `json:"crop_format"`
	DisplayWidth          int       `json:"display_width"`
	DisplayHeight         int       `json:"display_height"`
	AutoSleepAfterPush    bool      `json:"auto_sleep_after_push"`
	SleepAfterPushSeconds int       `json:"sleep_after_push_seconds"`
	OvernightDeepSleep    bool      `json:"overnight_deep_sleep"`
	DeepSleepActive       bool      `json:"deep_sleep_active"`
	DailyRefreshTime      string    `json:"daily_refresh_time,omitempty"`
}

func (h *Hub) noteSamsungFrameSeen(r *http.Request, frameID, action string) {
	clientIP := requestClientIP(r)
	frameID = h.resolveSamsungFrameID(frameID)
	touched := false
	dev := h.samsungDeviceByFrameID(frameID)
	if dev != nil && dev.IP != "" {
		if clientIP == dev.IP || frameIDIsMAC(frameID) {
			touched = h.devices.TouchSamsung(dev.IP, action)
		}
	}
	if !touched {
		if ip := frameIDToIP(frameID); ip != "" && clientIP == ip {
			touched = h.devices.TouchSamsung(ip, action)
		}
	}
	if touched {
		h.maybeClearSamsungDeepSleepOnFrameContact(frameID)
		if dev := h.samsungDeviceByFrameID(frameID); dev != nil {
			h.notifySamsungBridgeContact(dev.ID, action)
		}
	}
}

// notifySamsungBridgeContact tells samsung-bridge about hub-side frame contact so
// devices.sync LastSeen/action match (wake, HTTP pulls, etc. happen on the hub).
func (h *Hub) notifySamsungBridgeContact(deviceID, action string) {
	if deviceID == "" || action == "" || h.bridgeCoord == nil {
		return
	}
	if !h.bridgeCoord.BridgeOnline(string(DeviceTypeSamsung)) {
		return
	}
	body, err := json.Marshal(map[string]string{"action": action})
	if err != nil {
		return
	}
	if err := h.bridgeCoord.PublishCommand(string(DeviceTypeSamsung), protocol.CmdPayload{
		Cmd:      protocol.CmdDeviceTouch,
		DeviceID: deviceID,
		Body:     body,
	}); err != nil {
		log.Printf("samsung bridge contact notify device=%s: %v", deviceID, err)
	}
}

// maybeClearSamsungDeepSleepOnFrameContact clears sticky deep-sleep when the frame
// is HTTP-polling outside the inactive window (button wake; network is back).
func (h *Hub) maybeClearSamsungDeepSleepOnFrameContact(frameID string) {
	if h.samsung == nil {
		return
	}
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil || !cfg.DeepSleepActive {
		return
	}
	now := time.Now()
	if InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) &&
		InInactiveWindow(now, cfg.InactiveBegin, cfg.InactiveEnd) {
		return
	}
	h.clearSamsungDeepSleepAfterPush(frameID)
	log.Printf("samsung deep sleep cleared after frame contact frame=%s", frameID)
}

func (h *Hub) handleSamsungStatus(w http.ResponseWriter, r *http.Request, frameID string) {
	setSamsungCacheResponseHeaders(w)
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag, mod, hasImage := h.samsung.PNGInfo(frameID)
	resp := samsungStatusResponse{
		ETag:                  etag,
		Locked:                h.samsung.IsLocked(frameID),
		HasImage:              hasImage,
		PollIntervalMinutes:   cfg.PollIntervalMinutes,
		Name:                  cfg.Name,
		WifiMAC:               cfg.WifiMAC,
		InactiveBegin:         cfg.InactiveBegin,
		InactiveEnd:           cfg.InactiveEnd,
		CropFormat:            cfg.CropFormat,
		DisplayWidth:          cfg.DisplayWidth,
		DisplayHeight:         cfg.DisplayHeight,
		AutoSleepAfterPush:    samsungAutoSleepAfterPush(cfg),
		SleepAfterPushSeconds: samsungSleepAfterPushSec(cfg),
		OvernightDeepSleep:    samsungOvernightDeepSleepEnabled(cfg),
		DeepSleepActive:       cfg.DeepSleepActive,
		DailyRefreshTime:      cfg.DailyRefreshTime,
	}
	if hasImage {
		resp.Modified = mod
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Hub) handleSamsungPNG(w http.ResponseWriter, r *http.Request, frameID string) {
	setSamsungCacheResponseHeaders(w)
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	if h.samsung.IsLocked(frameID) {
		http.Error(w, "image locked", http.StatusLocked)
		return
	}
	path := h.samsung.pngPath(frameID)
	// Open once and derive size/etag/body from the same file descriptor: a
	// concurrent writePNGLocked rename swaps the directory entry to a new
	// inode but never touches bytes already open here, so Content-Length
	// always matches what's actually written below — a separate Stat (or
	// PNGInfo's own independent Stat) plus ReadFile can straddle a rename.
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", frameID, info.Size(), info.ModTime().UnixNano())))
	etag := hex.EncodeToString(sum[:8])
	h.noteSamsungFrameSeen(r, frameID, "png")
	if h.sendDelivery != nil && r.Method != http.MethodHead {
		h.sendDelivery.CompleteSamsung(frameID, etag)
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == `"`+etag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write(data)
}

func (h *Hub) handleSamsungContentJSON(w http.ResponseWriter, r *http.Request, frameID string) {
	setSamsungCacheResponseHeaders(w)
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	if h.samsung.IsLocked(frameID) {
		http.Error(w, "image locked", http.StatusLocked)
		return
	}
	path := h.samsung.pngPath(frameID)
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	etag, _, _ := h.samsung.PNGInfo(frameID)
	h.noteSamsungFrameSeen(r, frameID, "content.json")
	if h.sendDelivery != nil {
		h.sendDelivery.MarkSamsungDownloading(frameID, etag)
	}
	addr := h.serverAddr
	if addr == "" {
		addr = r.Host
	}
	contentID := samsungContentFileID(frameID, data)
	frameIP := frameIDToIP(frameID)
	if dev := h.samsungDeviceByFrameID(frameID); dev != nil && dev.IP != "" {
		frameIP = dev.IP
	}
	imageURL := samsungImageURL(addr, frameIP, frameID)
	manifest := buildContentJSON(imageURL, contentID, frameID, len(data))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(manifest)
}

func (h *Hub) handleSamsungImage(w http.ResponseWriter, r *http.Request, frameID string) {
	h.handleSamsungPNG(w, r, frameID)
}

func (h *Hub) handleSamsungPreview(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	if h.samsung.IsLocked(frameID) {
		http.Error(w, "image locked", http.StatusLocked)
		return
	}
	path := h.samsung.pngPath(frameID)
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	preview, err := RemapSamsungSendPNGToDisplay(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag, _, _ := h.samsung.PNGInfo(frameID)
	previewETag := etag + "-p2"
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == `"`+previewETag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("ETag", `"`+previewETag+`"`)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(preview)
}

func (h *Hub) handleSamsungLock(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	if !h.samsung.IsLocked(frameID) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Hub) handleSamsungConfigPut(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	var body SamsungFrameConfig
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	existing, _ := h.samsung.LoadConfig(frameID)
	if _, hasName := raw["name"]; !hasName {
		body.Name = existing.Name
	}
	if _, has := raw["wifi_mac"]; !has {
		body.WifiMAC = existing.WifiMAC
	}
	if _, has := raw["auto_sleep_after_push"]; !has {
		body.AutoSleepAfterPush = existing.AutoSleepAfterPush
	}
	if _, has := raw["sleep_after_push_seconds"]; !has && body.SleepAfterPushSeconds <= 0 {
		body.SleepAfterPushSeconds = existing.SleepAfterPushSeconds
	}
	if _, has := raw["overnight_deep_sleep"]; !has {
		body.OvernightDeepSleep = existing.OvernightDeepSleep
	}
	if _, has := raw["deep_sleep_active"]; !has {
		body.DeepSleepActive = existing.DeepSleepActive
	}
	if _, has := raw["overnight_deep_sleep_at"]; !has {
		body.OvernightDeepSleepAt = existing.OvernightDeepSleepAt
	}
	if _, has := raw["daily_refresh_time"]; !has {
		body.DailyRefreshTime = existing.DailyRefreshTime
	}
	if _, has := raw["morning_standby_restored_at"]; !has {
		body.MorningStandbyRestoredAt = existing.MorningStandbyRestoredAt
	}
	body.FrameID = frameID
	if body.PollIntervalMinutes <= 0 {
		body.PollIntervalMinutes = defaultPollInterval
	}
	normalizeSamsungConfig(&body)
	if err := h.samsung.SaveConfig(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.syncSamsungDeviceName(frameID, body.Name)
	h.syncSamsungWakeMAC(frameID, body.WifiMAC)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *Hub) samsungDeviceByFrameID(frameID string) *Device {
	if h.samsungAliases != nil {
		if c := h.samsungAliases.canonical(frameID); c != frameID {
			frameID = c
		}
	}
	if mac, ok := normalizeSamsungMAC(frameID); ok {
		if d := h.devices.FindSamsungByMAC(mac); d != nil {
			return d
		}
	}
	if ip := frameIDToIP(frameID); ip != "" {
		if d := h.devices.FindSamsungByIP(ip); d != nil {
			return d
		}
	}
	return nil
}

func (h *Hub) samsungWakeMAC(frameID string, dev *Device) string {
	if dev != nil && dev.MDCMAC != "" {
		return dev.MDCMAC
	}
	cfg, err := h.samsung.LoadConfig(frameID)
	if err == nil && cfg.WifiMAC != "" {
		return cfg.WifiMAC
	}
	return ""
}

func (h *Hub) syncSamsungWakeMAC(frameID, mac string) {
	mac = strings.TrimSpace(mac)
	dev := h.samsungDeviceByFrameID(frameID)
	if dev != nil {
		if h.devices.SetMDCMAC(dev.ID, mac) {
			_ = h.devices.Save()
		}
		if dev.IP != "" {
			if norm, ok := normalizeSamsungMAC(mac); ok {
				h.applySamsungMAC(dev.IP, norm)
			}
		}
		return
	}
	if ip := frameIDToIP(frameID); ip != "" {
		if h.devices.SetMDCMAC(samsungID(ip), mac) {
			_ = h.devices.Save()
		}
		if norm, ok := normalizeSamsungMAC(mac); ok {
			h.applySamsungMAC(ip, norm)
		}
	}
}

// recordSamsungBattery stores the latest reading and appends to passive history.
func (h *Hub) recordSamsungBattery(ip string, percent int, powerSource, source string) {
	if !h.devices.UpdateSamsungBattery(ip, percent, powerSource) {
		return
	}
	id := samsungProvisionalRegistryID(ip)
	if dev := h.devices.FindSamsungByIP(ip); dev != nil {
		id = samsungDeviceRegistryID(dev)
	}
	if h.samsungBattery != nil {
		h.samsungBattery.Record(id, percent, powerSource, source)
		_ = h.samsungBattery.Save()
	}
	_ = h.devices.Save()
}

// sleepSamsungDisplay reads battery and sends sleep-now on a single MDC session (with connect retries).
func (h *Hub) sleepSamsungDisplay(ip, pin string) error {
	return h.sleepSamsungDisplayDepth(ip, pin, false)
}

// sleepSamsungDeepDisplay sleeps after disabling network standby (overnight deep sleep).
func (h *Hub) sleepSamsungDeepDisplay(ip, pin string) error {
	return h.sleepSamsungDisplayDepth(ip, pin, true)
}

func (h *Hub) sleepSamsungDisplayDepth(ip, pin string, deep bool) error {
	res, err := SendMDCSleepWithBatteryCheck(ip, pin)
	if res.SessionOK {
		if res.BatteryOK {
			h.recordSamsungBattery(ip, res.Percent, res.PowerSource, samsungBatteryPreSleep)
		} else {
			h.devices.TouchSamsung(ip, "mdc_session")
		}
	}
	if err != nil {
		return err
	}
	h.devices.NoteSamsungSlept(ip, deep)
	_ = h.devices.Save()
	return nil
}

func (h *Hub) handleSamsungSleep(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil || dev.IP == "" {
		http.Error(w, "frame not registered on hub", http.StatusNotFound)
		return
	}
	if err := h.sleepSamsungDisplay(dev.IP, dev.MDCPin); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.notifySamsungBridgeContact(dev.ID, "mdc_sleep")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// pushSamsungFrame MDC-pushes the frame's current PNG (wake → content.json → optional sleep).
func (h *Hub) pushSamsungFrame(frameID string, dev *Device) error {
	return h.pushSamsungFrameWithProgress(frameID, dev, nil)
}

// pushSamsungFrameWithProgress is pushSamsungFrame plus an optional callback invoked on every
// wake poll, both during the automatic remote-wake attempt (WakeSamsungDisplayWithProgress) and
// the manual power-button-wait fallback (waitForMDCAwakeManual, which can legitimately take
// minutes) — the only way a caller (the samsung-bridge, to relay "retrying" status to the hub)
// finds out a wake is in progress rather than stuck, from the very first attempt.
func (h *Hub) pushSamsungFrameWithProgress(frameID string, dev *Device, onWakeAttempt func(phase string, attempt int)) error {
	h.ensureSamsungMAC(dev.IP, dev.MDCPin)
	frameID = h.resolveSamsungFrameID(frameID)
	if dev2 := h.samsungDeviceByFrameID(frameID); dev2 != nil {
		dev = dev2
	}
	if _, _, ok := h.samsung.PNGInfo(frameID); !ok {
		return fmt.Errorf("no image for frame %s", frameID)
	}
	addr := h.serverAddr
	if addr == "" {
		addr = "localhost:8080"
	}
	contentURL := samsungMDCContentURL(addr, dev.IP, frameID)
	logOutbound("samsung push frame=%s ip=%s content=%s", frameID, dev.IP, contentURL)
	cfg, _ := h.samsung.LoadConfig(frameID)
	wifiMAC := h.samsungWakeMAC(frameID, dev)
	autoSleep := samsungAutoSleepAfterPush(cfg)
	sleepAfter := samsungSleepAfterPushSec(cfg)
	now := time.Now()
	insideInactive := InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) && InInactiveWindow(now, cfg.InactiveBegin, cfg.InactiveEnd)
	restoreStandby := samsungRestoreNetworkStandbyOnPush(cfg, now)
	sleepFn := SamsungSleepFunc(h.sleepSamsungDisplay)
	if cfg.DeepSleepActive && (!InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) || insideInactive) {
		sleepFn = h.sleepSamsungDeepDisplay
	}
	err := PushSamsungContent(dev.IP, contentURL, dev.MDCPin, wifiMAC, autoSleep, sleepAfter, sleepFn, SamsungPushOptions{
		DeepSleepActive: cfg.DeepSleepActive,
		RestoreStandby:  restoreStandby,
	}, onWakeAttempt)
	if err == nil {
		h.devices.TouchSamsung(dev.IP, "mdc_push")
		if restoreStandby {
			h.clearSamsungDeepSleepAfterPush(frameID)
		}
	}
	return err
}

func (h *Hub) handleSamsungPush(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil || dev.IP == "" {
		http.Error(w, "frame not registered on hub", http.StatusNotFound)
		return
	}
	var sendID string
	if h.sendDelivery != nil {
		if etag, _, ok := h.samsung.PNGInfo(frameID); ok {
			send := h.sendDelivery.RegisterWithSession(dev.ID, "", requestSessionID(r))
			h.sendDelivery.BindSamsung(send.ID, frameID, etag)
			sendID = send.ID
			h.publishSendEvent(sendID)
		}
	}
	if err := h.pushSamsungFrame(frameID, dev); err != nil {
		if sendID != "" {
			h.sendDelivery.Fail(sendID)
			h.publishSendEvent(sendID)
		}
		code := http.StatusBadGateway
		if strings.Contains(err.Error(), "no image for frame") {
			code = http.StatusNotFound
		}
		if strings.Contains(err.Error(), "frame did not wake") {
			code = http.StatusGatewayTimeout
		}
		http.Error(w, err.Error(), code)
		return
	}
	h.notifySamsungBridgeContact(dev.ID, "mdc_push")
	out := map[string]any{"ok": true}
	if sendID != "" {
		out["send_id"] = sendID
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Hub) handleSamsungWake(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil || dev.IP == "" {
		http.Error(w, "frame not registered on hub", http.StatusNotFound)
		return
	}
	mac := h.samsungWakeMAC(frameID, dev)
	if mac == "" {
		http.Error(w, "wifi MAC required for wake — set it in Display settings", http.StatusBadRequest)
		return
	}
	err := WakeSamsungDisplay(dev.IP, dev.MDCPin, mac)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.devices.TouchSamsung(dev.IP, "mdc_wake")
	h.maybeClearSamsungDeepSleepOnFrameContact(frameID)
	h.notifySamsungBridgeContact(dev.ID, "mdc_wake")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// syncSamsungDeviceName copies the saved Samsung friendly name into the device registry.
func (h *Hub) syncSamsungDeviceName(frameID, name string) {
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil {
		return
	}
	if h.devices.SetName(dev.ID, strings.TrimSpace(name)) {
		_ = h.devices.Save()
	}
}

// applySamsungFriendlyNames overlays saved SamsungFrameConfig.Name on device list entries.
func (h *Hub) applySamsungFriendlyNames(devs []Device) {
	for i := range devs {
		if devs[i].Type != DeviceTypeSamsung {
			continue
		}
		frameID := SamsungFrameID(&devs[i])
		if frameID == "" {
			continue
		}
		cfg, err := h.samsung.LoadConfig(frameID)
		if err != nil || cfg.Name == "" {
			continue
		}
		devs[i].Name = cfg.Name
	}
}

func (h *Hub) handleSamsungImageUpload(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file field required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var dev *Device
	if d := h.samsungDeviceByFrameID(frameID); d != nil {
		dev = d
	}
	profile := h.samsungDisplayProfile(dev, frameID)
	var storeErr error
	if isFlatCalibrationName(header.Filename) {
		storeErr = h.samsung.StorePNG(frameID, raw)
	} else {
		storeErr = h.samsung.StoreUpload(frameID, raw, profile)
	}
	if storeErr != nil {
		http.Error(w, storeErr.Error(), http.StatusBadRequest)
		return
	}
	etag, _, _ := h.samsung.PNGInfo(frameID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "etag": etag})
}

func (h *Hub) handleSamsungList(w http.ResponseWriter, r *http.Request) {
	frames, err := h.samsung.ListFrames()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type frameInfo struct {
		ID                    string    `json:"id"`
		Name                  string    `json:"name,omitempty"`
		DeviceID              string    `json:"device_id,omitempty"`
		IP                    string    `json:"ip,omitempty"`
		Connected             bool      `json:"connected"`
		Battery               int       `json:"battery,omitempty"`
		PowerSource           string    `json:"power_source,omitempty"`
		BatterySamples        int       `json:"battery_samples,omitempty"`
		BatteryDelta          *int      `json:"battery_delta,omitempty"`
		BatteryPushDelta      *int      `json:"battery_push_delta,omitempty"`
		BatteryAt             time.Time `json:"battery_at,omitempty"`
		BatteryHistory        []SamsungBatterySample `json:"battery_history,omitempty"`
		LastSeen              time.Time `json:"last_seen,omitempty"`
		LastAction            string    `json:"last_action,omitempty"`
		HasImage              bool      `json:"has_image"`
		Locked                bool      `json:"locked"`
		ETag                  string    `json:"etag,omitempty"`
		WifiMAC               string    `json:"wifi_mac,omitempty"`
		PollIntervalMinutes   int       `json:"poll_interval_minutes"`
		InactiveBegin         string    `json:"inactive_begin"`
		InactiveEnd           string    `json:"inactive_end"`
		CropFormat            string    `json:"crop_format"`
		DisplayWidth          int       `json:"display_width"`
		DisplayHeight         int       `json:"display_height"`
		AutoSleepAfterPush    bool      `json:"auto_sleep_after_push"`
		SleepAfterPushSeconds int       `json:"sleep_after_push_seconds"`
		OvernightDeepSleep    bool      `json:"overnight_deep_sleep"`
		DeepSleepActive       bool      `json:"deep_sleep_active"`
	}
	devByFrame := make(map[string]Device)
	for _, d := range h.devices.List() {
		if d.Type != DeviceTypeSamsung {
			continue
		}
		frameID := SamsungFrameID(&d)
		if frameID == "" {
			continue
		}
		devByFrame[frameID] = d
	}
	seen := make(map[string]struct{}, len(frames)+len(devByFrame))
	for _, id := range frames {
		seen[id] = struct{}{}
	}
	for id := range devByFrame {
		seen[id] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	out := make([]frameInfo, 0, len(ids))
	for _, id := range ids {
		cfg, _ := h.samsung.LoadConfig(id)
		etag, _, hasImage := h.samsung.PNGInfo(id)
		name := strings.TrimSpace(cfg.Name)
		info := frameInfo{
			ID:                    id,
			Name:                  name,
			HasImage:              hasImage,
			Locked:                h.samsung.IsLocked(id),
			ETag:                  etag,
			WifiMAC:               cfg.WifiMAC,
			PollIntervalMinutes:   cfg.PollIntervalMinutes,
			InactiveBegin:         cfg.InactiveBegin,
			InactiveEnd:           cfg.InactiveEnd,
			CropFormat:            cfg.CropFormat,
			DisplayWidth:          cfg.DisplayWidth,
			DisplayHeight:         cfg.DisplayHeight,
			AutoSleepAfterPush:    samsungAutoSleepAfterPush(cfg),
			SleepAfterPushSeconds: samsungSleepAfterPushSec(cfg),
			OvernightDeepSleep:    samsungOvernightDeepSleepEnabled(cfg),
			DeepSleepActive:       cfg.DeepSleepActive,
		}
		if dev, ok := devByFrame[id]; ok {
			info.DeviceID = dev.ID
			info.IP = dev.IP
			info.LastSeen = dev.LastSeen
			info.LastAction = dev.LastAction
			info.Battery = dev.Battery
			info.PowerSource = dev.PowerSource
			sum := h.samsungBatterySummary(dev.ID, 5)
			info.BatterySamples = sum.Samples
			info.BatteryDelta = sum.Delta
			info.BatteryPushDelta = sum.PushDelta
			info.BatteryAt = sum.LastAt
			info.BatteryHistory = sum.Recent
			cp := dev
			ApplySamsungConnected(&cp)
			info.Connected = cp.Connected
			if info.Name == "" && dev.Name != "" {
				info.Name = dev.Name
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		la := strings.ToLower(samsungListLabel(out[i].Name, out[i].IP, out[i].ID))
		lb := strings.ToLower(samsungListLabel(out[j].Name, out[j].IP, out[j].ID))
		if la != lb {
			return la < lb
		}
		return out[i].ID < out[j].ID
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func samsungListLabel(name, ip, id string) string {
	if s := strings.TrimSpace(name); s != "" {
		return s
	}
	if ip != "" {
		return ip
	}
	return id
}

func (h *Hub) handleSamsungPoll(w http.ResponseWriter, r *http.Request) {
	devs := h.devices.List()
	probed := 0
	awake := 0
	battery := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range devs {
		if d.Type != DeviceTypeSamsung || d.IP == "" {
			continue
		}
		probed++
		ip := d.IP
		pin := d.MDCPin
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := QueryMDCBatteryLevel(ip, pin)
			if res.SessionOK {
				h.devices.TouchSamsung(ip, "mdc_session")
				h.ensureSamsungMAC(ip, pin)
				mu.Lock()
				awake++
				mu.Unlock()
			}
			if err != nil {
				return
			}
			h.recordSamsungBattery(ip, res.Percent, res.PowerSource, samsungBatteryPoll)
			mu.Lock()
			battery++
			mu.Unlock()
		}()
	}
	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"probed":  probed,
		"awake":   awake,
		"battery": battery,
	})
}
