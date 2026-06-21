package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultPollInterval = 60
const widgetName = "JoyousWidget"
const widgetFile = "joyous-widget.wgt"

// SamsungFrameConfig is persisted per frame under {data-dir}/samsung/{frameId}.json.
type SamsungFrameConfig struct {
	FrameID             string `json:"frame_id"`
	Name                string `json:"name,omitempty"`
	PollIntervalMinutes int    `json:"poll_interval_minutes"`
	InactiveBegin       string `json:"inactive_begin"` // "HH:MM" or empty
	InactiveEnd         string `json:"inactive_end"`   // "HH:MM" or empty
	CropFormat          string `json:"crop_format,omitempty"`
	DisplayWidth        int    `json:"display_width,omitempty"`
	DisplayHeight       int    `json:"display_height,omitempty"`
}

// SamsungStore manages Samsung EM32DX frame images and config on disk.
type SamsungStore struct {
	dir string
}

// NewSamsungStore creates a SamsungStore rooted at dir/samsung.
func NewSamsungStore(dir string) *SamsungStore {
	return &SamsungStore{dir: filepath.Join(dir, "samsung")}
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

func (s *SamsungStore) wgtPath() string {
	return filepath.Join(s.dir, widgetFile)
}

func validFrameID(id string) bool {
	if id == "" || strings.ContainsAny(id, "/\\..") {
		return false
	}
	return true
}

func defaultSamsungConfig(frameID string) SamsungFrameConfig {
	def := defaultSamsungDisplayProfile()
	return SamsungFrameConfig{
		FrameID:             frameID,
		PollIntervalMinutes: defaultPollInterval,
		CropFormat:          def.CropFormat,
		DisplayWidth:        def.Width,
		DisplayHeight:       def.Height,
	}
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
		if validFrameID(id) {
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

// writePNGLocked replaces the frame PNG using lockfile protocol.
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
	if err := os.WriteFile(tmp, pngData, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.pngPath(frameID))
}

// StoreUpload decodes raw image bytes, dithers to Samsung palette, and writes PNG.
func (s *SamsungStore) StoreUpload(frameID string, raw []byte, profile SamsungDisplayProfile) error {
	pngData, err := convertToSamsungPNG(raw, profile, CropRect{}, false)
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

// convertToSamsungPNG applies saved crop metadata (or center-crops) then dithers to PaletteSamsung.
func convertToSamsungPNG(raw []byte, profile SamsungDisplayProfile, crop CropRect, hasCrop bool) ([]byte, error) {
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
	n := UniqueColors(img)
	var out image.Image
	if n <= 6 {
		out = renderIndicesToRGB(StuckiDither(img, PaletteSamsung), PaletteSamsung)
	} else {
		enhanced := LABEnhance(img, 1.0)
		out = renderIndicesToRGB(StuckiDither(enhanced, PaletteSamsung), PaletteSamsung)
	}
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

// ── inactive hours helpers (shared with widget logic in tests) ───────────────

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
	ETag                string    `json:"etag"`
	Locked              bool      `json:"locked"`
	Modified            time.Time `json:"modified,omitempty"`
	HasImage            bool      `json:"has_image"`
	Name                string    `json:"name,omitempty"`
	PollIntervalMinutes int       `json:"poll_interval_minutes"`
	InactiveBegin       string    `json:"inactive_begin"`
	InactiveEnd         string    `json:"inactive_end"`
	CropFormat          string    `json:"crop_format"`
	DisplayWidth        int       `json:"display_width"`
	DisplayHeight       int       `json:"display_height"`
}

func (h *Hub) handleSamsungStatus(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag, mod, hasImage := h.samsung.PNGInfo(frameID)
	resp := samsungStatusResponse{
		ETag:                etag,
		Locked:              h.samsung.IsLocked(frameID),
		HasImage:            hasImage,
		PollIntervalMinutes: cfg.PollIntervalMinutes,
		Name:                cfg.Name,
		InactiveBegin:       cfg.InactiveBegin,
		InactiveEnd:         cfg.InactiveEnd,
		CropFormat:          cfg.CropFormat,
		DisplayWidth:        cfg.DisplayWidth,
		DisplayHeight:       cfg.DisplayHeight,
	}
	if hasImage {
		resp.Modified = mod
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Hub) handleSamsungPNG(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
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
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == `"`+etag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

func (h *Hub) handleSamsungContentJSON(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
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
	addr := h.serverAddr
	if addr == "" {
		addr = r.Host
	}
	fileID := getSamsungPushFileID(frameID)
	if fileID == "" {
		fileID = frameID
	}
	frameIP := frameIDToIP(frameID)
	imageURL := samsungImageURL(addr, frameIP, frameID)
	manifest := buildContentJSON(imageURL, fileID, len(data))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(manifest)
}

func (h *Hub) handleSamsungImage(w http.ResponseWriter, r *http.Request, frameID string) {
	h.handleSamsungPNG(w, r, frameID)
}

func (h *Hub) handleSamsungLock(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// syncSamsungDeviceName copies the saved Samsung friendly name into the device registry.
func (h *Hub) syncSamsungDeviceName(frameID, name string) {
	ip := frameIDToIP(frameID)
	if ip == "" {
		return
	}
	if h.devices.SetName(samsungID(ip), strings.TrimSpace(name)) {
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
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
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
	if ip := frameIDToIP(frameID); ip != "" {
		dev, _ = h.devices.Get(samsungID(ip))
	}
	profile := h.samsungDisplayProfile(dev, frameID)
	if err := h.samsung.StoreUpload(frameID, raw, profile); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	if frames == nil {
		frames = []string{}
	}
	type frameInfo struct {
		ID                  string `json:"id"`
		HasImage            bool   `json:"has_image"`
		Locked              bool   `json:"locked"`
		ETag                string `json:"etag,omitempty"`
		PollIntervalMinutes int    `json:"poll_interval_minutes"`
		InactiveBegin       string `json:"inactive_begin"`
		InactiveEnd         string `json:"inactive_end"`
		CropFormat          string `json:"crop_format"`
		DisplayWidth        int    `json:"display_width"`
		DisplayHeight       int    `json:"display_height"`
	}
	out := make([]frameInfo, 0, len(frames))
	for _, id := range frames {
		cfg, _ := h.samsung.LoadConfig(id)
		etag, _, hasImage := h.samsung.PNGInfo(id)
		out = append(out, frameInfo{
			ID:                  id,
			HasImage:            hasImage,
			Locked:              h.samsung.IsLocked(id),
			ETag:                etag,
			PollIntervalMinutes: cfg.PollIntervalMinutes,
			InactiveBegin:       cfg.InactiveBegin,
			InactiveEnd:         cfg.InactiveEnd,
			CropFormat:          cfg.CropFormat,
			DisplayWidth:        cfg.DisplayWidth,
			DisplayHeight:       cfg.DisplayHeight,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

type ssspConfig struct {
	XMLName    xml.Name `xml:"widget"`
	Ver        int      `xml:"ver"`
	Size       int64    `xml:"size"`
	WidgetName string   `xml:"widgetname"`
	Source     string   `xml:"source,omitempty"`
	WebType    string   `xml:"webtype"`
}

func (h *Hub) handleSamsungSSSPConfig(w http.ResponseWriter, r *http.Request) {
	wgtPath := h.samsung.wgtPath()
	fi, err := os.Stat(wgtPath)
	if err != nil {
		http.Error(w, "widget not installed: place "+widgetFile+" in data/samsung/", http.StatusNotFound)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h.serverAddr != "" {
		host = h.serverAddr
	}
	source := fmt.Sprintf("%s://%s/samsung/%s", scheme, host, widgetFile)
	ver := fi.ModTime().Unix()
	cfg := ssspConfig{
		Ver:        int(ver),
		Size:       fi.Size(),
		WidgetName: widgetName,
		Source:     source,
		WebType:    "tizen",
	}
	out, err := xml.MarshalIndent(cfg, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(xml.Header))
	w.Write(out)
}

func (h *Hub) handleSamsungWGT(w http.ResponseWriter, r *http.Request) {
	path := h.samsung.wgtPath()
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "widget not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/widget")
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(data)), 10))
	w.Write(data)
}

func (h *Hub) handleSamsungIndex(w http.ResponseWriter, r *http.Request) {
	addr := h.serverAddr
	if addr == "" {
		addr = r.Host
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "Samsung EM32DX widget deployment\n\nInstall URL (Custom App in Samsung E-Paper app):\n  http://%s/samsung/\n\nEndpoints:\n  GET /samsung/sssp_config.xml\n  GET /samsung/%s\n", addr, widgetFile)
}
