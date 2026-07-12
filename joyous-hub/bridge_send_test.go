package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"joyous-hub/protocol"
)

func TestBridgeEncodeInkJoyOverlay(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}

	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{90, 120, 180, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}

	imageID := "img-overlay"
	meta := ImageMeta{ID: imageID, Name: "Vacation.jpg"}
	cfg := defaultOverlayConfig()
	cfg.Location = "Portland"
	weather := WeatherSnapshot{
		TempC:       18,
		Condition:   "Clear",
		City:        "Portland",
		DisplayDate: time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 18, Min: 12, Max: 22},
	}
	token := cfg.sendToken(weather, false)
	info, err := overlaySendInfo(cfg, weather)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/images/" + imageID:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(meta)
		case "/images/" + imageID + "/original":
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngBuf.Bytes())
		case "/api/color":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ColorConfig{})
		case "/api/overlay":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cfg)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := NewImageStoreE(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.SetColorStore(NewColorStore(dir))
	encodeHub := &Hub{
		images:  store,
		color:   NewColorStore(dir),
		overlay: NewOverlayStore(dir),
	}

	body := protocol.SendImageBody{
		ImageID:      imageID,
		OverlayToken: token,
		HubBaseURL:   srv.URL,
		Overlay:      info,
	}
	dev := &Device{Portrait: false}

	bin, err := bridgeEncodeInkJoy(context.Background(), encodeHub, body, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(bin) != frameW*frameH*2 {
		t.Fatalf("bin size=%d want %d", len(bin), frameW*frameH*2)
	}

	plainBody := protocol.SendImageBody{
		ImageID:    imageID,
		HubBaseURL: srv.URL,
	}
	plainBin, err := bridgeEncodeInkJoy(context.Background(), encodeHub, plainBody, dev)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(bin, plainBin) {
		t.Fatal("overlay bin should differ from plain encode")
	}
}

func TestBridgeEncodeInkJoyOverlayTokenMismatch(t *testing.T) {
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		TempC:       10,
		Condition:   "Cloudy",
		DisplayDate: time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 10, Min: 8, Max: 12},
	}
	info, err := overlaySendInfo(cfg, weather)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	store, err := NewImageStoreE(dir)
	if err != nil {
		t.Fatal(err)
	}
	encodeHub := &Hub{
		images:  store,
		overlay: NewOverlayStore(dir),
	}
	body := protocol.SendImageBody{
		ImageID:      "img1",
		OverlayToken: "deadbeef00",
		HubBaseURL:   "http://example.com",
		Overlay:      info,
	}
	_, _, err = bridgeOverlayContext(context.Background(), encodeHub, body, false)
	if err == nil || err.Error() != "overlay token mismatch" {
		t.Fatalf("expected token mismatch, got %v", err)
	}
}
