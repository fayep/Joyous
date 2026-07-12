package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// isInkJoyFrameBinProxyPath reports paths like D0CF13EF4080/foo-p.bin that must be
// served from the hub disk cache, never tunneled to the bridge over MQTT.
func isInkJoyFrameBinProxyPath(subPath string) bool {
	parts := strings.Split(strings.Trim(subPath, "/"), "/")
	if len(parts) != 2 {
		return false
	}
	mac, file := parts[0], parts[1]
	if !looksLikeInkJoyMAC(mac) {
		return false
	}
	return strings.HasSuffix(strings.ToLower(file), ".bin")
}

func looksLikeInkJoyMAC(s string) bool {
	if strings.EqualFold(s, "api") {
		return false
	}
	if strings.Count(s, ":") == 5 {
		return true
	}
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

func inkjoyAlbumCacheName(imageID, overlayToken string, portrait bool) string {
	return strings.TrimSuffix(imageBinFilename(imageID, overlayToken, portrait), ".bin")
}

func inkjoyPlayURL(hubBaseURL, frameIP, mac, cacheName, preferHost string) string {
	hostPort := inkjoyPlayContentHost(hubBaseURL, frameIP, preferHost)
	return "http://" + hostPort + inkjoyCacheURLPath(mac, cacheName)
}

// inkjoyPlayContentHost picks host:port for frame .bin downloads on the hub HTTP server.
// preferHost is usually the hub/bridge LAN IP the frame already uses for MQTT (dev.HubIP).
func inkjoyPlayContentHost(hubBaseURL, frameIP, preferHost string) string {
	hostPort := hubHTTPHostPort(hubBaseURL)
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil || port == "" {
		host = hostPort
		port = "8080"
	}
	if preferHost != "" {
		return net.JoinHostPort(preferHost, port)
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return net.JoinHostPort(ip.String(), port)
	}
	if lan := resolvedLANIP(hostPort); lan != "" && lan != host {
		return net.JoinHostPort(lan, port)
	}
	return publicHTTPHost(hostPort, frameIP)
}

func hubHTTPHostPort(hubBaseURL string) string {
	addr := strings.TrimSpace(hubBaseURL)
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimRight(addr, "/")
	if addr == "" {
		return "127.0.0.1:8080"
	}
	return addr
}

const inkjoyCacheResponseHeader = "X-Joyous-Inkjoy-Cache"

func setInkjoyCacheResponseHeaders(w http.ResponseWriter) {
	w.Header().Set(inkjoyCacheResponseHeader, "1")
}

func registerInkJoyCacheRoutes(mux *http.ServeMux, cache *InkJoyCache) {
	if mux == nil || cache == nil {
		return
	}
	serve := func(w http.ResponseWriter, r *http.Request) {
		if !looksLikeInkJoyMAC(r.PathValue("mac")) {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimSuffix(r.PathValue("file"), ".bin")
		cache.ServeHTTP(w, r, r.PathValue("mac"), name)
	}
	mux.HandleFunc("GET /inkjoy/{mac}/{file}", serve)
	mux.HandleFunc("HEAD /inkjoy/{mac}/{file}", serve)
}

// FilePath returns the on-disk path for a cached .bin (for logging/diagnostics).
func (c *InkJoyCache) FilePath(mac, name string) string {
	return c.binPath(mac, name)
}

// ProbeHubURL checks that the hub can serve a cached .bin before the frame downloads it.
func ProbeHubURL(ctx context.Context, url string, wantBytes int64) error {
	return probeHubURLWithHeader(ctx, url, wantBytes, inkjoyCacheResponseHeader)
}

func probeHubURLWithHeader(ctx context.Context, url string, wantBytes int64, cacheHeader string) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
		}
		if err := probeHubURLOnce(ctx, url, wantBytes, cacheHeader); err != nil {
			last = err
			continue
		}
		return nil
	}
	return last
}

func probeHubURLOnce(ctx context.Context, url string, wantBytes int64, cacheHeader string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.Header.Get(cacheHeader) == "" {
		if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("HTTP 502 (request hit bridge MQTT proxy — upgrade joyous-hub)")
		}
		return fmt.Errorf("HTTP %d without %s (hub cache route inactive?)", resp.StatusCode, cacheHeader)
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("HTTP 404 (file not visible to hub yet)")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" && wantBytes > 0 {
		n, err := strconv.ParseInt(cl, 10, 64)
		if err == nil && n != wantBytes {
			return fmt.Errorf("Content-Length %d want %d", n, wantBytes)
		}
	}
	return nil
}

// VerifyInkjoyCacheServing checks the hub inkjoy cache HTTP handler is registered.
// A cache miss (404 + X-Joyous-Inkjoy-Cache) is success; 502 means the bridge proxy owns the route.
func VerifyInkjoyCacheServing(ctx context.Context, hubBaseURL string) error {
	base := strings.TrimRight(strings.TrimSpace(hubBaseURL), "/")
	if base == "" {
		return fmt.Errorf("hub base URL required")
	}
	url := base + "/inkjoy/000000000000/__startup_probe__.bin"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.Header.Get(inkjoyCacheResponseHeader) != "" {
		return nil
	}
	if resp.StatusCode == http.StatusBadGateway {
		return fmt.Errorf("GET /inkjoy/{{mac}}/{{file}} is not served from hub disk (bridge MQTT proxy?)")
	}
	return fmt.Errorf("inkjoy cache handler not active (HTTP %d)", resp.StatusCode)
}

func (c *InkJoyCache) binPath(mac, name string) string {
	return filepath.Join(c.dir, inkjoyMACDir(mac), name+".bin")
}

// Save writes bin atomically: to a temp file in the same directory, fsynced,
// then renamed over the final path. This means a concurrent ServeHTTP reader
// that already has the old file open (see below) keeps seeing the complete
// old inode's bytes — never a half-written or truncated file — and any write
// failure leaves the previous file (if any) untouched instead of a corrupt one.
func (c *InkJoyCache) Save(mac, name string, bin []byte) error {
	path := c.binPath(mac, name)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+name+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, werr := tmp.Write(bin)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if werr != nil || serr != nil || cerr != nil {
		os.Remove(tmpPath)
		if werr != nil {
			return werr
		}
		if serr != nil {
			return serr
		}
		return cerr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func (c *InkJoyCache) ServeHTTP(w http.ResponseWriter, r *http.Request, mac, name string) {
	setInkjoyCacheResponseHeaders(w)
	path := c.binPath(mac, name)
	// Open once and derive size/etag from the same file descriptor used to
	// read the body: a concurrent Save() rename swaps the directory entry to
	// a new inode but never touches bytes already open here, so Content-Length
	// (and everything else) always matches what's actually written below —
	// unlike a separate Stat+ReadFile pair, which can straddle a rename.
	f, err := os.Open(path)
	if err != nil {
		log.Printf("inkjoy cache miss mac=%s name=%s path=%s", inkjoyMACDir(mac), name, path)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	etag := fmt.Sprintf("%x-%d", info.ModTime().UnixNano(), info.Size())
	if cacheNotModified(r, etag, info.ModTime()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if r.Method == http.MethodHead {
		log.Printf("inkjoy cache head mac=%s name=%s bytes=%d", inkjoyMACDir(mac), name, info.Size())
		w.WriteHeader(http.StatusOK)
		return
	}
	log.Printf("inkjoy cache hit mac=%s name=%s bytes=%d", inkjoyMACDir(mac), name, info.Size())

	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Write(data)
}
