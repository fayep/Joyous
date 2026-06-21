package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DisplayPreviewStore caches JPEG previews of externally played .bin images per frame MAC.
type DisplayPreviewStore struct {
	dir string
}

func NewDisplayPreviewStore(dataDir string) *DisplayPreviewStore {
	return &DisplayPreviewStore{dir: filepath.Join(dataDir, "display")}
}

func (s *DisplayPreviewStore) jpegPath(mac string) string {
	return filepath.Join(s.dir, mac+".jpg")
}

// RestoreFromDisk sets display_preview_at on devices that already have a cached JPEG.
func (s *DisplayPreviewStore) RestoreFromDisk(devices *DeviceRegistry) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jpg") {
			continue
		}
		mac := strings.TrimSuffix(e.Name(), ".jpg")
		info, err := e.Info()
		if err != nil {
			continue
		}
		devices.SetDisplayPreviewAt(mac, info.ModTime())
	}
}

func (s *DisplayPreviewStore) Clear(mac string) {
	if s == nil {
		return
	}
	os.Remove(s.jpegPath(mac))
}

func (s *DisplayPreviewStore) Save(mac string, jpeg []byte) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.jpegPath(mac), jpeg, 0644)
}

func (s *DisplayPreviewStore) ServeHTTP(w http.ResponseWriter, r *http.Request, mac string) {
	path := s.jpegPath(mac)
	info, err := os.Stat(path)
	if err != nil {
		http.Error(w, "no preview", http.StatusNotFound)
		return
	}
	etag := fmt.Sprintf("%x-%d", info.ModTime().UnixNano(), info.Size())
	if cacheNotModified(r, etag, info.ModTime()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

// parsePlayBinURL reconstructs the HTTP(S) URL a frame uses to fetch the play .bin.
func parsePlayBinURL(payload []byte) (string, bool) {
	var msg struct {
		Data struct {
			Host string `json:"host"`
			Port int    `json:"port"`
			Imgs []struct {
				Imgurl string `json:"imgurl"`
			} `json:"imgs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return "", false
	}
	if len(msg.Data.Imgs) == 0 {
		return "", false
	}
	imgurl := msg.Data.Imgs[0].Imgurl
	if imgurl == "" {
		return "", false
	}
	if strings.Contains(imgurl, "://") {
		return imgurl, true
	}
	host := msg.Data.Host
	port := msg.Data.Port
	if port == 0 {
		port = 80
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		if port == 80 {
			fmt.Sscanf(p, "%d", &port)
		}
	}
	scheme := "http"
	if port == 443 {
		scheme = "https"
	}
	if !strings.HasPrefix(imgurl, "/") {
		imgurl = "/" + imgurl
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, host, port, imgurl), true
}

func (h *Hub) handleExternalPlay(mac string, payload []byte) {
	url, ok := parsePlayBinURL(payload)
	if !ok {
		return
	}
	go h.fetchDisplayPreview(mac, url)
}

func (h *Hub) fetchDisplayPreview(mac, binURL string) {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(binURL)
	if err != nil {
		log.Printf("[%s] display preview fetch %s: %v", mac, binURL, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[%s] display preview fetch %s: HTTP %d", mac, binURL, resp.StatusCode)
		return
	}
	bin, err := io.ReadAll(io.LimitReader(resp.Body, frameW*frameH*2+1))
	if err != nil {
		log.Printf("[%s] display preview read: %v", mac, err)
		return
	}
	portrait := false
	if dev, ok := h.devices.Get(inkjoyID(mac)); ok {
		portrait = dev.Portrait
	}
	jpeg, err := binToDisplayPreviewJPEG(bin, portrait)
	if err != nil {
		log.Printf("[%s] display preview decode (portrait=%v): %v", mac, portrait, err)
		return
	}
	if err := h.displayPreview.Save(mac, jpeg); err != nil {
		log.Printf("[%s] display preview save: %v", mac, err)
		return
	}
	h.devices.SetDisplayPreview(mac)
	log.Printf("[%s] display preview updated from %s (portrait=%v)", mac, binURL, portrait)
}

func (h *Hub) handleDeviceDisplayPreview(w http.ResponseWriter, r *http.Request, deviceID string) {
	dev, ok := h.devices.Get(deviceID)
	if !ok || dev.Type != DeviceTypeInkJoy {
		http.Error(w, "inkjoy device required", http.StatusNotFound)
		return
	}
	h.displayPreview.ServeHTTP(w, r, dev.MAC)
}
