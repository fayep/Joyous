// inkjoy-proxy — Transparent MQTT MITM proxy for the InkJoy frame.
//
// Runs on the EdgeRouter X (MIPS32LE). iptables redirects the frame's TCP
// connection to port 1883 here; we relay all packets to the real broker while
// decoding every MQTT message in both directions.
//
// In --replace-bin mode, any broker→frame PUBLISH on the frame's inkjoyap
// topic has its binUri/imgUri fields swapped to point at a local HTTP server,
// allowing fully local image delivery without server involvement.
//
// Build for EdgeRouter X (Mediatek MT7621, MIPS32LE, no FPU):
//   GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o inkjoy-proxy ./inkjoy-proxy/
//
// Deploy:
//   scp inkjoy-proxy ubnt@192.168.1.1:/tmp/
//   ssh ubnt@192.168.1.1 "sudo iptables -t nat -A PREROUTING -i eth1 \
//       -p tcp --dport 1883 -j REDIRECT --to-port 18830"
//   ssh ubnt@192.168.1.1 "sudo /tmp/inkjoy-proxy [flags]"
//
// Flags:
//   --broker   upstream broker addr (default 13.39.148.101:1883)
//   --listen   local listen addr   (default :18830)
//   --replace-bin  URL to substitute for binUri/imgUri in play messages
//   --inject-play  path to JSON file to inject as a play message immediately

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	broker       = flag.String("broker", "13.39.148.101:1883", "upstream MQTT broker")
	listenAddr   = flag.String("listen", ":18830", "local listen address (all interfaces)")
	controlAddr  = flag.String("control", "192.168.1.1:18831", "control/log port (LAN only — not exposed to WAN)")
	replaceBin   = flag.String("replace-bin", "", "replace download URL in intercepted play messages")
	injectPlay   = flag.String("inject-play", "", "JSON file to inject as a play message immediately on connect")
	captureDir   = flag.String("capture-dir", "/tmp/ij_capture", "dir to append unknown broker→frame messages; empty = disabled")
	otaDir       = flag.String("ota-dir", "/tmp/ij-ota", "dir to save auto-downloaded OTA artifacts (ota, fpga_ota); empty = disabled")
	allowActions = flag.String("allow-actions", "play,heart,heart_ack,login_ack,shutdown_ack,image_refresh_ack", "comma-separated broker→frame actions to pass through")
	blockUnknown = flag.Bool("block-unknown", false, "drop unknown broker→frame actions (default: capture but forward)")
)

// allowedActionSet is populated from --allow-actions at startup.
var allowedActionSet map[string]bool

// ── Log fan-out ───────────────────────────────────────────────────────────────
// All log output goes to stderr AND to every connected control client.

type fanWriter struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

var fan = &fanWriter{clients: map[chan []byte]struct{}{}}

func (f *fanWriter) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	f.mu.Lock()
	for ch := range f.clients {
		select {
		case ch <- cp:
		default: // slow client — drop line rather than block
		}
	}
	f.mu.Unlock()
	return os.Stderr.Write(p)
}

func (f *fanWriter) subscribe() chan []byte {
	ch := make(chan []byte, 256)
	f.mu.Lock()
	f.clients[ch] = struct{}{}
	f.mu.Unlock()
	return ch
}

func (f *fanWriter) unsubscribe(ch chan []byte) {
	f.mu.Lock()
	delete(f.clients, ch)
	f.mu.Unlock()
}

// ── Pending inject queue ──────────────────────────────────────────────────────
// The frame polls every ~20s: connect → subscribe → disconnect.
// We pre-load payloads here; they fire on the next SUBACK regardless of
// whether a frame session is currently active.

var (
	pendingInject = make(chan []byte, 8) // survives across sessions

	// injectedMsgIDs tracks msgids we injected so we can suppress the
	// corresponding play_ack before it reaches the real broker.
	injectedMu     sync.Mutex
	injectedMsgIDs = map[string]bool{}
)

// ── MQTT packet types ────────────────────────────────────────────────────────

