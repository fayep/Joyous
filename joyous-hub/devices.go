package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DeviceType identifies how to reach a photo frame.
type DeviceType string

const (
	DeviceTypeInkJoy  DeviceType = "inkjoy"
	DeviceTypeSamsung DeviceType = "samsung"
)

// Device holds runtime state for a connected or discovered frame.
type Device struct {
	ID         string     `json:"id"`
	Type       DeviceType `json:"type"`
	Name       string     `json:"name,omitempty"`
	MAC        string     `json:"mac,omitempty"`
	IP         string     `json:"ip,omitempty"`
	USN        string     `json:"usn,omitempty"`
	Location   string     `json:"location,omitempty"`
	Firmware   string     `json:"firmware,omitempty"`
	Battery    int        `json:"battery"`
	RSSI       int        `json:"rssi"`
	Connected  bool       `json:"connected"`
	LastSeen   time.Time  `json:"last_seen"`
	LastAction string     `json:"last_action,omitempty"`
	MDCPin            string     `json:"mdc_pin,omitempty"`
	MDCMAC            string     `json:"mdc_mac,omitempty"` // optional WoL target
	DisplayCropFormat string     `json:"display_crop_format,omitempty"` // e.g. "16:9"
	DisplayWidth      int        `json:"display_width,omitempty"`
	DisplayHeight     int        `json:"display_height,omitempty"`
}

// DeviceRegistry tracks known frames in memory and persists them to disk.
type DeviceRegistry struct {
	mu  sync.RWMutex
	m   map[string]*Device
	dir string
}

// NewDeviceRegistry creates a DeviceRegistry. Call Load() to restore from disk.
func NewDeviceRegistry(dir string) *DeviceRegistry {
	return &DeviceRegistry{m: make(map[string]*Device), dir: dir}
}

func inkjoyID(mac string) string { return mac }

func samsungID(ip string) string { return "samsung:" + ip }

// Get returns a device by ID, or nil.
func (r *DeviceRegistry) Get(id string) (*Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.m[id]
	if !ok {
		return nil, false
	}
	cp := *d
	return &cp, true
}

// MarkConnected records that an InkJoy frame with the given MAC has connected.
func (r *DeviceRegistry) MarkConnected(mac string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	d.Connected = true
	d.LastSeen = time.Now()
}

// MarkDisconnected marks the device as offline.
func (r *DeviceRegistry) MarkDisconnected(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.m[id]; ok {
		d.Connected = false
	}
}

// UpdateHeart applies telemetry from a heart message.
func (r *DeviceRegistry) UpdateHeart(mac string, info HeartInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	d.Battery = info.Battery
	d.RSSI = info.RSSI
	if info.Firmware != "" {
		d.Firmware = info.Firmware
	}
	d.LastSeen = time.Now()
	d.LastAction = "heart"
}

// UpdateLogin applies login info.
func (r *DeviceRegistry) UpdateLogin(mac string, info LoginInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	if info.Firmware != "" {
		d.Firmware = info.Firmware
	}
	d.LastSeen = time.Now()
	d.LastAction = "login"
}

// UpsertSamsung registers or refreshes a Samsung frame from SSDP discovery.
func (r *DeviceRegistry) UpsertSamsung(found SSDPDevice) *Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := samsungID(found.IP)
	d, ok := r.m[id]
	if !ok {
		d = &Device{
			ID:   id,
			Type: DeviceTypeSamsung,
			IP:   found.IP,
			Name: found.DisplayName(),
		}
		r.m[id] = d
	}
	d.IP = found.IP
	d.USN = found.USN
	d.Location = found.Location
	if found.Server != "" {
		d.Name = found.DisplayName()
	}
	applySamsungDisplayProfile(d, found.DisplayProfile())
	d.LastSeen = time.Now()
	d.LastAction = "discover"
	// Samsung frames are reachable when discovered; not the same as MQTT connected.
	d.Connected = true
	return d
}

// List returns a snapshot of all known devices sorted by type then name/id.
func (r *DeviceRegistry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Device, 0, len(r.m))
	for _, d := range r.m {
		out = append(out, *d)
	}
	return out
}

// Save persists the registry to {dir}/devices.json.
func (r *DeviceRegistry) Save() error {
	r.mu.RLock()
	snapshot := make([]*Device, 0, len(r.m))
	for _, d := range r.m {
		cp := *d
		cp.Connected = false
		snapshot = append(snapshot, &cp)
	}
	r.mu.RUnlock()

	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "devices.json"), b, 0644)
}

// Load restores the registry from {dir}/devices.json (if it exists).
func (r *DeviceRegistry) Load() error {
	data, err := os.ReadFile(filepath.Join(r.dir, "devices.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var devs []*Device
	if err := json.Unmarshal(data, &devs); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range devs {
		d.Connected = false
		migrateDevice(d)
		r.m[d.ID] = d
	}
	return nil
}

func migrateDevice(d *Device) {
	if d.ID == "" && d.MAC != "" {
		d.ID = d.MAC
	}
	if d.Type == "" {
		if strings.HasPrefix(d.ID, "samsung:") {
			d.Type = DeviceTypeSamsung
			if d.IP == "" {
				d.IP = strings.TrimPrefix(d.ID, "samsung:")
			}
		} else {
			d.Type = DeviceTypeInkJoy
			if d.MAC == "" {
				d.MAC = d.ID
			}
		}
	}
}

func (r *DeviceRegistry) getOrCreateInkJoy(mac string) *Device {
	id := inkjoyID(mac)
	d, ok := r.m[id]
	if !ok {
		d = &Device{ID: id, Type: DeviceTypeInkJoy, MAC: mac}
		r.m[id] = d
	}
	return d
}

// SamsungFrameID returns the samsung store key for a device (IP-based).
func SamsungFrameID(dev *Device) string {
	if dev.IP != "" {
		return strings.ReplaceAll(dev.IP, ".", "-")
	}
	return strings.TrimPrefix(dev.ID, "samsung:")
}

func frameIDToIP(frameID string) string {
	if strings.Count(frameID, "-") == 3 && !strings.Contains(frameID, "/") {
		return strings.ReplaceAll(frameID, "-", ".")
	}
	return ""
}
