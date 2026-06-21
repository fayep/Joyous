package main

import (
	"strconv"
	"strings"
)

// SamsungDisplayProfile describes target framing for MDC/widget output.
type SamsungDisplayProfile struct {
	CropFormat string // metadata crop key, e.g. "16:9"
	Width      int
	Height     int
}

func defaultSamsungDisplayProfile() SamsungDisplayProfile {
	return SamsungDisplayProfile{
		CropFormat: "16:9",
		Width:      samsungW,
		Height:     samsungH,
	}
}

// inferSamsungDisplay guesses crop format and pixel size from UPnP/SSDP hints.
func inferSamsungDisplay(server, description string) SamsungDisplayProfile {
	combined := strings.ToLower(server + " " + description)
	p := defaultSamsungDisplayProfile()

	if w, h, ok := parseResolution(combined); ok {
		p.Width, p.Height = w, h
		p.CropFormat = cropFormatForSize(w, h)
		return p
	}
	if strings.Contains(combined, "portrait") || strings.Contains(combined, "9:16") {
		p.CropFormat = "9:16"
		p.Width, p.Height = 1440, 2560
		return p
	}
	if strings.Contains(combined, "em32") || strings.Contains(combined, "emdx") || strings.Contains(combined, "epaper") {
		return p
	}
	return p
}

func parseResolution(s string) (w, h int, ok bool) {
	for i := 0; i < len(s)-4; i++ {
		if s[i] < '0' || s[i] > '9' {
			continue
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j >= len(s) || (s[j] != 'x' && s[j] != 'X') {
			continue
		}
		k := j + 1
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		w1, err1 := strconv.Atoi(s[i:j])
		w2, err2 := strconv.Atoi(s[j+1 : k])
		if err1 != nil || err2 != nil || w1 < 100 || w2 < 100 {
			continue
		}
		return w1, w2, true
	}
	return 0, 0, false
}

func cropFormatForSize(w, h int) string {
	if w <= 0 || h <= 0 {
		return "16:9"
	}
	if w == h {
		return "1:1"
	}
	if w > h {
		switch {
		case ratioClose(float64(w)/float64(h), 16.0/9.0):
			return "16:9"
		case ratioClose(float64(w)/float64(h), 4.0/3.0):
			return "4:3"
		}
		return "16:9"
	}
	switch {
	case ratioClose(float64(h)/float64(w), 16.0/9.0):
		return "9:16"
	case ratioClose(float64(h)/float64(w), 4.0/3.0):
		return "3:4"
	}
	return "9:16"
}

func ratioClose(a, b float64) bool {
	if a == 0 || b == 0 {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.05
}

func (h *Hub) samsungDisplayProfile(dev *Device, frameID string) SamsungDisplayProfile {
	p := defaultSamsungDisplayProfile()
	if dev != nil {
		if dev.DisplayCropFormat != "" {
			p.CropFormat = dev.DisplayCropFormat
		}
		if dev.DisplayWidth > 0 && dev.DisplayHeight > 0 {
			p.Width, p.Height = dev.DisplayWidth, dev.DisplayHeight
		}
	}
	if cfg, err := h.samsung.LoadConfig(frameID); err == nil {
		if cfg.CropFormat != "" {
			p.CropFormat = cfg.CropFormat
		}
		if cfg.DisplayWidth > 0 && cfg.DisplayHeight > 0 {
			p.Width, p.Height = cfg.DisplayWidth, cfg.DisplayHeight
		}
	}
	if p.Width <= 0 || p.Height <= 0 {
		def := defaultSamsungDisplayProfile()
		if p.CropFormat == "9:16" || p.CropFormat == "3:4" {
			p.Width, p.Height = 1440, 2560
		} else {
			p.Width, p.Height = def.Width, def.Height
		}
	}
	if p.CropFormat == "" {
		p.CropFormat = cropFormatForSize(p.Width, p.Height)
	}
	return p
}

func applySamsungDisplayProfile(d *Device, p SamsungDisplayProfile) {
	if d == nil || p.CropFormat == "" {
		return
	}
	if d.DisplayCropFormat == "" {
		d.DisplayCropFormat = p.CropFormat
	}
	if d.DisplayWidth <= 0 && p.Width > 0 {
		d.DisplayWidth = p.Width
	}
	if d.DisplayHeight <= 0 && p.Height > 0 {
		d.DisplayHeight = p.Height
	}
}