const (
	pktCONNECT    = 1
	pktCONNACK    = 2
	pktPUBLISH    = 3
	pktPUBACK     = 4
	pktPUBREC     = 5
	pktPUBREL     = 6
	pktPUBCOMP    = 7
	pktSUBSCRIBE  = 8
	pktSUBACK     = 9
	pktUNSUBSCRIBE = 10
	pktUNSUBACK   = 11
	pktPINGREQ    = 12
	pktPINGRESP   = 13
	pktDISCONNECT = 14
)

var pktNames = map[byte]string{
	pktCONNECT: "CONNECT", pktCONNACK: "CONNACK",
	pktPUBLISH: "PUBLISH", pktPUBACK: "PUBACK",
	pktPUBREC: "PUBREC", pktPUBREL: "PUBREL", pktPUBCOMP: "PUBCOMP",
	pktSUBSCRIBE: "SUBSCRIBE", pktSUBACK: "SUBACK",
	pktUNSUBSCRIBE: "UNSUBSCRIBE", pktUNSUBACK: "UNSUBACK",
	pktPINGREQ: "PINGREQ", pktPINGRESP: "PINGRESP",
	pktDISCONNECT: "DISCONNECT",
}

// ── MQTT framing ─────────────────────────────────────────────────────────────

// readPacket reads one complete MQTT packet from r.
// Returns (fixedHeader byte, payload bytes, error).
func readPacket(r io.Reader) (byte, []byte, error) {
	// Fixed header: first byte
	var hdr [1]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}

	// Remaining length: variable-length encoding (1–4 bytes)
	remLen, err := readVarInt(r)
	if err != nil {
		return 0, nil, fmt.Errorf("remaining length: %w", err)
	}

	// Payload
	payload := make([]byte, remLen)
	if remLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, fmt.Errorf("payload read: %w", err)
		}
	}
	return hdr[0], payload, nil
}

func readVarInt(r io.Reader) (int, error) {
	var (
		result    int
		shift     uint
		b         [1]byte
	)
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		result |= int(b[0]&0x7f) << shift
		if b[0]&0x80 == 0 {
			return result, nil
		}
		shift += 7
		if shift > 21 {
			return 0, fmt.Errorf("remaining length overflow")
		}
	}
}

func encodeVarInt(v int) []byte {
	var buf []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v > 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			break
		}
	}
	return buf
}

// writePacket reassembles and writes an MQTT packet.
func writePacket(w io.Writer, hdr byte, payload []byte) error {
	frame := make([]byte, 0, 1+4+len(payload))
	frame = append(frame, hdr)
	frame = append(frame, encodeVarInt(len(payload))...)
	frame = append(frame, payload...)
	_, err := w.Write(frame)
	return err
}

// ── PUBLISH parsing ──────────────────────────────────────────────────────────

type publishPkt struct {
	hdr     byte
	topic   string
	pktID   uint16 // 0 if QoS 0
	payload []byte
}

func parsePublish(hdr byte, raw []byte) (*publishPkt, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("PUBLISH too short")
	}
	qos := (hdr >> 1) & 0x03

	topicLen := int(binary.BigEndian.Uint16(raw[0:2]))
	if len(raw) < 2+topicLen {
		return nil, fmt.Errorf("PUBLISH topic truncated")
	}
	topic := string(raw[2 : 2+topicLen])
	off := 2 + topicLen

	var pktID uint16
	if qos > 0 {
		if len(raw) < off+2 {
			return nil, fmt.Errorf("PUBLISH pktID missing")
		}
		pktID = binary.BigEndian.Uint16(raw[off : off+2])
		off += 2
	}

	return &publishPkt{
		hdr:     hdr,
		topic:   topic,
		pktID:   pktID,
		payload: raw[off:],
	}, nil
}

func (p *publishPkt) encode() []byte {
	qos := (p.hdr >> 1) & 0x03
	topicBytes := []byte(p.topic)
	size := 2 + len(topicBytes)
	if qos > 0 {
		size += 2
	}
	size += len(p.payload)

	buf := make([]byte, 0, size)
	buf = append(buf, byte(len(topicBytes)>>8), byte(len(topicBytes)))
	buf = append(buf, topicBytes...)
	if qos > 0 {
		buf = append(buf, byte(p.pktID>>8), byte(p.pktID))
	}
	buf = append(buf, p.payload...)
	return buf
}

