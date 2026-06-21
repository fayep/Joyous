package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveOTADir(t *testing.T) {
	if got := resolveOTADir("", "/data"); got != filepath.Join("/data", "ota") {
		t.Fatalf("auto: got %q", got)
	}
	if got := resolveOTADir("off", "/data"); got != "" {
		t.Fatalf("off: got %q", got)
	}
}

func TestOTAArtifactURL(t *testing.T) {
	msg := map[string]any{
		"data": map[string]any{
			"host": "13.39.148.101",
			"port": float64(8080),
			"path": "/inkjoy/ota/AABBCCDDEEFF",
		},
	}
	url, ext, err := otaArtifactURL("ota", msg)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://13.39.148.101:8080/inkjoy/ota/AABBCCDDEEFF" || ext != ".bin" {
		t.Fatalf("got %q ext=%q", url, ext)
	}
	_, ext, err = otaArtifactURL("fpga", msg)
	if err != nil || ext != ".fs" {
		t.Fatalf("fpga ext: %q err=%v", ext, err)
	}
}

func TestOTACaptureHandle(t *testing.T) {
	dir := t.TempDir()
	body := []byte("FIRMWARE_BYTES")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fw.bin" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	payload, _ := json.Marshal(map[string]any{
		"action": "ota",
		"msgid":  "12345",
		"data": map[string]any{
			"host": host,
			"port": port,
			"path": "/fw.bin",
		},
	})

	c := NewOTACapture(dir)
	c.Handle("AABBCCDDEEFF", "ota", payload)
	c.Handle("AABBCCDDEEFF", "ota", payload)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dir, "ota_*.bin"))
		if len(matches) == 1 {
			data, err := os.ReadFile(matches[0])
			if err == nil && string(data) == string(body) {
				jsonPath := matches[0][:len(matches[0])-4] + ".json"
				if _, err := os.Stat(jsonPath); err != nil {
					t.Fatalf("missing payload json: %v", err)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected downloaded firmware file")
}

func TestOTACaptureDedupMsgid(t *testing.T) {
	dir := t.TempDir()
	c := NewOTACapture(dir)
	payload := []byte(`{"action":"ota","msgid":"same","data":{"host":"x","port":8080,"path":"/a"}}`)
	c.Handle("MAC", "ota", payload)
	c.Handle("MAC", "ota", payload)
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 1 {
		t.Fatalf("expected 1 payload file, got %d", len(files))
	}
}
