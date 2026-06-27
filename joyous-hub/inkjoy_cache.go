package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// InkJoyCache stores .bin images relayed from cloud play commands for local serving.
type InkJoyCache struct {
	dir string
}

func NewInkJoyCache(dataDir string) *InkJoyCache {
	return &InkJoyCache{dir: filepath.Join(dataDir, "inkjoy")}
}

func inkjoyMACDir(mac string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(mac), ":", ""), "-", "")
}

func (c *InkJoyCache) binPath(mac, name string) string {
	return filepath.Join(c.dir, inkjoyMACDir(mac), name+".bin")
}

func (c *InkJoyCache) Save(mac, name string, bin []byte) error {
	if err := os.MkdirAll(filepath.Dir(c.binPath(mac, name)), 0755); err != nil {
		return err
	}
	return os.WriteFile(c.binPath(mac, name), bin, 0644)
}

func (c *InkJoyCache) ServeHTTP(w http.ResponseWriter, r *http.Request, mac, name string) {
	path := c.binPath(mac, name)
	info, err := os.Stat(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
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
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}