// ── Message processing ───────────────────────────────────────────────────────

// captureUnknown appends a raw JSON payload to {captureDir}/{action}.jsonl.
func captureUnknown(dir, action string, payload []byte) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, action)
	f, err := os.OpenFile(filepath.Join(dir, safe+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(payload, '\n'))
	return err
}

// downloadOTAArtifact fetches the artifact described in an ota/fpga_ota message
// and writes it to otaDir. Runs in a goroutine — does not block the relay.
func downloadOTAArtifact(action string, m map[string]any) {
	dir := *otaDir
	if dir == "" {
		return
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		log.Printf("[ota-dl] %s: no data field", action)
		return
	}
	host, _ := data["host"].(string)
	path, _ := data["path"].(string)
	if host == "" || path == "" {
		log.Printf("[ota-dl] %s: missing host or path in data", action)
		return
	}
	port := 8080
	switch v := data["port"].(type) {
	case float64:
		port = int(v)
	case int:
		port = v
	}

	url := fmt.Sprintf("http://%s:%d%s", host, port, path)
	ext := ".bin"
	if action == "fpga" {
		ext = ".fs"
	}
	ts := time.Now().Format("20060102_150405")
	stamac, _ := m["stamac"].(string)
	macClean := strings.NewReplacer(":", "", "-", "").Replace(stamac)
	if macClean == "" {
		macClean = "unknown"
	}
	filename := fmt.Sprintf("%s_%s_%s%s", action, macClean, ts, ext)
	destPath := filepath.Join(dir, filename)

	log.Printf("[ota-dl] %s: fetching %s → %s", action, url, destPath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[ota-dl] %s: mkdir failed: %v", action, err)
		return
	}

	resp, err := http.Get(url) //nolint:gosec // URL comes from the broker we're already trusting
	if err != nil {
		log.Printf("[ota-dl] %s: GET failed: %v", action, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[ota-dl] %s: server returned %s", action, resp.Status)
		return
	}

	f, err := os.Create(destPath)
	if err != nil {
		log.Printf("[ota-dl] %s: create failed: %v", action, err)
		return
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		log.Printf("[ota-dl] %s: write failed after %d bytes: %v", action, n, err)
		os.Remove(destPath)
		return
	}
	log.Printf("[ota-dl] %s: saved %d bytes → %s", action, n, destPath)
}

// processPublish logs the PUBLISH and optionally modifies the payload.
// Returns (payload, drop): if drop is true the packet should not be forwarded.
func processPublish(dir string, pkt *publishPkt, replaceBinURL string) ([]byte, bool) {
	payloadStr := string(pkt.payload)
	isJSON := len(pkt.payload) > 0 && pkt.payload[0] == '{'

	if isJSON {
		var m map[string]any
		if err := json.Unmarshal(pkt.payload, &m); err == nil {
			pretty, _ := json.MarshalIndent(m, "    ", "  ")
			log.Printf("[%s] PUBLISH topic=%q\n    %s", dir, pkt.topic, pretty)

			// Auto-download OTA artifacts as soon as we see the push.
			if dir == "broker→frame" {
				// Wire action for FPGA OTA is "fpga"; frame replies with "fpga_ota_ack"
				if action, _ := m["action"].(string); action == "ota" || action == "fpga" {
					go downloadOTAArtifact(action, m)
				}
			}

			// Suppress play_ack replies to our injected play messages.
			// The server never sent the play, so it must never see the ack.
			if action, _ := m["action"].(string); action == "play_ack" {
				if data, ok := m["data"].(map[string]any); ok {
					if ackID, _ := data["ack_msgid"].(string); ackID != "" {
						injectedMu.Lock()
						isOurs := injectedMsgIDs[ackID]
						injectedMu.Unlock()
						if isOurs {
							log.Printf("[%s] *** SUPPRESSED play_ack for injected msgid=%s ***", dir, ackID)
							return nil, true
						}
					}
				}
			}

			// Filter unknown broker→frame actions: capture to disk, optionally block.
			if dir == "broker→frame" && *captureDir != "" {
				if action, _ := m["action"].(string); action != "" && !allowedActionSet[action] {
					if err := captureUnknown(*captureDir, action, pkt.payload); err != nil {
						log.Printf("[%s] capture write error: %v", dir, err)
					}
					if *blockUnknown {
						log.Printf("[%s] *** BLOCKED unknown action=%q → %s/%s.jsonl ***", dir, action, *captureDir, action)
						return nil, true
					}
					log.Printf("[%s] *** CAPTURED unknown action=%q → %s/%s.jsonl (forwarded) ***", dir, action, *captureDir, action)
				}
			}

			// Detect play message and optionally replace download URL
			if replaceBinURL != "" && isPlayMessage(m) {
				modified := replaceDownloadURL(m, replaceBinURL)
				if newPayload, err := json.Marshal(modified); err == nil {
					log.Printf("[%s] *** REPLACED download URL → %s ***", dir, replaceBinURL)
					pkt.payload = newPayload
				}
			}
		} else {
			log.Printf("[%s] PUBLISH topic=%q payload=%q", dir, pkt.topic, payloadStr)
		}
	} else {
		log.Printf("[%s] PUBLISH topic=%q payload(%d bytes)=%q",
			dir, pkt.topic, len(pkt.payload), truncate(payloadStr, 120))
	}

	return pkt.payload, false
}

// Play message format (from proxy capture):
//   broker→frame PUBLISH /inkjoyap/{MAC}
//   {
//     "action": "play",
//     "data": {
//       "host": "ink-ufile.s3.eu-west-3.amazonaws.com",
//       "port": 443,
//       "imgs": [{"imgid": "...", "imgurl": "/88/xxxx.bin"}],
//       "mode": 2,
//       "strategy": 1
//     },
//     "msgid": "...",
//     "stamac": "AA:BB:CC:DD:EE:FF"
//   }
//   play_ack result codes: 106=download started, 182-255=progress, 200/255=done

func isPlayMessage(m map[string]any) bool {
	action, _ := m["action"].(string)
	return strings.EqualFold(action, "play")
}

// replaceDownloadURL rewrites host, port, and imgurl inside the play message
// so the frame fetches from our local server instead of S3.
// newURL must be a full URL e.g. "http://192.168.1.10:8080/image.bin"
func replaceDownloadURL(m map[string]any, newURL string) map[string]any {
	// Parse newURL into host, port, path
	host, portStr, path := parseLocalURL(newURL)

	out := make(map[string]any, len(m))
	for k, v := range m {
		if k == "data" {
			if data, ok := v.(map[string]any); ok {
				newData := make(map[string]any, len(data))
				for dk, dv := range data {
					newData[dk] = dv
				}
				newData["host"] = host
				newData["port"] = portStr
				// Replace every entry in imgs array
				if imgs, ok := data["imgs"].([]any); ok {
					newImgs := make([]any, len(imgs))
					for i, img := range imgs {
						if imgMap, ok := img.(map[string]any); ok {
							newImg := make(map[string]any, len(imgMap))
							for ik, iv := range imgMap {
								newImg[ik] = iv
							}
							newImg["imgurl"] = path
							newImgs[i] = newImg
						} else {
							newImgs[i] = img
						}
					}
					newData["imgs"] = newImgs
				}
				out[k] = newData
				continue
			}
		}
		out[k] = v
	}
	return out
}

func parseLocalURL(rawURL string) (host string, port int, path string) {
	// Strip scheme
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Split host:port from path
	slash := strings.Index(s, "/")
	if slash < 0 {
		slash = len(s)
		path = "/"
	} else {
		path = s[slash:]
	}
	hostPort := s[:slash]
	if h, p, err := net.SplitHostPort(hostPort); err == nil {
		host = h
		fmt.Sscanf(p, "%d", &port)
	} else {
		host = hostPort
		if strings.HasPrefix(rawURL, "https") {
			port = 443
		} else {
			port = 80
		}
	}
	return
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// logConnect decodes and logs the CONNECT variable header + payload.
// MQTT 3.1.1 layout:
//   variable header: [0:2] proto name len, [2:6] "MQTT", [6] level, [7] flags, [8:10] keepalive
//   payload fields (each prefixed by 2-byte length):
//     clientID (always), will topic+msg (if flags&0x04), username (if flags&0x80), password (if flags&0x40)
func logConnect(dir string, payload []byte) {
	if len(payload) < 10 {
		return
	}
	flags     := payload[7]
	keepalive := binary.BigEndian.Uint16(payload[8:10])

	readStr := func(off int) (string, int) {
		if off+2 > len(payload) {
			return "", off
		}
		l := int(binary.BigEndian.Uint16(payload[off : off+2]))
		off += 2
		if off+l > len(payload) {
			return "", off
		}
		return string(payload[off : off+l]), off + l
	}

	clientID, off := readStr(10)
	log.Printf("[%s]   CONNECT clientID=%q keepalive=%ds flags=0x%02x", dir, clientID, keepalive, flags)

	if flags&0x04 != 0 { // will flag
		willTopic, o := readStr(off); off = o
		willMsg, o   := readStr(off); off = o
		log.Printf("[%s]   will topic=%q msg=%q", dir, willTopic, willMsg)
	}
	if flags&0x80 != 0 { // username flag
		username, o := readStr(off); off = o
		log.Printf("[%s]   username=%q", dir, username)
	}
	if flags&0x40 != 0 { // password flag
		password, _ := readStr(off)
		log.Printf("[%s]   password=%q", dir, password)
	}
}

// ── Proxy relay ──────────────────────────────────────────────────────────────

// relay forwards packets from src to dst (broker→frame direction only).
// frame→broker is handled directly in handleConn to support broker reconnection.
func relay(src net.Conn, dst net.Conn, dir string, replaceBinURL string, injectCh <-chan []byte) {
	for {
		hdr, payload, err := readPacket(src)
		if err != nil {
			if err != io.EOF {
				log.Printf("[%s] read error: %v", dir, err)
			}
			return
		}

		pktType := hdr >> 4
		name := pktNames[pktType]
		if name == "" {
			name = fmt.Sprintf("PKT%d", pktType)
		}

		// Handle PUBLISH: parse, log, maybe modify or drop
		if pktType == pktPUBLISH {
			pkt, err := parsePublish(hdr, payload)
			if err != nil {
				log.Printf("[%s] PUBLISH parse error: %v (forwarding raw)", dir, err)
			} else {
				var drop bool
				pkt.payload, drop = processPublish(dir, pkt, replaceBinURL)
				if drop {
					continue // swallow packet — don't forward to dst
				}
				payload = pkt.encode()
			}
		} else {
			// Log non-PUBLISH packets briefly
			switch pktType {
			case pktPINGREQ, pktPINGRESP:
				// too noisy, skip
			default:
				log.Printf("[%s] %s (%d payload bytes)", dir, name, len(payload))
				if pktType == pktCONNECT {
					logConnect(dir, payload)
				}
			}
		}

		if err := writePacketTo(dst, hdr, payload); err != nil {
			log.Printf("[%s] write error: %v", dir, err)
			return
		}

		// After any broker→frame packet: drain pending injects.
		// The frame uses application-level heart/heart_ack rather than MQTT
		// PINGREQ/PINGRESP, so we must fire on any packet (PUBACK, PUBLISH, SUBACK…).
		// The channel read is non-blocking so there's no cost when queue is empty.
		if injectCh != nil {
			select {
			case injPayload := <-injectCh:
				log.Printf("[%s] *** INJECTING (triggered by %s) ***", dir, name)
				if err := writePacket(dst, byte(pktPUBLISH<<4), injPayload); err != nil {
					log.Printf("[%s] inject write error: %v", dir, err)
				}
			default:
			}
		}
	}
}

// writePacketTo writes an MQTT packet to any io.Writer (bridge, net.Conn, etc.)
func writePacketTo(w io.Writer, hdr byte, payload []byte) error {
	var buf bytes.Buffer
	buf.WriteByte(hdr)
	remLen := len(payload)
	for {
		b := byte(remLen & 0x7F)
		remLen >>= 7
		if remLen > 0 {
			b |= 0x80
		}
		buf.WriteByte(b)
		if remLen == 0 {
			break
		}
	}
	buf.Write(payload)
	_, err := w.Write(buf.Bytes())
	return err
}

// buildPublishPayload builds the raw MQTT PUBLISH variable header + payload
// for a given topic (QoS 0) and JSON body.
func buildPublishPayload(topic string, body []byte) []byte {
	var buf bytes.Buffer
	tBytes := []byte(topic)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(tBytes)))
	buf.Write(tBytes)
	buf.Write(body)
	return buf.Bytes()
}

