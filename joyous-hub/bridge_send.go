package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"joyous-hub/protocol"
)

// CacheHubImage stores a hub-fetched original and sidecar meta for bridge-side encode.
func (s *ImageStore) CacheHubImage(id string, meta ImageMeta, raw []byte) error {
	if err := os.MkdirAll(s.rawDir(), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(s.rawPath(id), raw, 0644); err != nil {
		return err
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(id), b, 0644)
}

// fetchHubImage downloads album metadata and original bytes from the hub.
func fetchHubImage(ctx context.Context, hubBase, imageID string) (ImageMeta, []byte, error) {
	base := strings.TrimRight(hubBase, "/")
	metaReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/images/"+imageID, nil)
	if err != nil {
		return ImageMeta{}, nil, err
	}
	metaResp, err := http.DefaultClient.Do(metaReq)
	if err != nil {
		return ImageMeta{}, nil, fmt.Errorf("fetch meta: %w", err)
	}
	defer metaResp.Body.Close()
	if metaResp.StatusCode != http.StatusOK {
		return ImageMeta{}, nil, fmt.Errorf("fetch meta: HTTP %d", metaResp.StatusCode)
	}
	var meta ImageMeta
	if err := json.NewDecoder(metaResp.Body).Decode(&meta); err != nil {
		return ImageMeta{}, nil, fmt.Errorf("decode meta: %w", err)
	}

	rawReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/images/"+imageID+"/original", nil)
	if err != nil {
		return ImageMeta{}, nil, err
	}
	rawResp, err := http.DefaultClient.Do(rawReq)
	if err != nil {
		return ImageMeta{}, nil, fmt.Errorf("fetch original: %w", err)
	}
	defer rawResp.Body.Close()
	if rawResp.StatusCode != http.StatusOK {
		return ImageMeta{}, nil, fmt.Errorf("fetch original: HTTP %d", rawResp.StatusCode)
	}
	raw, err := io.ReadAll(rawResp.Body)
	if err != nil {
		return ImageMeta{}, nil, err
	}
	return meta, raw, nil
}

// syncColorFromHub refreshes bridge color settings from the hub (best-effort).
func syncColorFromHub(store *ColorStore, hubBase string) {
	if store == nil {
		return
	}
	base := strings.TrimRight(hubBase, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/color", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	defer resp.Body.Close()
	var cfg ColorConfig
	if json.NewDecoder(resp.Body).Decode(&cfg) == nil {
		_ = store.Save(cfg)
	}
}

func decodeOverlaySendInfo(info *protocol.OverlaySendInfo) (OverlayConfig, WeatherSnapshot, error) {
	if info == nil {
		return OverlayConfig{}, WeatherSnapshot{}, fmt.Errorf("overlay info missing")
	}
	var cfg OverlayConfig
	var weather WeatherSnapshot
	if err := json.Unmarshal(info.Config, &cfg); err != nil {
		return cfg, weather, fmt.Errorf("decode overlay config: %w", err)
	}
	if err := json.Unmarshal(info.Weather, &weather); err != nil {
		return cfg, weather, fmt.Errorf("decode overlay weather: %w", err)
	}
	return cfg, weather, nil
}

func overlaySendInfo(cfg OverlayConfig, weather WeatherSnapshot) (*protocol.OverlaySendInfo, error) {
	cb, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	wb, err := json.Marshal(weather)
	if err != nil {
		return nil, err
	}
	return &protocol.OverlaySendInfo{Config: cb, Weather: wb}, nil
}

func bridgeOverlayContext(ctx context.Context, hub *Hub, body protocol.SendImageBody, portrait bool) (OverlayConfig, WeatherSnapshot, error) {
	if body.Overlay != nil {
		cfg, weather, err := decodeOverlaySendInfo(body.Overlay)
		if err != nil {
			return OverlayConfig{}, WeatherSnapshot{}, err
		}
		if hub.overlay != nil {
			if err := hub.overlay.Save(cfg); err != nil {
				return OverlayConfig{}, WeatherSnapshot{}, err
			}
		}
		if body.OverlayToken != "" {
			if token := cfg.sendToken(weather, portrait); token != body.OverlayToken {
				return OverlayConfig{}, WeatherSnapshot{}, fmt.Errorf("overlay token mismatch")
			}
		}
		return cfg, weather, nil
	}
	if hub.overlay == nil {
		return OverlayConfig{}, WeatherSnapshot{}, fmt.Errorf("overlay store unavailable")
	}
	if err := syncOverlayFromHub(hub.overlay, body.HubBaseURL); err != nil {
		return OverlayConfig{}, WeatherSnapshot{}, err
	}
	weather, err := hub.fetchOverlayWeather(ctx)
	if err != nil {
		return OverlayConfig{}, WeatherSnapshot{}, fmt.Errorf("overlay weather: %w", err)
	}
	cfg := hub.overlay.Config()
	if body.OverlayToken != "" {
		if token := cfg.sendToken(weather, portrait); token != body.OverlayToken {
			return OverlayConfig{}, WeatherSnapshot{}, fmt.Errorf("overlay token mismatch")
		}
	}
	return cfg, weather, nil
}

// bridgeEncodeInkJoy fetches the hub original and encodes an InkJoy .bin locally.
// Crop selection uses the destination device (portrait → 3:4 or 4:3) against body.Crops.
func bridgeEncodeInkJoy(
	ctx context.Context,
	hub *Hub,
	body protocol.SendImageBody,
	dev *Device,
) ([]byte, error) {
	if hub == nil || hub.images == nil {
		return nil, fmt.Errorf("bridge encode: image store unavailable")
	}
	if dev == nil {
		return nil, fmt.Errorf("bridge encode: device required")
	}
	if body.HubBaseURL == "" {
		return nil, fmt.Errorf("bridge encode: hub_base_url required")
	}
	portrait := dev.Portrait
	syncColorFromHub(hub.color, body.HubBaseURL)

	meta, raw, err := fetchHubImage(ctx, body.HubBaseURL, body.ImageID)
	if err != nil {
		return nil, err
	}
	mergeSendCrops(&meta, body.Crops)
	if err := hub.images.CacheHubImage(body.ImageID, meta, raw); err != nil {
		return nil, err
	}

	if body.OverlayToken != "" {
		cfg, weather, err := bridgeOverlayContext(ctx, hub, body, portrait)
		if err != nil {
			return nil, err
		}
		photoName := overlayPhotoNameFromMeta(meta, cfg)
		return hub.images.ServeBinOrientationOverlay(body.ImageID, portrait, body.OverlayToken, func(img image.Image, flatRGB bool) (image.Image, error) {
			return drawWeatherOverlay(img, cfg, weather, photoName, portrait), nil
		})
	}
	return hub.images.ServeBinOrientation(body.ImageID, portrait)
}

func overlayPhotoNameFromMeta(meta ImageMeta, cfg OverlayConfig) string {
	if !cfg.ShowPhotoName {
		return ""
	}
	return overlayPhotoNameFromFilename(meta.Name)
}

func mergeSendCrops(meta *ImageMeta, crops map[string]protocol.CropRect) {
	if meta == nil || len(crops) == 0 {
		return
	}
	if meta.Crops == nil {
		meta.Crops = make(map[string]CropRect, len(crops))
	}
	for k, c := range crops {
		meta.Crops[k] = CropRect{X: c.X, Y: c.Y, W: c.W, H: c.H}
	}
}

func cropsToProtocol(crops map[string]CropRect) map[string]protocol.CropRect {
	if len(crops) == 0 {
		return nil
	}
	out := make(map[string]protocol.CropRect, len(crops))
	for k, c := range crops {
		out[k] = protocol.CropRect{X: c.X, Y: c.Y, W: c.W, H: c.H}
	}
	return out
}

func buildSendImageBody(h *Hub, imageID, overlayToken, sendID string, overlayCfg *OverlayConfig, overlayWeather *WeatherSnapshot) (protocol.SendImageBody, error) {
	body := protocol.SendImageBody{
		ImageID:      imageID,
		OverlayToken: overlayToken,
		SendID:       sendID,
		HubBaseURL:   hubBaseURL(h.serverAddr),
	}
	if h.images != nil {
		crops, err := h.images.GetCrops(imageID)
		if err != nil {
			return body, err
		}
		body.Crops = cropsToProtocol(crops)
	}
	if overlayToken != "" {
		cfg := overlayCfg
		weather := overlayWeather
		if cfg == nil || weather == nil {
			if h.overlay == nil {
				return body, fmt.Errorf("overlay send requires overlay config")
			}
			if cfg == nil {
				c := h.overlay.Config()
				cfg = &c
			}
			if weather == nil {
				w, err := h.fetchOverlayWeather(context.Background())
				if err != nil {
					return body, err
				}
				weather = &w
			}
		}
		info, err := overlaySendInfo(*cfg, *weather)
		if err != nil {
			return body, err
		}
		body.Overlay = info
	}
	return body, nil
}

func syncOverlayFromHub(store *OverlayStore, hubBase string) error {
	if store == nil {
		return fmt.Errorf("overlay store unavailable")
	}
	base := strings.TrimRight(hubBase, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/overlay", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch overlay: HTTP %d", resp.StatusCode)
	}
	var cfg OverlayConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return err
	}
	return store.Save(cfg)
}

