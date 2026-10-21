//go:build inkjoybridge

// BLE (BluFi) frame provisioning. Only inkjoy-bridge needs this — the plain
// hub doesn't talk Bluetooth at all; BLE scan/adopt is tunneled to the bridge
// over MQTT via inkjoy_bridge_ui.go, the same way its config UI is.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// BLEFrame is an InkJoy frame found during a BLE scan.
type BLEFrame struct {
	Name    string `json:"name"`
	Address string `json:"address"` // UUID string on macOS
	MAC     string `json:"mac"`     // e.g. "AABBCCDDEEFF" from IJ_D prefix
}

// bleState serialises all BLE adapter operations — CoreBluetooth is not thread-safe.
var bleState struct {
	sync.Mutex
	ready   bool
	adapter *bluetooth.Adapter
	// cache of recently scanned address strings → actual Address value for Connect
	cache map[string]bluetooth.Address
}

func bleAdapter() (*bluetooth.Adapter, error) {
	bleState.Lock()
	defer bleState.Unlock()
	if bleState.adapter == nil {
		a := bluetooth.DefaultAdapter
		if err := a.Enable(); err != nil {
			return nil, fmt.Errorf("BLE enable: %w", err)
		}
		bleState.adapter = a
		bleState.cache = map[string]bluetooth.Address{}
	}
	return bleState.adapter, nil
}

// ScanBLEFrames scans for InkJoy BLE frames for the given duration and returns
// those whose name starts with "IJ_".
func ScanBLEFrames(timeout time.Duration) ([]BLEFrame, error) {
	a, err := bleAdapter()
	if err != nil {
		return nil, err
	}

	bleState.Lock()
	bleState.cache = map[string]bluetooth.Address{}
	bleState.Unlock()

	var mu sync.Mutex
	var frames []BLEFrame
	seen := map[string]bool{}

	// Stop scan after timeout.
	time.AfterFunc(timeout, func() { a.StopScan() })

	err = a.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		name := r.LocalName()
		if !strings.HasPrefix(name, "IJ_") {
			return
		}
		addr := r.Address.String()
		mu.Lock()
		defer mu.Unlock()
		if seen[addr] {
			return
		}
		seen[addr] = true
		mac := strings.TrimPrefix(name, "IJ_")
		frames = append(frames, BLEFrame{Name: name, Address: addr, MAC: mac})
		bleState.Lock()
		bleState.cache[addr] = r.Address
		bleState.Unlock()
		log.Printf("BLE scan: %s  %s", name, addr)
	})
	// Scan() returns after StopScan(); ignore the stop-error.
	if err != nil && !strings.Contains(err.Error(), "stop") {
		return nil, fmt.Errorf("BLE scan: %w", err)
	}
	return frames, nil
}

