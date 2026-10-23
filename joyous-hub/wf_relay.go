package main

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Waveform relay: the cloud's wf_update names a panel waveform file
// (/spiffs/epd.wf on the frame) by URL. Like a cloud play, the bridge
// downloads it, keeps a copy in the hub's InkJoy cache and points the frame
// at the hub instead. The frame's downloader (ij_wf_update in ij_epd) takes
// the URL as is: http, or https verified against ESP-IDF's public CA bundle,
// so a hub HTTPS URL needs a publicly trusted certificate.

const (
	wfCacheExt = ".wf"
	// wfMaxSize is the firmware's limit on wf_file_size (0x80000).
	wfMaxSize = 512 << 10
)

type wfUpdateInfo struct {
	URL                       string
	Panel, Batch, Date, Scene string
	CRC                       uint32
	HasCRC                    bool
	Size                      int64
}

func parseWFUpdate(payload []byte) (wfUpdateInfo, error) {
	var msg struct {
		Data struct {
			URL      string          `json:"url"`
			Panel    string          `json:"wf_panel"`
			Batch    string          `json:"wf_batch"`
			Date     string          `json:"wf_date"`
			Scene    string          `json:"wf_scene"`
			CRC      json.RawMessage `json:"wf_crc32"`
			FileSize json.Number     `json:"wf_file_size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return wfUpdateInfo{}, err
	}
	d := msg.Data
	info := wfUpdateInfo{URL: strings.TrimSpace(d.URL), Panel: d.Panel, Batch: d.Batch, Date: d.Date, Scene: d.Scene}
	if n, err := d.FileSize.Int64(); err == nil {
		info.Size = n
	}
	// The firmware takes wf_crc32 as a number or as a hex string.
	if len(d.CRC) > 0 {
		var num uint32
		var str string
		if json.Unmarshal(d.CRC, &num) == nil {
			info.CRC, info.HasCRC = num, true
		} else if json.Unmarshal(d.CRC, &str) == nil && str != "" {
			if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(str), "0x"), 16, 32); err == nil {
				info.CRC, info.HasCRC = uint32(v), true
			}
		}
	}
	return info, nil
}

// wfCacheName names a waveform by what it is, so copies are kept per version.
func wfCacheName(info wfUpdateInfo, crc uint32) string {
	parts := []string{"wf"}
	for _, p := range []string{info.Panel, info.Batch, info.Date, info.Scene} {
		if p = artifactNamePart(p); p != "" {
			parts = append(parts, p)
		}
	}
	parts = append(parts, fmt.Sprintf("%08X", crc))
	return strings.Join(parts, "-")
}

func downloadWF(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, wfMaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > wfMaxSize {
		return nil, fmt.Errorf("waveform larger than %d bytes", wfMaxSize)
	}
	return b, nil
}

// wfServeURL is the full URL the frame fetches a cached waveform from.
func (h *Hub) wfServeURL(mac, name string) string {
	path := "/inkjoy/" + inkjoyMACDir(mac) + "/" + name + wfCacheExt
	if base := strings.TrimRight(h.publicHTTPSURL, "/"); base != "" {
		return base + path
	}
	host, port := h.hubPlayHostPort(mac)
	return fmt.Sprintf("http://%s:%d%s", host, port, path)
}

func (h *Hub) isLocalWFURL(rawURL string) bool {
	return strings.Contains(rawURL, "/inkjoy/") && strings.HasSuffix(rawURL, wfCacheExt)
}

// rewriteWFUpdate downloads a cloud wf_update's waveform, checks it against
// the push's size and CRC, caches it, and returns the payload with data.url
// pointing at the hub copy. On any error the caller forwards the original.
func (h *Hub) rewriteWFUpdate(mac string, payload []byte) ([]byte, error) {
	if h.inkjoy == nil {
		return payload, nil
	}
	info, err := parseWFUpdate(payload)
	if err != nil {
		return payload, err
	}
	if info.URL == "" || h.isLocalWFURL(info.URL) {
		return payload, nil
	}
	data, err := downloadWF(info.URL)
	if err != nil {
		return payload, fmt.Errorf("download %s: %w", info.URL, err)
	}
	if info.Size > 0 && int64(len(data)) != info.Size {
		return payload, fmt.Errorf("download %s: %d bytes, push says %d", info.URL, len(data), info.Size)
	}
	crc := crc32.ChecksumIEEE(data)
	if info.HasCRC && crc != info.CRC {
		// The firmware's CRC variant is unconfirmed; keep the file either way
		// so it can be compared, and let the frame be the judge.
		log.Printf("[%s] wf_update: CRC-32 %08X != pushed %08X (variant may differ)", mac, crc, info.CRC)
	}
	name := wfCacheName(info, crc)
	if err := h.inkjoy.SaveExt(mac, name, wfCacheExt, data); err != nil {
		return payload, fmt.Errorf("cache save: %w", err)
	}

	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		return payload, err
	}
	d, ok := msg["data"].(map[string]any)
	if !ok {
		return payload, fmt.Errorf("wf_update payload missing data")
	}
	local := h.wfServeURL(mac, name)
	d["url"] = local
	out, err := json.Marshal(msg)
	if err != nil {
		return payload, err
	}
	log.Printf("[%s] wf_update relayed via %s (%d bytes, from %s)", mac, local, len(data), info.URL)
	return out, nil
}