// buildPlayPayload constructs the MQTT payload for a play message pointing at url.
// Records the msgid so the proxy can suppress the frame's play_ack reply.
func buildPlayPayload(url string) []byte {
	host, port, path := parseLocalURL(url)
	topic := "/inkjoyap/" + CLIENT_ID
	msgid := fmt.Sprintf("%d", time.Now().UnixMilli())

	// Remember this msgid — we'll drop any play_ack with ack_msgid == msgid
	injectedMu.Lock()
	injectedMsgIDs[msgid] = true
	injectedMu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"action": "play",
		"msgid":  msgid,
		"stamac": STAMAC,
		"data": map[string]any{
			"host":     host,
			"port":     port,
			"imgs":     []any{map[string]any{"imgid": "local-0", "imgurl": path}},
			"mode":     2,
			"strategy": 1,
		},
	})
	return buildPublishPayload(topic, body)
}

// buildActionPayload constructs an MQTT PUBLISH payload for an arbitrary action.
func buildActionPayload(action string, data map[string]any) []byte {
	topic := "/inkjoyap/" + CLIENT_ID
	msg := map[string]any{
		"action": action,
		"msgid":  fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac": STAMAC,
	}
	if data != nil {
		msg["data"] = data
	}
	body, _ := json.Marshal(msg)
	return buildPublishPayload(topic, body)
}

