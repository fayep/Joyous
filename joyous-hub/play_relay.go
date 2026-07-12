package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func playImgID(payload []byte) string {
	var msg struct {
		Data struct {
			Imgs []struct {
				Imgid string `json:"imgid"`
			} `json:"imgs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil || len(msg.Data.Imgs) == 0 {
		return ""
	}
	return strings.TrimSpace(msg.Data.Imgs[0].Imgid)
}

func playCacheName(payload []byte, remoteURL string) string {
	if id := playImgID(payload); id != "" {
		return sanitizeCaptureName(id)
	}
	sum := sha256.Sum256([]byte(remoteURL))
	return hex.EncodeToString(sum[:8])
}

func inkjoyCacheURLPath(mac, name string) string {
	return "/inkjoy/" + inkjoyMACDir(mac) + "/" + name + ".bin"
}

func replacePlayDownloadURL(payload []byte, host string, port int, imgPath string) ([]byte, error) {
	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, err
	}
	data, ok := msg["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("play payload missing data")
	}
	data["host"] = host
	data["port"] = port
	imgs, ok := data["imgs"].([]any)
	if !ok || len(imgs) == 0 {
		return nil, fmt.Errorf("play payload missing imgs")
	}
	img0, ok := imgs[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("play payload imgs[0] invalid")
	}
	img0["imgurl"] = imgPath
	return json.Marshal(msg)
}

func (h *Hub) hubPlayHostPort(mac string) (host string, port int) {
	addr := h.serverAddr
	if addr == "" {
		addr = "localhost:8080"
	}
	hostPart, portStr, _ := net.SplitHostPort(addr)
	port = 8080
	fmt.Sscanf(portStr, "%d", &port)
	host = hostPart
	if dev, ok := h.devices.Get(inkjoyID(mac)); ok && dev.HubIP != "" {
		host = dev.HubIP
	} else if h.hubIP != "" {
		host = h.hubIP
	}
	return host, port
}

func (h *Hub) isLocalPlayURL(rawURL string) bool {
	hostPart := rawURL
	path := "/"
	if i := strings.Index(hostPart, "://"); i >= 0 {
		hostPart = hostPart[i+3:]
	}
	if slash := strings.Index(hostPart, "/"); slash >= 0 {
		path = hostPart[slash:]
		hostPart = hostPart[:slash]
	} else {
		return false
	}
	if strings.HasPrefix(path, "/inkjoy/") {
		return true
	}
	hostPart, _, _ = net.SplitHostPort(hostPart)
	localHosts := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
	}
	if h.hubIP != "" {
		localHosts[h.hubIP] = struct{}{}
	}
	if h.serverAddr != "" {
		if host, _, err := net.SplitHostPort(h.serverAddr); err == nil && host != "" {
			localHosts[host] = struct{}{}
		}
	}
	_, isLocal := localHosts[hostPart]
	return isLocal && strings.HasPrefix(path, "/images/")
}

func downloadPlayBin(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	bin, err := io.ReadAll(io.LimitReader(resp.Body, frameW*frameH*2+1))
	if err != nil {
		return nil, err
	}
	if len(bin) != frameW*frameH*2 {
		return nil, fmt.Errorf("bin size %d != %d", len(bin), frameW*frameH*2)
	}
	return bin, nil
}

func (h *Hub) saveDisplayPreviewFromBin(mac string, bin []byte) error {
	portrait := false
	if dev, ok := h.devices.Get(inkjoyID(mac)); ok {
		portrait = dev.Portrait
	}
	jpeg, err := binToDisplayPreviewJPEG(bin, portrait, h.colorPipeline().InkJoyDisplay)
	if err != nil {
		return err
	}
	if err := h.displayPreview.Save(mac, jpeg); err != nil {
		return err
	}
	h.devices.SetDisplayPreview(mac)
	return nil
}

// rewriteExternalPlay downloads a cloud play image, caches it on the hub data dir,
// and rewrites the play payload to point at the hub HTTP server.
func (h *Hub) rewriteExternalPlay(mac string, payload []byte) ([]byte, error) {
	if h.inkjoy == nil {
		return payload, nil
	}
	remoteURL, ok := parsePlayBinURL(payload)
	if !ok {
		return payload, nil
	}
	if h.isLocalPlayURL(remoteURL) {
		return payload, nil
	}

	bin, err := downloadPlayBin(remoteURL)
	if err != nil {
		return payload, fmt.Errorf("download %s: %w", remoteURL, err)
	}

	name := playCacheName(payload, remoteURL)
	cachePath := h.inkjoy.binPath(mac, name)
	if _, err := os.Stat(cachePath); err != nil {
		if err := h.inkjoy.Save(mac, name, bin); err != nil {
			return payload, fmt.Errorf("cache save: %w", err)
		}
	}

	host, port := h.hubPlayHostPort(mac)
	localPath := inkjoyCacheURLPath(mac, name)
	rewritten, err := replacePlayDownloadURL(payload, host, port, localPath)
	if err != nil {
		return payload, err
	}

	if err := h.saveDisplayPreviewFromBin(mac, bin); err != nil {
		log.Printf("[%s] display preview from relayed play: %v", mac, err)
	}

	log.Printf("[%s] play relayed via %s (from %s)", mac, localPath, remoteURL)
	return rewritten, nil
}
