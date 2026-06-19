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
	"os"
	"strings"
	"sync"
	"time"
)

var (
	broker      = flag.String("broker", "13.39.148.101:1883", "upstream MQTT broker")
	listenAddr  = flag.String("listen", ":18830", "local listen address (all interfaces)")
	controlAddr = flag.String("control", "192.168.1.1:18831", "control/log port (LAN only — not exposed to WAN)")
	replaceBin  = flag.String("replace-bin", "", "replace download URL in intercepted play messages")
	injectPlay  = flag.String("inject-play", "", "JSON file to inject as a play message immediately on connect")
)

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

// ── Proxy relay ──────────────────────────────────────────────────────────────

func relay(src, dst net.Conn, dir string, replaceBinURL string, injectCh <-chan []byte) {
	defer dst.Close()

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
				if pktType == pktCONNECT && len(payload) >= 10 {
					keepalive := binary.BigEndian.Uint16(payload[6:8])
					clientIDLen := int(binary.BigEndian.Uint16(payload[8:10]))
					if len(payload) >= 10+clientIDLen {
						log.Printf("[%s]   clientID=%q keepalive=%ds",
							dir, payload[10:10+clientIDLen], keepalive)
					}
				}
			}
		}

		if err := writePacket(dst, hdr, payload); err != nil {
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
				log.Printf("[%s] *** INJECTING play (triggered by %s) ***", dir, name)
				if err := writePacket(dst, byte(pktPUBLISH<<4), injPayload); err != nil {
					log.Printf("[%s] inject write error: %v", dir, err)
				}
			default:
			}
		}
	}
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

	// Read one optional JSON command (non-blocking with short deadline)
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var cmd struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(conn).Decode(&cmd); err == nil && cmd.URL != "" {
		conn.SetReadDeadline(time.Time{}) // clear deadline — keep connection for logs

		payload := buildPlayPayload(cmd.URL)
		select {
		case pendingInject <- payload:
			fmt.Fprintf(conn, "OK: play queued → %s\n", cmd.URL)
			fmt.Fprintf(conn, "    fires on next frame SUBACK (~20s poll cycle)\n")
			log.Printf("[control] play queued → %s (fires on next SUBACK)", cmd.URL)
		default:
			fmt.Fprintf(conn, "ERROR: inject queue full\n")
		}
	} else {
		conn.SetReadDeadline(time.Time{}) // watch-only mode — just stream logs
	}

	// Keep connection alive until client disconnects (log stream drains it)
	io.Copy(io.Discard, conn)
}

// ── Connection handler ───────────────────────────────────────────────────────

func handleConn(clientConn net.Conn, startupInjectPayload []byte) {
	defer clientConn.Close()

	brokerConn, err := net.Dial("tcp", *broker)
	if err != nil {
		log.Printf("Cannot reach broker %s: %v", *broker, err)
		return
	}
	defer brokerConn.Close()

	log.Printf("New session: frame=%s ↔ broker=%s", clientConn.RemoteAddr(), brokerConn.RemoteAddr())

	// One-shot startup injection (--inject-play flag) — pre-load before session starts
	if startupInjectPayload != nil {
		pendingInject <- startupInjectPayload
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		relay(clientConn, brokerConn, "frame→broker", "", nil)
	}()
	go func() {
		defer wg.Done()
		relay(brokerConn, clientConn, "broker→frame", *replaceBin, pendingInject)
	}()
	wg.Wait()
	log.Printf("Session closed: frame=%s", clientConn.RemoteAddr())
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetOutput(fan)
	log.Printf("inkjoy-proxy starting")
	log.Printf("  upstream broker : %s", *broker)
	log.Printf("  listen          : %s", *listenAddr)
	if *replaceBin != "" {
		log.Printf("  replace-bin     : %s", *replaceBin)
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