const CLIENT_ID = "AABBCCDDEEFF"
const STAMAC = "AA:BB:CC:DD:EE:FF"

// ── Control server ────────────────────────────────────────────────────────────
// Listens on controlAddr. Accepts newline-terminated JSON commands from local
// tools (e.g. from your Mac via ssh or nc). The frame and the real broker never
// see these injected play messages.
//
// Command format:
//   {"url": "http://192.168.1.10:8080/image.bin"}
//
// Example from Mac:
//   echo '{"url":"http://192.168.1.10:8080/image.bin"}' | ssh ubnt@router nc localhost 18831

func runControlServer() {
	ln, err := net.Listen("tcp", *controlAddr)
	if err != nil {
		log.Printf("Control server failed to listen on %s: %v", *controlAddr, err)
		return
	}
	log.Printf("Control server listening on %s", *controlAddr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleControl(conn)
	}
}

func handleControl(conn net.Conn) {
	defer conn.Close()
	fmt.Fprintf(conn, "inkjoy-proxy ready — send {\"url\":\"http://...\"} to inject, or just watch logs\n")
	fmt.Fprintf(conn, "---\n")

	// Subscribe to log fan-out before reading the command so we don't miss lines
	logCh := fan.subscribe()
	defer fan.unsubscribe(logCh)

	// Stream logs to this client in background
	go func() {
		for line := range logCh {
			if _, err := conn.Write(line); err != nil {
				return
			}
		}
	}()

	// Read one optional JSON command (non-blocking with short deadline).
	// Formats:
	//   {"url":"http://..."}                          — inject play message
	//   {"action":"image_refresh"}                    — inject arbitrary action (no data)
	//   {"action":"down_int_img","data":{...}}        — inject action with data payload
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var cmd struct {
		URL    string         `json:"url"`
		Action string         `json:"action"`
		Data   map[string]any `json:"data"`
	}
	if err := json.NewDecoder(conn).Decode(&cmd); err == nil {
		conn.SetReadDeadline(time.Time{})

		var payload []byte
		var desc string
		if cmd.URL != "" {
			payload = buildPlayPayload(cmd.URL)
			desc = "play → " + cmd.URL
		} else if cmd.Action != "" {
			payload = buildActionPayload(cmd.Action, cmd.Data)
			desc = "action:" + cmd.Action
		}

		if payload != nil {
			select {
			case pendingInject <- payload:
				fmt.Fprintf(conn, "OK: %s queued (fires on next frame SUBACK)\n", desc)
				log.Printf("[control] %s queued", desc)
			default:
				fmt.Fprintf(conn, "ERROR: inject queue full\n")
			}
		} else {
			conn.SetReadDeadline(time.Time{})
		}
	} else {
		conn.SetReadDeadline(time.Time{}) // watch-only mode — just stream logs
	}

	// Keep connection alive until client disconnects (log stream drains it)
	io.Copy(io.Discard, conn)
}

