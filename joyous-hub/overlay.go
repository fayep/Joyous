package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const overlayConfigFile = "overlay.json"

// OverlayConfig holds persisted overlay settings (separate from album images).
type OverlayConfig struct {
	Enabled       bool    `json:"enabled"`
	Layout        string  `json:"layout"` // bottom_bar
	ShowDate      bool    `json:"show_date"`
	ShowTemp      bool    `json:"show_temp"`
	ShowCondition bool    `json:"show_condition"`
	ShowCity      bool    `json:"show_city"`
	UseFahrenheit bool    `json:"use_fahrenheit"`
	DateStyle     int     `json:"date_style"` // 1=Jun 20, 2026
	Location      string  `json:"location"`
	Latitude      float64 `json:"latitude,omitempty"`
	Longitude     float64 `json:"longitude,omitempty"`
	Timezone      string  `json:"timezone,omitempty"`
}

func defaultOverlayConfig() OverlayConfig {
	return OverlayConfig{
		Enabled:       true,
		Layout:        "bottom_bar",
		ShowDate:      true,
		ShowTemp:      true,
		ShowCondition: true,
		ShowCity:      true,
		UseFahrenheit: true,
		DateStyle:     1,
	}
}

// WeatherSnapshot is live weather data composited onto a frame image.
type WeatherSnapshot struct {
	TempC       float64   `json:"temp_c"`
	Condition   string    `json:"condition"`
	City        string    `json:"city"`
	ObservedAt  time.Time `json:"observed_at"`
	DisplayDate time.Time `json:"display_date"`
}

// OverlayStore persists overlay.json under the hub data directory.
type OverlayStore struct {
	dir string
	mu  sync.RWMutex
	cfg OverlayConfig
}

func NewOverlayStore(dataDir string) *OverlayStore {
	s := &OverlayStore{dir: dataDir, cfg: defaultOverlayConfig()}
	s.load()
	return s
}

func (s *OverlayStore) path() string { return filepath.Join(s.dir, overlayConfigFile) }

func (s *OverlayStore) load() {
	data, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	var cfg OverlayConfig
	if json.Unmarshal(data, &cfg) == nil {
		s.cfg = cfg
	}
}

func (s *OverlayStore) Config() OverlayConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *OverlayStore) Save(cfg OverlayConfig) error {
	if cfg.Layout == "" {
		cfg.Layout = "bottom_bar"
	}
	if cfg.DateStyle == 0 {
		cfg.DateStyle = 1
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), b, 0644)
}

func (s *OverlayStore) Active() bool {
	cfg := s.Config()
	if !cfg.Enabled || !overlayHasContent(cfg) {
		return false
	}
	if strings.TrimSpace(cfg.Location) == "" && cfg.Latitude == 0 && cfg.Longitude == 0 {
		return false
	}
	return true
}

func overlayHasContent(cfg OverlayConfig) bool {
	return cfg.ShowDate || cfg.ShowTemp || cfg.ShowCondition || cfg.ShowCity
}

func (cfg OverlayConfig) sendToken(weather WeatherSnapshot, portrait bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "v1|%t|%s|%t|%t|%t|%t|%t|%d|%s|%.4f|%.4f|%s|%.1f|%s|%s",
		portrait, cfg.Layout, cfg.ShowDate, cfg.ShowTemp, cfg.ShowCondition, cfg.ShowCity,
		cfg.UseFahrenheit, cfg.DateStyle, cfg.Location, cfg.Latitude, cfg.Longitude, cfg.Timezone,
		weather.TempC, weather.Condition, weather.DisplayDate.Format("2006-01-02"))
	return hex.EncodeToString(h.Sum(nil))[:10]
}

type weatherClient interface {
	Fetch(ctx context.Context, cfg OverlayConfig) (WeatherSnapshot, error)
}

type weatherFetcher struct {
	client *http.Client
}

