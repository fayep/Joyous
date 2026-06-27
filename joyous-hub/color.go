package main

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
)

const colorConfigFile = "color.json"

const (
	ColorPresetCalibrated = "calibrated"
	ColorPresetLegacy     = "legacy"
	ColorPresetSRGB       = "srgb"
	ColorPresetReflection = "reflection"
	ColorPresetCustom     = "custom"
)

var colorNames = []string{"black", "white", "yellow", "red", "blue", "green"}

// PaletteRGB is one ink slot as 0–255 RGB (JSON-friendly).
type PaletteRGB [3]int

// ColorConfig holds persisted color pipeline settings.
type ColorConfig struct {
	LABChromaEnabled     bool    `json:"lab_chroma_enabled"`
	LABChromaStrength    float64 `json:"lab_chroma_strength"`
	LABHighlightEnabled  bool    `json:"lab_highlight_enabled"`
	LABHighlightStrength float64 `json:"lab_highlight_strength"`
	LABShadowEnabled     bool    `json:"lab_shadow_enabled"`
	LABShadowStrength    float64 `json:"lab_shadow_strength"`

	InkJoyDisplayPreset  string `json:"inkjoy_display_preset"`
	SamsungDisplayPreset string `json:"samsung_display_preset"`
	SamsungSendPreset    string `json:"samsung_send_preset"`

	InkJoyDisplay  [6]PaletteRGB `json:"inkjoy_display,omitempty"`
	SamsungDisplay [6]PaletteRGB `json:"samsung_display,omitempty"`
	SamsungSend    [6]PaletteRGB `json:"samsung_send,omitempty"`
}

// ColorPipeline is the resolved palette + LAB settings used when encoding.
type ColorPipeline struct {
	LABChromaEnabled     bool
	LABChromaStrength    float64
	LABHighlightEnabled  bool
	LABHighlightStrength float64
	LABShadowEnabled     bool
	LABShadowStrength    float64
	InkJoyDisplay        [6][3]float64
	SamsungDisplay       [6][3]float64
	SamsungSend          [6][3]float64
}

// PaletteReflection — Reflection Spectra 6 physical pigments.
var PaletteReflection = [6][3]float64{
	{8, 0, 0},
	{239, 255, 255},
	{255, 215, 0},
	{134, 0, 0},
	{0, 28, 138},
	{20, 93, 20},
}

// ColorStore persists color.json under the hub data directory.
type ColorStore struct {
	dir string
	mu  sync.RWMutex
	cfg ColorConfig
}

func NewColorStore(dataDir string) *ColorStore {
	s := &ColorStore{dir: dataDir, cfg: defaultColorConfig()}
	s.load()
	return s
}

func defaultColorConfig() ColorConfig {
	return ColorConfig{
		LABChromaEnabled:     false,
		LABChromaStrength:    1.0,
		LABHighlightEnabled:  false,
		LABHighlightStrength: 1.0,
		LABShadowEnabled:     false,
		LABShadowStrength:    1.0,
		InkJoyDisplayPreset:  ColorPresetCalibrated,
		SamsungDisplayPreset: ColorPresetCalibrated,
		SamsungSendPreset:    ColorPresetCalibrated,
	}
}

func (s *ColorStore) path() string { return filepath.Join(s.dir, colorConfigFile) }

func (s *ColorStore) load() {
	data, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	var raw map[string]json.RawMessage
	var cfg ColorConfig
	if json.Unmarshal(data, &raw) != nil || json.Unmarshal(data, &cfg) != nil {
		return
	}
	if _, ok := raw["lab_chroma_enabled"]; !ok {
		migrateLegacyLABConfig(&cfg, raw)
	}
	s.cfg = normalizeColorConfig(cfg)
}

func migrateLegacyLABConfig(cfg *ColorConfig, raw map[string]json.RawMessage) {
	if v, ok := raw["lab_enhance"]; ok {
		var on bool
		if json.Unmarshal(v, &on) == nil {
			cfg.LABChromaEnabled = on
			cfg.LABHighlightEnabled = on
		}
	}
	if v, ok := raw["lab_strength"]; ok {
		var strength float64
		if json.Unmarshal(v, &strength) == nil && strength > 0 {
			cfg.LABChromaStrength = strength
			cfg.LABHighlightStrength = strength
		}
	}
}

func normalizeColorConfig(cfg ColorConfig) ColorConfig {
	if cfg.LABChromaStrength <= 0 {
		cfg.LABChromaStrength = 1.0
	}
	if cfg.LABHighlightStrength <= 0 {
		cfg.LABHighlightStrength = 1.0
	}
	if cfg.LABShadowStrength <= 0 {
		cfg.LABShadowStrength = 1.0
	}
	if cfg.InkJoyDisplayPreset == "" {
		cfg.InkJoyDisplayPreset = ColorPresetCalibrated
	}
	if cfg.SamsungDisplayPreset == "" {
		cfg.SamsungDisplayPreset = ColorPresetCalibrated
	}
	if cfg.SamsungSendPreset == "" {
		cfg.SamsungSendPreset = ColorPresetCalibrated
	}
	return cfg
}

