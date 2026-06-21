package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OTACapture saves OTA/FPGA MQTT payloads and downloads firmware artifacts.
type OTACapture struct {
	dir  string
	seen sync.Map // msgid → struct{}
}

// NewOTACapture returns a capture helper. Empty dir disables downloads (payload still saved if Handle called).
func NewOTACapture(dir string) *OTACapture {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	return &OTACapture{dir: dir}
}

func resolveOTADir(configured, dataDir string) string {
	configured = strings.TrimSpace(configured)
	switch strings.ToLower(configured) {
	case "", "auto":
		return filepath.Join(dataDir, "ota")
	case "off", "none", "disable", "disabled":
		return ""
	default:
		return configured
	}
}

func logOTAReady(dir string) {
	log.Printf("ota: blocked pushes captured → %s", dir)
}

// Handle records the MQTT payload and starts an immediate artifact download.
func (o *OTACapture) Handle(mac, action string, payload []byte) {
	if o == nil {
		return
	}
	if action != "ota" && action != "fpga" {
		return
	}

	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("[ota] %s: bad JSON from %s: %v", mac, action, err)
		return
	}
	if msgid, _ := msg["msgid"].(string); msgid != "" {
		if _, loaded := o.seen.LoadOrStore(msgid, struct{}{}); loaded {
			return
		}
	}

	ts := time.Now().UTC().Format("20060102_150405")
	base := fmt.Sprintf("%s_%s_%s", action, mac, ts)
	if err := o.savePayload(base, payload); err != nil {
		log.Printf("[ota] %s: save payload: %v", mac, err)
	}

	// Start download immediately — URLs may be short-lived.
	go o.download(mac, action, base, msg)
}

func (o *OTACapture) savePayload(base string, payload []byte) error {
	if err := os.MkdirAll(o.dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(o.dir, base+".json"), payload, 0644)
}

func (o *OTACapture) download(mac, action, base string, msg map[string]any) {
	url, ext, err := otaArtifactURL(action, msg)
	if err != nil {
		log.Printf("[ota] %s %s: %v", mac, action, err)
		return
	}
	dest := filepath.Join(o.dir, base+ext)
	log.Printf("[ota] %s %s: fetching %s → %s", mac, action, url, dest)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url) //nolint:gosec // URL from InkJoy broker push we are intercepting
	if err != nil {
		log.Printf("[ota] %s %s: GET failed: %v", mac, action, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[ota] %s %s: server returned %s", mac, action, resp.Status)
		return
	}

	f, err := os.Create(dest)
	if err != nil {
		log.Printf("[ota] %s %s: create failed: %v", mac, action, err)
		return
	}
	n, err := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if err != nil {
		log.Printf("[ota] %s %s: write failed after %d bytes: %v", mac, action, n, err)
		os.Remove(dest)
		return
	}
	if closeErr != nil {
		log.Printf("[ota] %s %s: close failed: %v", mac, action, closeErr)
		return
	}
	log.Printf("[ota] %s %s: saved %d bytes → %s", mac, action, n, dest)
}

func otaArtifactURL(action string, msg map[string]any) (url, ext string, err error) {
	data, ok := msg["data"].(map[string]any)
	if !ok {
		return "", "", fmt.Errorf("missing data field")
	}
	host, _ := data["host"].(string)
	path, _ := data["path"].(string)
	if host == "" || path == "" {
		return "", "", fmt.Errorf("missing host or path")
	}
	port := 8080
	switch v := data["port"].(type) {
	case float64:
		port = int(v)
	case int:
		port = v
	case json.Number:
		if p, perr := v.Int64(); perr == nil {
			port = int(p)
		}
	}
	ext = ".bin"
	if action == "fpga" {
		ext = ".fs"
	}
	return fmt.Sprintf("http://%s:%d%s", host, port, path), ext, nil
}