func (f *weatherFetcher) Fetch(ctx context.Context, cfg OverlayConfig) (WeatherSnapshot, error) {
	lat, lon, city, err := f.resolveCoords(ctx, cfg)
	if err != nil {
		return WeatherSnapshot{}, err
	}
	tz := strings.TrimSpace(cfg.Timezone)
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lon))
	q.Set("current", "temperature_2m,weather_code")
	if tz != "" {
		q.Set("timezone", tz)
	} else {
		q.Set("timezone", "auto")
	}
	reqURL := "https://api.open-meteo.com/v1/forecast?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return WeatherSnapshot{}, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return WeatherSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return WeatherSnapshot{}, fmt.Errorf("weather api: %s", resp.Status)
	}
	var payload struct {
		Current struct {
			Time          string  `json:"time"`
			Temperature2m float64 `json:"temperature_2m"`
			WeatherCode   int     `json:"weather_code"`
		} `json:"current"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return WeatherSnapshot{}, err
	}
	observed, _ := time.Parse(time.RFC3339, payload.Current.Time)
	if observed.IsZero() {
		observed = time.Now()
	}
	if city == "" {
		city = strings.TrimSpace(cfg.Location)
	}
	return WeatherSnapshot{
		TempC:       payload.Current.Temperature2m,
		Condition:   wmoWeatherText(payload.Current.WeatherCode),
		City:        city,
		ObservedAt:  observed,
		DisplayDate: observed,
	}, nil
}

func (f *weatherFetcher) resolveCoords(ctx context.Context, cfg OverlayConfig) (lat, lon float64, city string, err error) {
	if cfg.Latitude != 0 || cfg.Longitude != 0 {
		return cfg.Latitude, cfg.Longitude, strings.TrimSpace(cfg.Location), nil
	}
	name := strings.TrimSpace(cfg.Location)
	if name == "" {
		return 0, 0, "", errors.New("overlay location required (city name or latitude/longitude)")
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("count", "1")
	q.Set("language", "en")
	q.Set("format", "json")
	reqURL := "https://geocoding-api.open-meteo.com/v1/search?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, 0, "", err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, "", fmt.Errorf("geocoding api: %s", resp.Status)
	}
	var payload struct {
		Results []struct {
			Name      string  `json:"name"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Admin1    string  `json:"admin1"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, 0, "", err
	}
	if len(payload.Results) == 0 {
		return 0, 0, "", fmt.Errorf("location %q not found", name)
	}
	r := payload.Results[0]
	city = r.Name
	if r.Admin1 != "" {
		city = r.Name + ", " + r.Admin1
	}
	return r.Latitude, r.Longitude, city, nil
}

func wmoWeatherText(code int) string {
	switch code {
	case 0:
		return "Clear"
	case 1, 2, 3:
		return "Partly cloudy"
	case 45, 48:
		return "Fog"
	case 51, 53, 55:
		return "Drizzle"
	case 56, 57:
		return "Freezing drizzle"
	case 61, 63, 65:
		return "Rain"
	case 66, 67:
		return "Freezing rain"
	case 71, 73, 75:
		return "Snow"
	case 77:
		return "Snow grains"
	case 80, 81, 82:
		return "Showers"
	case 85, 86:
		return "Snow showers"
	case 95:
		return "Thunderstorm"
	case 96, 99:
		return "Thunderstorm with hail"
	default:
		return "Weather"
	}
}

func formatOverlayDate(t time.Time, style int) string {
	switch style {
	case 2:
		return t.Format("02 Jan 2006")
	case 3:
		return t.Format("January 02 2006")
	default:
		return t.Format("Jan 02, 2006")
	}
}

func formatOverlayTemp(tempC float64, fahrenheit bool) string {
	if fahrenheit {
		f := tempC*9/5 + 32
		return strconv.Itoa(int(f+0.5)) + "°F"
	}
	return strconv.Itoa(int(tempC+0.5)) + "°C"
}

func (h *Hub) fetchOverlayWeather(ctx context.Context) (WeatherSnapshot, error) {
	if h.weather == nil {
		h.weather = &weatherFetcher{client: &http.Client{Timeout: 12 * time.Second}}
	}
	return h.weather.Fetch(ctx, h.overlay.Config())
}

func (h *Hub) ensureOverlayBin(imageID string, portrait bool, cfg OverlayConfig, weather WeatherSnapshot) (string, error) {
	token := cfg.sendToken(weather, portrait)
	_, err := h.images.ServeBinOrientationOverlay(imageID, portrait, token, func(img image.Image, flatRGB bool) (image.Image, error) {
		return drawWeatherOverlay(img, cfg, weather, portrait), nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (h *Hub) renderOverlayPreviewJPEG(ctx context.Context, imageID string, portrait bool) ([]byte, error) {
	cfg := h.overlay.Config()
	if !cfg.Enabled || !overlayHasContent(cfg) {
		return h.images.framePreviewJPEG(imageID, portrait, "")
	}
	weather, err := h.fetchOverlayWeather(ctx)
	if err != nil {
		return nil, err
	}
	token := cfg.sendToken(weather, portrait)
	bin, err := h.images.ServeBinOrientationOverlay(imageID, portrait, token, func(img image.Image, flatRGB bool) (image.Image, error) {
		return drawWeatherOverlay(img, cfg, weather, portrait), nil
	})
	if err != nil {
		return nil, err
	}
	img, err := decodeBinToImage(bin, portrait)
	if err != nil {
		return nil, err
	}
	preview := scaleInkJoyDisplayPreview(img)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, preview, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func parseImageBinFilename(file string) (id, overlayToken string, portrait bool) {
	portrait = strings.HasSuffix(file, "-p.bin")
	base := strings.TrimSuffix(strings.TrimSuffix(file, "-p.bin"), ".bin")
	if i := strings.Index(base, "~"); i >= 0 {
		return base[:i], base[i+1:], portrait
	}
	return base, "", portrait
}

func imageBinFilename(id, overlayToken string, portrait bool) string {
	name := id
	if overlayToken != "" {
		name += "~" + overlayToken
	}
	if portrait {
		return name + "-p.bin"
	}
	return name + ".bin"
}
