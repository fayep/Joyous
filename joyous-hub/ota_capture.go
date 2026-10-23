package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
// The cloud re-sends the same push every ~10 minutes, so artifacts are stored
// once by content (see otaArtifactName) and every push is one line in
// pushes.jsonl naming the artifact it pointed to.
type OTACapture struct {
	dir  string
	seen sync.Map // msgid → struct{}
	mu   sync.Mutex
}

// otaPushLog is the per-push record file in the capture dir.
const otaPushLog = "pushes.jsonl"

// otaPush is one pushes.jsonl line.
type otaPush struct {
	CapturedAt string          `json:"captured_at"`
	MAC        string          `json:"mac"`
	Action     string          `json:"action"`
	Payload    json.RawMessage `json:"payload"`
	Artifact   string          `json:"artifact,omitempty"`
	SHA256     string          `json:"sha256,omitempty"`
	Bytes      int64           `json:"bytes,omitempty"`
	New        bool            `json:"new,omitempty"` // first push of this artifact
	Error      string          `json:"error,omitempty"`
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
	rec := otaPush{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		MAC:        mac,
		Action:     action,
		Payload:    json.RawMessage(payload),
	}
	// Start download immediately — URLs may be short-lived.
	go func() {
		o.download(mac, action, msg, &rec)
		if err := o.appendPush(rec); err != nil {
			log.Printf("[ota] %s: record push: %v", mac, err)
		}
	}()
}

func (o *OTACapture) appendPush(rec otaPush) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := os.MkdirAll(o.dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(o.dir, otaPushLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (o *OTACapture) download(mac, action string, msg map[string]any, rec *otaPush) {
	fail := func(format string, args ...any) {
		rec.Error = fmt.Sprintf(format, args...)
		log.Printf("[ota] %s %s: %s", mac, action, rec.Error)
	}
	url, ext, err := otaArtifactURL(action, msg)
	if err != nil {
		fail("%v", err)
		return
	}
	if err := os.MkdirAll(o.dir, 0755); err != nil {
		fail("%v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url) //nolint:gosec // URL from InkJoy broker push we are intercepting
	if err != nil {
		fail("GET %s failed: %v", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("GET %s: server returned %s", url, resp.Status)
		return
	}

	tmp, err := os.CreateTemp(o.dir, ".download-*")
	if err != nil {
		fail("create failed: %v", err)
		return
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	h := sha256.New()
	head := &prefixBuffer{max: 256}
	n, err := io.Copy(io.MultiWriter(tmp, h, head), resp.Body)
	closeErr := tmp.Close()
	if err != nil {
		fail("write failed after %d bytes: %v", n, err)
		return
	}
	if closeErr != nil {
		fail("close failed: %v", closeErr)
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	name := otaArtifactName(action, sum, head.b, ext)
	rec.Artifact, rec.SHA256, rec.Bytes = name, sum, n

	o.mu.Lock()
	defer o.mu.Unlock()
	dest := filepath.Join(o.dir, name)
	if _, err := os.Stat(dest); err == nil {
		log.Printf("[ota] %s %s: unchanged, same as %s (%d bytes)", mac, action, name, n)
		return
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		fail("save %s: %v", name, err)
		return
	}
	rec.New = true
	log.Printf("[ota] %s %s: NEW artifact saved %d bytes → %s (from %s)", mac, action, n, dest, url)
}

// prefixBuffer keeps the first max bytes written to it.
type prefixBuffer struct {
	b   []byte
	max int
}

func (p *prefixBuffer) Write(b []byte) (int, error) {
	if room := p.max - len(p.b); room > 0 {
		p.b = append(p.b, b[:min(room, len(b))]...)
	}
	return len(b), nil
}

// otaArtifactName names a downloaded artifact by content, so repeat pushes
// of the same image map to one file: <action>[_<version>]_<sha256[:12]><ext>.
// An ESP-IDF app image's version comes from its esp_app_desc_t (magic
// 0xABCD5432 at offset 0x20, version string at 0x30).
func otaArtifactName(action, sha256Hex string, head []byte, ext string) string {
	name := action
	if v := espAppVersion(head); v != "" {
		name += "_" + v
	}
	return name + "_" + sha256Hex[:12] + ext
}

func espAppVersion(head []byte) string {
	if len(head) < 0x50 || head[0] != 0xE9 || binary.LittleEndian.Uint32(head[0x20:]) != 0xABCD5432 {
		return ""
	}
	v := head[0x30:0x50]
	if i := bytes.IndexByte(v, 0); i >= 0 {
		v = v[:i]
	}
	return artifactNamePart(string(v))
}

// artifactNamePart keeps [A-Za-z0-9._-] of s (other runes become '_'), or ""
// for an empty s, for building artifact file names from pushed metadata.
func artifactNamePart(s string) string {
	s = strings.TrimSpace(s)
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
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