// bridgeEncodeSamsung fetches the hub original and encodes a Samsung PNG locally.
func bridgeEncodeSamsung(
	ctx context.Context,
	hub *Hub,
	body protocol.SendImageBody,
	dev *Device,
) ([]byte, error) {
	if hub == nil || hub.images == nil || hub.samsung == nil {
		return nil, fmt.Errorf("bridge encode: samsung encode hub unavailable")
	}
	if dev == nil {
		return nil, fmt.Errorf("bridge encode: device required")
	}
	if body.HubBaseURL == "" {
		return nil, fmt.Errorf("bridge encode: hub_base_url required")
	}
	syncColorFromHub(hub.color, body.HubBaseURL)

	meta, raw, err := fetchHubImage(ctx, body.HubBaseURL, body.ImageID)
	if err != nil {
		return nil, err
	}
	mergeSendCrops(&meta, body.Crops)
	if err := hub.images.CacheHubImage(body.ImageID, meta, raw); err != nil {
		return nil, err
	}

	frameID := SamsungFrameID(dev)
	profile := hub.samsungDisplayProfile(dev, frameID)
	crop, hasCrop := cropForFormat(meta.Crops, profile.CropFormat)
	img, err := prepareSamsungFrameRGBA(raw, profile, crop, hasCrop, meta.RotateOverride)
	if err != nil {
		return nil, err
	}
	pipe := hub.colorPipelineForImage(body.ImageID)
	if body.OverlayToken != "" {
		cfg, weather, err := bridgeOverlayContext(ctx, hub, body, dev.Portrait)
		if err != nil {
			return nil, err
		}
		photoName := overlayPhotoNameFromMeta(meta, cfg)
		return encodeSamsungPNGWithOverlay(img, func(base image.Image) image.Image {
			return drawWeatherOverlay(base, cfg, weather, photoName, dev.Portrait)
		}, pipe)
	}
	return encodeSamsungPNGFromRGBA(img, pipe)
}

func hubBaseURL(serverAddr string) string {
	addr := strings.TrimSpace(serverAddr)
	if addr == "" {
		return "http://127.0.0.1:8080"
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}