// AdoptBLEFrame connects to an InkJoy frame (identified by its BLE address UUID)
// and sends the full BluFi provisioning sequence: op-mode, WiFi SSID+password,
// then mqtt_config pointing at the hub.
func AdoptBLEFrame(addrStr, ssid, wifiPwd, mqttHost string, mqttPort int, mqttUsr, mqttPwd string) error {
	a, err := bleAdapter()
	if err != nil {
		return err
	}

	bleState.Lock()
	addr, ok := bleState.cache[addrStr]
	bleState.Unlock()
	if !ok {
		return fmt.Errorf("address %q not in scan cache — scan first", addrStr)
	}

	log.Printf("BLE adopt: connecting to %s", addrStr)
	dev, err := a.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("BLE connect: %w", err)
	}
	// Disconnect on any early/error return. On the happy path we hand the live
	// connection to a background goroutine (see the end) and clear this so the
	// HTTP handler can return without waiting on the post-provision report —
	// the hub↔bridge UI tunnel times out at 20s (bridgehub defaultUIHTTPTimeout).
	disconnect := true
	defer func() {
		if disconnect {
			dev.Disconnect()
		}
	}()

	// Discover BluFi service.
	svcUUID, _ := bluetooth.ParseUUID("0000ffff-0000-1000-8000-00805f9b34fb")
	svcs, err := dev.DiscoverServices([]bluetooth.UUID{svcUUID})
	if err != nil || len(svcs) == 0 {
		return fmt.Errorf("BluFi service not found: %v", err)
	}

	// Discover the BluFi write (0xff01, P2E) and notify (0xff02, E2P)
	// characteristics. Pre-v1.1 frames (e.g. the 0.5.6 10") are write-only from
	// our side, but v1.1.x/v1.3.x frames report back over 0xff02 — notably a
	// `wifi_connect_success` custom-data frame once they associate to WiFi — so
	// we subscribe before sending creds both to ack that (the frame otherwise
	// stalls 1.5s then resets anyway) and, more usefully, to observe whether the
	// frame actually joined WiFi and which IP it got.
	writeUUID, _ := bluetooth.ParseUUID("0000ff01-0000-1000-8000-00805f9b34fb")
	notifyUUID, _ := bluetooth.ParseUUID("0000ff02-0000-1000-8000-00805f9b34fb")
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{writeUUID, notifyUUID})
	if err != nil {
		return fmt.Errorf("discover BluFi characteristics: %w", err)
	}
	var p2e, e2p bluetooth.DeviceCharacteristic
	var haveWrite, haveNotify bool
	for _, c := range chars {
		switch c.UUID() {
		case writeUUID:
			p2e, haveWrite = c, true
		case notifyUUID:
			e2p, haveNotify = c, true
		}
	}
	if !haveWrite {
		return fmt.Errorf("BluFi write char (0xff01) not found")
	}

	var seq uint8
	write := func(typeByte byte, data []byte) error {
		pkt := append([]byte{typeByte, 0x00, seq, byte(len(data))}, data...)
		seq++
		log.Printf("BLE adopt: → type=0x%02x len=%d", typeByte, len(data))
		_, err := p2e.Write(pkt)
		return err
	}

	// Subscribe to 0xff02 (if present) and reassemble inbound BluFi custom-data
	// frames into whole JSON messages. Older frames never notify; that's fine.
	rx := &bluFiReassembler{msgs: make(chan []byte, 8)}
	if haveNotify {
		if err := e2p.EnableNotifications(func(buf []byte) { rx.feed(buf) }); err != nil {
			log.Printf("BLE adopt: notify subscribe failed (continuing write-only): %v", err)
			haveNotify = false
		}
	} else {
		log.Printf("BLE adopt: no 0xff02 notify char (pre-v1.1 frame?) — write-only")
	}

	// 1. Set op mode → STA (type 8)
	if err := write(8, []byte{1}); err != nil {
		return fmt.Errorf("set op mode: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	// 2. WiFi SSID (type 9)
	if err := write(9, []byte(ssid)); err != nil {
		return fmt.Errorf("send SSID: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	// 3. WiFi password (type 13)
	if err := write(13, []byte(wifiPwd)); err != nil {
		return fmt.Errorf("send WiFi password: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	// 4. End of WiFi info (type 12, empty)
	if err := write(12, []byte{}); err != nil {
		return fmt.Errorf("send WiFi end: %w", err)
	}
	time.Sleep(1 * time.Second)

	// 5. mqtt_config JSON (type 77)
	cfg := map[string]any{
		"msgid":  fmt.Sprintf("%d", time.Now().UnixMilli()),
		"action": "mqtt_config",
		"data": map[string]any{
			"host": mqttHost,
			"port": mqttPort,
			"usr":  mqttUsr,
			"pwd":  mqttPwd,
		},
	}
	payload, _ := json.Marshal(cfg)
	if err := write(77, payload); err != nil {
		return fmt.Errorf("send mqtt_config: %w", err)
	}

	log.Printf("BLE adopt: mqtt_config sent to %s — host=%s port=%d", addrStr, mqttHost, mqttPort)

	// The creds are delivered; the frame now associates to WiFi on its own. We
	// must NOT block the HTTP response on the follow-up report — the hub↔bridge
	// UI tunnel times out at 20s (bridgehub defaultUIHTTPTimeout) and WiFi
	// association + the frame's BSSID-failover preconnect can run longer. So we
	// return now and watch the BluFi notify stream in the background: v1.1.x/
	// v1.3.x frames push a `wifi_connect_success` custom-data frame over 0xff02
	// once associated (and wait ~1.5s for an ack before resetting into a clean
	// MQTT session). We ack it and log what it reports — did it join WiFi? which
	// IP? — so a failed adopt is diagnosable from the bridge log. Write-only
	// frames (0.5.6) have nothing to watch; just a short grace, then disconnect.
	if !haveNotify {
		time.Sleep(2 * time.Second)
		return nil // defer disconnects
	}
	disconnect = false // ownership of dev passes to the goroutine below
	go func() {
		defer dev.Disconnect()
		deadline := time.After(25 * time.Second)
		for {
			select {
			case raw := <-rx.msgs:
				log.Printf("BLE adopt: ← %s", raw)
				var msg struct {
					Action string `json:"action"`
					Data   struct {
						Msgid string `json:"msgid"`
						SSID  string `json:"ssid"`
						IP    string `json:"ip"`
					} `json:"data"`
				}
				if json.Unmarshal(raw, &msg) != nil {
					continue
				}
				if msg.Action == "wifi_connect_success" {
					log.Printf("BLE adopt: frame joined WiFi ssid=%q ip=%q — acking", msg.Data.SSID, msg.Data.IP)
					ack, _ := json.Marshal(map[string]any{
						"action": "wifi_connect_success_ack",
						"data":   map[string]any{"msgid": msg.Data.Msgid, "result": 0},
					})
					if err := write(bluFiTypeCustomData, ack); err != nil {
						log.Printf("BLE adopt: wifi_connect_success_ack write failed: %v", err)
					}
					time.Sleep(500 * time.Millisecond) // let the ack flush; frame resets regardless
					return
				}
			case <-deadline:
				log.Printf("BLE adopt: no wifi_connect_success within 25s — frame may not have joined WiFi (check SSID/password/band: frame is 2.4GHz BluFi)")
				return
			}
		}
	}()
	return nil
}

const (
	// bluFiTypeCustomData is a BluFi data frame (frame-type bits = 01b) with
	// subtype 19 (custom data): typeByte = (19<<2)|1 = 0x4d. It's the framing
	// the frame uses for its JSON reports (wifi_connect_success), and the same
	// one our mqtt_config write (type 77 = 0x4d) already uses.
	bluFiTypeCustomData = 0x4d
	// bluFiFragFlag in the frame-control byte signals more fragments follow.
	// When set, the first 2 bytes of the data field are the total content
	// length; the final fragment clears the flag and carries no such prefix.
	// Reports larger than the ATT MTU (default 20B of payload) arrive this way.
	bluFiFragFlag = 0x10
)

// bluFiReassembler decodes inbound BluFi notifications from 0xff02 and joins
// custom-data fragments into whole JSON payloads, delivered on msgs. BluFi
// frame layout: [type, frameControl, seq, dataLen, data...] (a 2-byte checksum
// may trail if that frame-control flag is set; we don't parse it). We only
// consume unencrypted custom-data frames — the frame negotiates no BluFi
// encryption with us, so its JSON reports come through in the clear.
type bluFiReassembler struct {
	mu   sync.Mutex
	buf  []byte
	msgs chan []byte
}

func (r *bluFiReassembler) feed(pkt []byte) {
	if len(pkt) < 4 {
		return
	}
	typ, ctrl, dlen := pkt[0], pkt[1], int(pkt[3])
	if 4+dlen > len(pkt) {
		dlen = len(pkt) - 4 // tolerate a short/garbled length field
	}
	data := pkt[4 : 4+dlen]
	log.Printf("BLE adopt: ← frame type=0x%02x ctrl=0x%02x len=%d", typ, ctrl, dlen)
	if typ != bluFiTypeCustomData {
		return // status/other frames — logged above, not reassembled
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctrl&bluFiFragFlag != 0 {
		if len(data) >= 2 { // strip the 2-byte total-length prefix
			r.buf = append(r.buf, data[2:]...)
		}
		return
	}
	payload := append(r.buf, data...)
	r.buf = nil
	select {
	case r.msgs <- payload:
	default: // never block the BLE notification callback
	}
}
