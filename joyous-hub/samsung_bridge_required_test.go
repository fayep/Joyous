package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleSamsungWakeRequiresBridge(t *testing.T) {
	h := buildTestHub(t)
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.1.108", "B0F2F657D5CD")
	frameID := "B0F2F657D5CD"
	cfg, _ := h.samsung.LoadConfig(frameID)
	cfg.WifiMAC = "B0:F2:F6:57:D5:CD"
	_ = h.samsung.SaveConfig(cfg)
	h.devices.SetMDCMAC(samsungRegistryID("B0F2F657D5CD"), "B0F2F657D5CD")

	rec := httptest.NewRecorder()
	h.handleSamsungWake(rec, httptest.NewRequest(http.MethodPost, "/api/samsung/"+frameID+"/wake", nil), frameID)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 without bridge; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "samsung bridge offline") {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

func TestHandleSamsungPushRequiresBridge(t *testing.T) {
	h := buildTestHub(t)
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.applySamsungMAC("192.168.1.108", "B0F2F657D5CD")
	frameID := "B0F2F657D5CD"
	if err := h.samsung.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.handleSamsungPush(rec, httptest.NewRequest(http.MethodPost, "/api/samsung/"+frameID+"/push", nil), frameID)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 without bridge; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleDiscoverRequiresBridge(t *testing.T) {
	h := buildTestHub(t)
	rec := httptest.NewRecorder()
	h.handleDiscover(rec, httptest.NewRequest(http.MethodPost, "/api/devices/discover", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
