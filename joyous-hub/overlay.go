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
	Timezone          string  `json:"timezone,omitempty"`
	Template          string  `json:"template,omitempty"` // Go text/template; see OverlayTemplateData
	ShowPhotoName     bool    `json:"show_photo_name"`
	PhotoNamePosition string  `json:"photo_name_position"` // bottom_right, bottom_center
	WeatherStyle      string  `json:"weather_style"`       // box, outline
}

const (
	overlayWeatherStyleBox     = "box"
	overlayWeatherStyleOutline = "outline"
)

func normalizeWeatherStyle(s string) string {
	if strings.TrimSpace(s) == overlayWeatherStyleOutline {
		return overlayWeatherStyleOutline
	}
	return overlayWeatherStyleBox
}

func overlayWeatherUsesOutline(cfg OverlayConfig) bool {
	return normalizeWeatherStyle(cfg.WeatherStyle) == overlayWeatherStyleOutline
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
		Template:          defaultOverlayTemplate(),
		PhotoNamePosition: overlayPhotoNameBottomRight,
		WeatherStyle:      overlayWeatherStyleBox,
	}
}

// WeatherSnapshot is live weather data composited onto a frame image.
type WeatherSnapshot struct {
	TempC         float64              `json:"temp_c"` // current °C (alias for Temperature.Current)
	Condition     string               `json:"condition"`
	City          string               `json:"city"`
	ObservedAt    time.Time            `json:"observed_at"`
	DisplayDate   time.Time            `json:"display_date"`
	Temperature   OverlayTemperature   `json:"temperature"`
	Precipitation OverlayPrecipitation `json:"precipitation"`
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
	cfg.PhotoNamePosition = normalizePhotoNamePosition(cfg.PhotoNamePosition)
	cfg.WeatherStyle = normalizeWeatherStyle(cfg.WeatherStyle)
	if t := strings.TrimSpace(cfg.Template); t != "" {
		if _, err := executeOverlayTemplate(cfg, WeatherSnapshot{}); err != nil {
			return err
		}
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
	if overlayNeedsWeather(cfg) {
		if strings.TrimSpace(cfg.Location) == "" && cfg.Latitude == 0 && cfg.Longitude == 0 {
			return false
		}
	}
	return true
}

func overlayHasContent(cfg OverlayConfig) bool {
	if cfg.ShowPhotoName {
		return true
	}
	return strings.TrimSpace(effectiveOverlayTemplate(cfg)) != ""
}

func (cfg OverlayConfig) sendToken(weather WeatherSnapshot, portrait bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "v4|%t|%s|%t|%d|%s|%.4f|%.4f|%s|%s|%.1f|%.1f|%.1f|%d|%d|%s|%s|%t|%s|%s",
		portrait, cfg.Layout, cfg.UseFahrenheit, cfg.DateStyle,
		cfg.Location, cfg.Latitude, cfg.Longitude, cfg.Timezone,
		effectiveOverlayTemplate(cfg),
		weather.Temperature.Current, weather.Temperature.Min, weather.Temperature.Max,
		weather.Precipitation.Hour, weather.Precipitation.Max,
		weather.Condition, weather.DisplayDate.Format("2006-01-02"),
		cfg.ShowPhotoName, normalizePhotoNamePosition(cfg.PhotoNamePosition),
		normalizeWeatherStyle(cfg.WeatherStyle))
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
	q.Set("daily", "temperature_2m_max,temperature_2m_min,precipitation_probability_max")
	q.Set("hourly", "precipitation_probability")
	q.Set("forecast_days", "1")
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
		Daily struct {
			Time                     []string  `json:"time"`
			Temperature2mMax         []float64 `json:"temperature_2m_max"`
			Temperature2mMin         []float64 `json:"temperature_2m_min"`
			PrecipitationProbMax     []int     `json:"precipitation_probability_max"`
		} `json:"daily"`
		Hourly struct {
			Time                    []string `json:"time"`
			PrecipitationProbability []int    `json:"precipitation_probability"`
		} `json:"hourly"`
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
	tempC := payload.Current.Temperature2m
	precipHour := 0
	if i := hourlyIndexFor(payload.Hourly.Time, observed); i >= 0 && i < len(payload.Hourly.PrecipitationProbability) {
		precipHour = payload.Hourly.PrecipitationProbability[i]
	}
	tempMin, tempMax := tempC, tempC
	precipMax := 0
	if len(payload.Daily.Temperature2mMin) > 0 {
		tempMin = payload.Daily.Temperature2mMin[0]
	}
	if len(payload.Daily.Temperature2mMax) > 0 {
		tempMax = payload.Daily.Temperature2mMax[0]
	}
	if len(payload.Daily.PrecipitationProbMax) > 0 {
		precipMax = payload.Daily.PrecipitationProbMax[0]
	}
	return WeatherSnapshot{
		TempC:       tempC,
		Condition:   wmoWeatherText(payload.Current.WeatherCode),
		City:        city,
		ObservedAt:  observed,
		DisplayDate: observed,
		Temperature: OverlayTemperature{Current: tempC, Min: tempMin, Max: tempMax},
		Precipitation: OverlayPrecipitation{Hour: precipHour, Max: precipMax},
	}, nil
}