func (s *ColorStore) Config() ColorConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *ColorStore) Save(cfg ColorConfig) error {
	cfg = normalizeColorConfig(cfg)
	if err := validateColorConfig(cfg); err != nil {
		return err
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

func (s *ColorStore) Pipeline() ColorPipeline {
	return ResolveColorPipeline(s.Config())
}

func ResolveColorPipeline(cfg ColorConfig) ColorPipeline {
	return ColorPipeline{
		LABChromaEnabled:     cfg.LABChromaEnabled,
		LABChromaStrength:    cfg.LABChromaStrength,
		LABHighlightEnabled:  cfg.LABHighlightEnabled,
		LABHighlightStrength: cfg.LABHighlightStrength,
		LABShadowEnabled:     cfg.LABShadowEnabled,
		LABShadowStrength:    cfg.LABShadowStrength,
		InkJoyDisplay:        resolvePalette(cfg.InkJoyDisplayPreset, cfg.InkJoyDisplay, inkjoyDisplayPresets()),
		SamsungDisplay:       resolvePalette(cfg.SamsungDisplayPreset, cfg.SamsungDisplay, samsungDisplayPresets()),
		SamsungSend:          resolvePalette(cfg.SamsungSendPreset, cfg.SamsungSend, samsungSendPresets()),
	}
}

func inkjoyDisplayPresets() map[string][6][3]float64 {
	return map[string][6][3]float64{
		ColorPresetCalibrated: PaletteInkJoyDisplay,
		ColorPresetLegacy:     PaletteInkJoy,
		ColorPresetSRGB:       PaletteInkJoySend,
		ColorPresetReflection: PaletteReflection,
	}
}

func samsungDisplayPresets() map[string][6][3]float64 {
	return map[string][6][3]float64{
		ColorPresetCalibrated: PaletteSamsungDisplay,
		ColorPresetLegacy:     PaletteInkJoy,
		ColorPresetSRGB:       PaletteSamsungSend,
		ColorPresetReflection: PaletteReflection,
	}
}

func samsungSendPresets() map[string][6][3]float64 {
	return map[string][6][3]float64{
		ColorPresetCalibrated: PaletteSamsungSend,
		ColorPresetLegacy:     PaletteInkJoy,
		ColorPresetSRGB:       PaletteSamsungSend,
		ColorPresetReflection: PaletteReflection,
	}
}

func resolvePalette(preset string, custom [6]PaletteRGB, presets map[string][6][3]float64) [6][3]float64 {
	if preset == ColorPresetCustom {
		return paletteFromRGB(custom)
	}
	if p, ok := presets[preset]; ok {
		return p
	}
	return PaletteInkJoyDisplay
}

func paletteFromRGB(custom [6]PaletteRGB) [6][3]float64 {
	var out [6][3]float64
	for i := range out {
		out[i] = [3]float64{
			float64(clampInt(custom[i][0], 0, 255)),
			float64(clampInt(custom[i][1], 0, 255)),
			float64(clampInt(custom[i][2], 0, 255)),
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func validateColorConfig(cfg ColorConfig) error {
	if cfg.LABChromaStrength < 0 || cfg.LABChromaStrength > 3 {
		return fmt.Errorf("lab_chroma_strength must be between 0 and 3")
	}
	if cfg.LABHighlightStrength < 0 || cfg.LABHighlightStrength > 3 {
		return fmt.Errorf("lab_highlight_strength must be between 0 and 3")
	}
	if cfg.LABShadowStrength < 0 || cfg.LABShadowStrength > 3 {
		return fmt.Errorf("lab_shadow_strength must be between 0 and 3")
	}
	for _, preset := range []string{cfg.InkJoyDisplayPreset, cfg.SamsungDisplayPreset, cfg.SamsungSendPreset} {
		if err := validatePresetName(preset); err != nil {
			return err
		}
	}
	return nil
}

func validatePresetName(preset string) error {
	switch preset {
	case ColorPresetCalibrated, ColorPresetLegacy, ColorPresetSRGB, ColorPresetReflection, ColorPresetCustom:
		return nil
	default:
		return fmt.Errorf("unknown palette preset %q", preset)
	}
}

func paletteToJSON(p [6][3]float64) [6]PaletteRGB {
	var out [6]PaletteRGB
	for i := range out {
		out[i] = PaletteRGB{int(p[i][0]), int(p[i][1]), int(p[i][2])}
	}
	return out
}

func colorPresetCatalog() map[string]map[string][6]PaletteRGB {
	catalog := map[string]map[string][6]PaletteRGB{
		"inkjoy_display":  {},
		"samsung_display": {},
		"samsung_send":    {},
	}
	for name, pal := range inkjoyDisplayPresets() {
		catalog["inkjoy_display"][name] = paletteToJSON(pal)
	}
	for name, pal := range samsungDisplayPresets() {
		catalog["samsung_display"][name] = paletteToJSON(pal)
	}
	for name, pal := range samsungSendPresets() {
		catalog["samsung_send"][name] = paletteToJSON(pal)
	}
	return catalog
}

func shouldApplyLABProcessing(pipe ColorPipeline, img image.Image, flatRGB bool) bool {
	if flatRGB || (!pipe.LABChromaEnabled && !pipe.LABHighlightEnabled && !pipe.LABShadowEnabled) {
		return false
	}
	return UniqueColors(img) > 6
}

func defaultColorPipeline() ColorPipeline {
	return ResolveColorPipeline(defaultColorConfig())
}