// ── Connection handler ───────────────────────────────────────────────────────

// brokerBridge holds a replaceable broker connection shared between goroutines.
type brokerBridge struct {
	mu   sync.Mutex
	conn net.Conn
}

func (b *brokerBridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	c := b.conn
	b.mu.Unlock()
	if c == nil {
		return 0, io.ErrClosedPipe
	}
	return c.Write(p)
}

func (b *brokerBridge) swap(newConn net.Conn) net.Conn {
	b.mu.Lock()
	old := b.conn
	b.conn = newConn
	b.mu.Unlock()
	return old
}

func handleConn(clientConn net.Conn, startupInjectPayload []byte) {
	defer clientConn.Close()

	brokerConn, err := net.Dial("tcp", *broker)
	if err != nil {
		log.Printf("Cannot reach broker %s: %v", *broker, err)
		return
	}

	log.Printf("New session: frame=%s ↔ broker=%s", clientConn.RemoteAddr(), brokerConn.RemoteAddr())

	if startupInjectPayload != nil {
		pendingInject <- startupInjectPayload
	}

	bridge := &brokerBridge{conn: brokerConn}

	// frameDone closes when the frame disconnects — signals the broker loop to stop.
	frameDone := make(chan struct{})

	// savedConnect/savedSubscribe let us replay the session when the broker kicks us.
	var (
		savedMu        sync.Mutex
		savedConnect   []byte // raw CONNECT packet payload
		savedConnectHdr byte
		savedSubscribe []byte // raw SUBSCRIBE packet payload
		savedSubscribeHdr byte
	)

	// frame→broker: intercept CONNECT and SUBSCRIBE to save for replay; write via bridge.
	go func() {
		defer close(frameDone)
		sawDisconnect := false
		defer func() {
			if !sawDisconnect {
				log.Printf("[frame→broker] source dropped without DISCONNECT — sending DISCONNECT to broker")
				hdr := byte(pktDISCONNECT << 4)
				_ = writePacketTo(bridge, hdr, nil)
			}
		}()
		for {
			hdr, payload, err := readPacket(clientConn)
			if err != nil {
				if err != io.EOF {
					log.Printf("[frame→broker] read error: %v", err)
				}
				return
			}
			pktType := hdr >> 4
			if pktType == pktDISCONNECT {
				sawDisconnect = true
			}
			if pktType == pktCONNECT {
				savedMu.Lock()
				savedConnectHdr = hdr
				savedConnect = append([]byte(nil), payload...)
				savedMu.Unlock()
				logConnect("frame→broker", payload)
			}
			if pktType == pktSUBSCRIBE {
				savedMu.Lock()
				savedSubscribeHdr = hdr
				savedSubscribe = append([]byte(nil), payload...)
				savedMu.Unlock()
				log.Printf("[frame→broker] SUBSCRIBE (%d payload bytes)", len(payload))
			}
			if pktType == pktPUBLISH {
				pkt, err := parsePublish(hdr, payload)
				if err == nil {
					pkt.payload, _ = processPublish("frame→broker", pkt, "")
					payload = pkt.encode()
				}
			}
			if err := writePacketTo(bridge, hdr, payload); err != nil {
				log.Printf("[frame→broker] write error: %v", err)
				return
			}
		}
	}()

	// broker→frame loop: relay, reconnecting to broker if kicked.
	currentBroker := brokerConn
	for {
		relay(currentBroker, clientConn, "broker→frame", *replaceBin, pendingInject)

		// Check if the frame is gone — if so, stop.
		select {
		case <-frameDone:
			currentBroker.Close()
			log.Printf("Session closed: frame=%s", clientConn.RemoteAddr())
			return
		default:
		}

		// Broker dropped us (likely kicked by another client with same clientID).
		// Reconnect and replay the session.
		currentBroker.Close()
		log.Printf("[broker→frame] broker connection lost — reconnecting")
		time.Sleep(500 * time.Millisecond)

		newBroker, err := net.Dial("tcp", *broker)
		if err != nil {
			log.Printf("[broker→frame] reconnect failed: %v — closing frame connection", err)
			return
		}

		savedMu.Lock()
		ch, chHdr := savedConnect, savedConnectHdr
		sub, subHdr := savedSubscribe, savedSubscribeHdr
		savedMu.Unlock()

		if ch != nil {
			if err := writePacketTo(newBroker, chHdr, ch); err != nil {
				log.Printf("[broker→frame] replay CONNECT failed: %v", err)
				newBroker.Close()
				return
			}
			// Read CONNACK
			_, _, _ = readPacket(newBroker)
		}
		if sub != nil {
			if err := writePacketTo(newBroker, subHdr, sub); err != nil {
				log.Printf("[broker→frame] replay SUBSCRIBE failed: %v", err)
				newBroker.Close()
				return
			}
		}

		bridge.swap(newBroker)
		currentBroker = newBroker
		log.Printf("[broker→frame] reconnected to broker, session resumed")
	}
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetOutput(fan)

	// Build allowedActionSet from --allow-actions flag
	allowedActionSet = make(map[string]bool)
	for _, a := range strings.Split(*allowActions, ",") {
		if a = strings.TrimSpace(a); a != "" {
			allowedActionSet[a] = true
		}
	}

	log.Printf("inkjoy-proxy starting")
	log.Printf("  upstream broker : %s", *broker)
	log.Printf("  listen          : %s", *listenAddr)
	if *replaceBin != "" {
		log.Printf("  replace-bin     : %s", *replaceBin)
	}
	if *otaDir != "" {
		log.Printf("  ota-dir         : %s  (auto-download ota→.bin, fpga_ota→.fs)", *otaDir)
	}
	if *captureDir != "" {
		mode := "capture+forward"
		if *blockUnknown {
			mode = "capture+BLOCK"
		}
		log.Printf("  filter          : unknown broker→frame actions → %s (%s)", *captureDir, mode)
		log.Printf("  allowed actions : %s", *allowActions)
	}

	// Pre-load inject payload if requested
	var injectPayload []byte
	if *injectPlay != "" {
		raw, err := os.ReadFile(*injectPlay)
		if err != nil {
			log.Fatalf("Cannot read inject-play file: %v", err)
		}
		// Validate JSON
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			log.Fatalf("inject-play is not valid JSON: %v", err)
		}
		// Determine topic from payload or use default
		topic := "/inkjoyap/AABBCCDDEEFF"
		if t, ok := m["_topic"].(string); ok {
			topic = t
			delete(m, "_topic")
			raw, _ = json.Marshal(m)
		}
		injectPayload = buildPublishPayload(topic, raw)
		log.Printf("  inject-play     : %d bytes → topic %s", len(raw), topic)
	}

	log.Printf("  control         : %s  (send {\"url\":\"http://...\"} to inject play)", *controlAddr)

	go runControlServer()

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("Listen %s: %v", *listenAddr, err)
	}
	log.Printf("Listening on %s", *listenAddr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleConn(conn, injectPayload)
	}
}