func hourlyIndexFor(times []string, at time.Time) int {
	if len(times) == 0 {
		return -1
	}
	hourKey := at.Format("2006-01-02T15:04")
	if len(hourKey) >= 13 {
		hourKey = hourKey[:13] + ":00"
	}
	for i, t := range times {
		if strings.HasPrefix(t, hourKey) || t == hourKey {
			return i
		}
	}
	// Fallback: nearest hour at or before observed time.
	best := -1
	for i, t := range times {
		if t <= hourKey {
			best = i
		}
	}
	return best
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
	case 1:
		return "Mainly clear"
	case 2:
		return "Partly cloudy"
	case 3:
		return "Overcast"
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
	return strconv.Itoa(int(tempC+0.5)) + "C"
}

func (h *Hub) fetchOverlayWeather(ctx context.Context) (WeatherSnapshot, error) {
	if h.overlay == nil {
		return WeatherSnapshot{}, nil
	}
	cfg := h.overlay.Config()
	if !overlayNeedsWeather(cfg) {
		return WeatherSnapshot{}, nil
	}
	if h.weather == nil {
		h.weather = &weatherFetcher{client: &http.Client{Timeout: 12 * time.Second}}
	}
	return h.weather.Fetch(ctx, cfg)
}

func (h *Hub) overlayPhotoName(imageID string, cfg OverlayConfig) string {
	if !cfg.ShowPhotoName || imageID == "" || h.images == nil {
		return ""
	}
	meta, err := h.images.readMeta(imageID)
	if err != nil {
		return ""
	}
	return overlayPhotoNameFromFilename(meta.Name)
}

func (h *Hub) ensureOverlayBin(imageID string, portrait bool, cfg OverlayConfig, weather WeatherSnapshot) (string, error) {
	token := cfg.sendToken(weather, portrait)
	photoName := h.overlayPhotoName(imageID, cfg)
	_, err := h.images.ServeBinOrientationOverlay(imageID, portrait, token, func(img image.Image, flatRGB bool) (image.Image, error) {
		return drawWeatherOverlay(img, cfg, weather, photoName, portrait), nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func mergeOverlayConfig(base OverlayConfig, override *OverlayConfig) OverlayConfig {
	if override == nil {
		return base
	}
	merged := base
	merged.Template = override.Template
	if override.Location != "" {
		merged.Location = override.Location
	}
	if override.Latitude != 0 || override.Longitude != 0 {
		merged.Latitude = override.Latitude
		merged.Longitude = override.Longitude
	}
	if override.Timezone != "" {
		merged.Timezone = override.Timezone
	}
	merged.UseFahrenheit = override.UseFahrenheit
	if override.DateStyle != 0 {
		merged.DateStyle = override.DateStyle
	}
	merged.Enabled = override.Enabled
	merged.ShowPhotoName = override.ShowPhotoName
	if override.PhotoNamePosition != "" {
		merged.PhotoNamePosition = override.PhotoNamePosition
	}
	merged.WeatherStyle = normalizeWeatherStyle(override.WeatherStyle)
	return merged
}

func (h *Hub) renderOverlayPreviewJPEG(ctx context.Context, imageID string, portrait bool, cfgOverride *OverlayConfig) ([]byte, error) {
	cfg := mergeOverlayConfig(h.overlay.Config(), cfgOverride)
	if !cfg.Enabled || !overlayHasContent(cfg) {
		return h.images.framePreviewJPEG(imageID, portrait, "")
	}
	weather, err := h.fetchOverlayWeather(ctx)
	if err != nil {
		return nil, err
	}
	photoName := h.overlayPhotoName(imageID, cfg)
	token := cfg.sendToken(weather, portrait)
	bin, err := h.images.ServeBinOrientationOverlay(imageID, portrait, token, func(img image.Image, flatRGB bool) (image.Image, error) {
		return drawWeatherOverlay(img, cfg, weather, photoName, portrait), nil
	})
	if err != nil {
		return nil, err
	}
	img, err := h.images.decodeBinToImage(bin, portrait)
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
