package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestOTACaptureStoresEachArtifactOnce(t *testing.T) {
	dir := t.TempDir()
	// A minimal ESP-IDF app image header carrying version v9.8.7.
	body := make([]byte, 0x100)
	body[0] = 0xE9
	binary.LittleEndian.PutUint32(body[0x20:], 0xABCD5432)
	copy(body[0x30:], "v9.8.7")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fw.bin" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	push := func(msgid string) []byte {
		b, _ := json.Marshal(map[string]any{"action": "ota", "msgid": msgid,
			"data": map[string]any{"host": host, "port": port, "path": "/fw.bin"}})
		return b
	}

	c := NewOTACapture(dir)
	for _, id := range []string{"1", "1", "2", "3"} { // a repeated msgid and two re-pushes
		c.Handle("AABBCCDDEEFF", "ota", push(id))
	}
	var recs []otaPush
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		recs = nil
		if b, err := os.ReadFile(filepath.Join(dir, otaPushLog)); err == nil {
			for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
				var r otaPush
				if json.Unmarshal(line, &r) == nil {
					recs = append(recs, r)
				}
			}
		}
		if len(recs) == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(recs) != 3 {
		t.Fatalf("want 3 push records (one per msgid), got %d", len(recs))
	}
	bins, _ := filepath.Glob(filepath.Join(dir, "ota_*.bin"))
	if len(bins) != 1 {
		t.Fatalf("want 1 stored artifact, got %v", bins)
	}
	name := filepath.Base(bins[0])
	if !strings.HasPrefix(name, "ota_v9.8.7_") {
		t.Errorf("artifact %q should carry the app version", name)
	}
	newCount := 0
	for _, r := range recs {
		if r.Artifact != name || r.Bytes != int64(len(body)) || r.Error != "" {
			t.Errorf("record %+v", r)
		}
		if r.New {
			newCount++
		}
	}
	if newCount != 1 {
		t.Errorf("exactly one push should be marked new, got %d", newCount)
	}
}
