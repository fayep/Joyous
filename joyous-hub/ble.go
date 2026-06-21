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
	defer dev.Disconnect()

	// Discover BluFi service.
	svcUUID, _ := bluetooth.ParseUUID("0000ffff-0000-1000-8000-00805f9b34fb")
	svcs, err := dev.DiscoverServices([]bluetooth.UUID{svcUUID})
	if err != nil || len(svcs) == 0 {
		return fmt.Errorf("BluFi service not found: %v", err)
	}

	// Discover write characteristic (P2E).
	writeUUID, _ := bluetooth.ParseUUID("0000ff01-0000-1000-8000-00805f9b34fb")
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{writeUUID})
	if err != nil || len(chars) == 0 {
		return fmt.Errorf("BluFi write char not found: %v", err)
	}
	p2e := chars[0]

	var seq uint8
	write := func(typeByte byte, data []byte) error {
		pkt := append([]byte{typeByte, 0x00, seq, byte(len(data))}, data...)
		seq++
		log.Printf("BLE adopt: → type=0x%02x len=%d", typeByte, len(data))
		_, err := p2e.Write(pkt)
		return err
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
	time.Sleep(3 * time.Second) // give frame time to ack and reconnect
	return nil
}
