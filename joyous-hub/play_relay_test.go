package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplacePlayDownloadURL(t *testing.T) {
	orig := []byte(`{"action":"play","msgid":"abc123","stamac":"AA:BB:CC:DD:EE:FF","data":{"host":"cdn.example.com","port":443,"imgs":[{"imgid":"cloud-img","imgurl":"/88/foo.bin"}],"mode":2,"strategy":1}}`)
	rewritten, err := replacePlayDownloadURL(orig, "192.168.1.5", 8080, "/inkjoy/AABBCCDDEEFF/cloud-img.bin")
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(rewritten, &msg); err != nil {
		t.Fatal(err)
	}
	if msg["msgid"] != "abc123" {
		t.Fatalf("msgid=%v", msg["msgid"])
	}
	data := msg["data"].(map[string]any)
	if data["host"] != "192.168.1.5" || data["port"].(float64) != 8080 {
		t.Fatalf("host/port=%v %v", data["host"], data["port"])
	}
	imgs := data["imgs"].([]any)
	img0 := imgs[0].(map[string]any)
	if img0["imgurl"] != "/inkjoy/AABBCCDDEEFF/cloud-img.bin" || img0["imgid"] != "cloud-img" {
		t.Fatalf("img=%v", img0)
	}
}

func TestIsLocalPlayURL(t *testing.T) {
	h := &Hub{hubIP: "192.168.1.5", serverAddr: "192.168.1.5:8080"}
	if !h.isLocalPlayURL("http://192.168.1.5:8080/images/abc.bin") {
		t.Fatal("expected hub images URL to be local")
	}
	if !h.isLocalPlayURL("http://192.168.1.5:8080/inkjoy/AABBCCDDEEFF/foo.bin") {
		t.Fatal("expected inkjoy cache URL to be local")
	}
	if h.isLocalPlayURL("https://ink-ufile.s3.eu-west-3.amazonaws.com:443/88/foo.bin") {
		t.Fatal("expected remote S3 URL to be non-local")
	}
}

func TestRewriteExternalPlay(t *testing.T) {
	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x02
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bin)
	}))
	defer srv.Close()

	dir := t.TempDir()
	mac := "AA:BB:CC:DD:EE:FF"
	devices := NewDeviceRegistry(dir)
	devices.getOrCreateInkJoy(mac)

	h := &Hub{
		devices:        devices,
		displayPreview: NewDisplayPreviewStore(dir),
		inkjoy:         NewInkJoyCache(dir),
		hubIP:          "192.168.1.7",
		serverAddr:     "192.168.1.7:8080",
	}

	rest := strings.TrimPrefix(srv.URL, "http://")
	remoteHost, remotePortStr, remotePath := rest, "80", "/"
	if i := strings.Index(rest, "/"); i >= 0 {
		remotePath = rest[i:]
		rest = rest[:i]
	}
	if hostPart, portPart, err := net.SplitHostPort(rest); err == nil {
		remoteHost, remotePortStr = hostPart, portPart
	} else {
		remoteHost = rest
	}
	remotePort := 80
	fmt.Sscanf(remotePortStr, "%d", &remotePort)

	payload, _ := json.Marshal(map[string]any{
		"action": "play",
		"msgid":  "remote-play-1",
		"stamac": mac,
		"data": map[string]any{
			"host": remoteHost,
			"port": remotePort,
			"imgs": []any{map[string]any{"imgid": "album-42", "imgurl": remotePath}},
			"mode": 2, "strategy": 1,
		},
	})

	rewritten, err := h.rewriteExternalPlay(mac, payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(rewritten) == string(payload) {
		t.Fatal("expected rewritten payload")
	}
	localURL, ok := parsePlayBinURL(rewritten)
	if !ok {
		t.Fatal("expected local play URL")
	}
	if !strings.Contains(localURL, "/inkjoy/AABBCCDDEEFF/album-42.bin") {
		t.Fatalf("local url=%q", localURL)
	}
	cachePath := filepath.Join(dir, "inkjoy", "AABBCCDDEEFF", "album-42.bin")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cached bin missing: %v", err)
	}
	dev, _ := devices.Get(inkjoyID(mac))
	if dev.DisplayPreviewAt.IsZero() {
		t.Fatal("expected display preview updated")
	}
}
