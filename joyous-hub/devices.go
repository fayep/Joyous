package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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

// SamsungRecentWindow is how long after hub contact a Samsung frame counts as active.
const SamsungRecentWindow = 5 * time.Minute

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
	Battery     int        `json:"battery"`
	PowerSource string     `json:"power_source,omitempty"` // samsung: ac, usb, wireless
	// Samsung battery history (API-only; not persisted on Device in devices.json).
	BatterySamples   int                    `json:"battery_samples,omitempty"`
	BatteryDelta     *int                   `json:"battery_delta,omitempty"`
	BatteryPushDelta *int                   `json:"battery_push_delta,omitempty"`
	BatteryAt        time.Time              `json:"battery_at,omitempty"`
	BatteryHistory   []SamsungBatterySample `json:"battery_history,omitempty"`
	RSSI        int        `json:"rssi"`
	Connected   bool       `json:"connected"`
	LastSeen    time.Time  `json:"last_seen"`
	LastAction string     `json:"last_action,omitempty"`
	MDCPin            string     `json:"mdc_pin,omitempty"`
	MDCMAC            string     `json:"mdc_mac,omitempty"` // optional WoL target
	DisplayCropFormat string     `json:"display_crop_format,omitempty"` // e.g. "16:9"
	DisplayWidth      int        `json:"display_width,omitempty"`
	DisplayHeight     int        `json:"display_height,omitempty"`
	// InkJoy-specific
	SleepBeginTime string `json:"sleep_begin_time,omitempty"`
	SleepEndTime   string `json:"sleep_end_time,omitempty"`
	LastImageID       string    `json:"last_image_id,omitempty"`
	DisplayPreviewAt  time.Time `json:"display_preview_at,omitempty"`
	HubIP          string `json:"hub_ip,omitempty"`   // hub's LAN IP as seen via this frame's MQTT socket
	Portrait       bool   `json:"portrait,omitempty"` // user-set: frame is in portrait orientation
	Orientation    int    `json:"orientation"`        // raw accelerometer value from heart (unreliable)
	DeepSleepActive bool  `json:"deep_sleep_active,omitempty"` // samsung: overnight deep sleep (button wake)
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

// SamsungRecentlySeen reports whether the frame contacted the hub within SamsungRecentWindow.
func SamsungRecentlySeen(lastSeen time.Time) bool {
	return !lastSeen.IsZero() && time.Since(lastSeen) < SamsungRecentWindow
}

// samsungActionProvesAwake reports whether LastAction indicates the frame was reachable while awake.
func samsungActionProvesAwake(action string) bool {
	switch action {
	case "mdc_session", "mdc_push", "mdc_wake", "mdc_battery", "content.json", "png":
		return true
	default:
		return false
	}
}

// ApplySamsungConnected sets Connected for Samsung devices from recent awake proof (InkJoy unchanged).
func ApplySamsungConnected(d *Device) {
	if d == nil || d.Type != DeviceTypeSamsung {
		return
	}
	if d.LastAction == "mdc_sleep" {
		d.Connected = false
		return
	}
	d.Connected = SamsungRecentlySeen(d.LastSeen) && samsungActionProvesAwake(d.LastAction)
}

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

// SetHubIP records the hub's LAN IP as observed from this frame's MQTT socket.
func (r *DeviceRegistry) SetHubIP(mac, ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	d.HubIP = ip
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
	d.Orientation = info.Orientation
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
	if info.SleepBeginTime != "" {
		d.SleepBeginTime = info.SleepBeginTime
	}
	if info.SleepEndTime != "" {
		d.SleepEndTime = info.SleepEndTime
	}
	d.LastSeen = time.Now()
	d.LastAction = "login"
}

// SetLastImage records the most recently sent image for a device.
func (r *DeviceRegistry) SetLastImage(mac, imageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	d.LastImageID = imageID
	d.DisplayPreviewAt = time.Time{}
}

// SetDisplayPreview marks an externally fetched preview as current (clears hub album thumb).
func (r *DeviceRegistry) SetDisplayPreview(mac string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	d.DisplayPreviewAt = time.Now()
	d.LastImageID = ""
}

// SetDisplayPreviewAt restores display preview timestamp from disk cache (startup).
func (r *DeviceRegistry) SetDisplayPreviewAt(mac string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	if at.After(d.DisplayPreviewAt) {
		d.DisplayPreviewAt = at
	}
}

// UpdateSleep stores the sleep schedule on an InkJoy device after hub sends wifi_sleep.
func (r *DeviceRegistry) UpdateSleep(mac, beginTime, endTime string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	d.SleepBeginTime = beginTime
	d.SleepEndTime = endTime
}

// SetName updates the friendly name for any device.
func (r *DeviceRegistry) SetName(id, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	d.Name = name
	return true
}

func (r *DeviceRegistry) SetPortrait(id string, portrait bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	d.Portrait = portrait
	return true
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
	applySamsungDisplayProfile(d, found.DisplayProfile())
	d.LastAction = "discover"
	return d
}

// SetMDCMAC stores the WiFi MAC used for Samsung WoL / magic wake.
func (r *DeviceRegistry) SetMDCMAC(id, mac string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	d.MDCMAC = strings.TrimSpace(mac)
	return true
}

// NoteSamsungSlept records a successful sleep command without marking the frame awake.
func (r *DeviceRegistry) NoteSamsungSlept(ip string) bool {
	if ip == "" {
		return false
	}
	id := samsungID(ip)
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	d.LastAction = "mdc_sleep"
	return true
}

// TouchSamsung records hub contact from a Samsung frame (HTTP poll, PNG fetch, MDC probe, etc.).
func (r *DeviceRegistry) TouchSamsung(ip, action string) bool {
	if ip == "" {
		return false
	}
	id := samsungID(ip)
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	d.LastSeen = time.Now()
	d.LastAction = action
	return true
}

// UpdateSamsungBattery stores MDC battery telemetry for a registered Samsung frame.
func (r *DeviceRegistry) UpdateSamsungBattery(ip string, percent int, powerSource string) bool {
	if ip == "" {
		return false
	}
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	id := samsungID(ip)
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	d.Battery = percent
	d.PowerSource = powerSource
	d.LastSeen = time.Now()
	d.LastAction = "mdc_battery"
	return true
}

// deviceDisplayLabel returns the same label the web UI shows for sorting.
func deviceDisplayLabel(d Device) string {
	if s := strings.TrimSpace(d.Name); s != "" {
		return s
	}
	if d.Type == DeviceTypeInkJoy && d.MAC != "" {
		return d.MAC
	}
	if d.IP != "" {
		return d.IP
	}
	return d.ID
}

func deviceLess(a, b Device) bool {
	la, lb := strings.ToLower(deviceDisplayLabel(a)), strings.ToLower(deviceDisplayLabel(b))
	if la != lb {
		return la < lb
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.ID < b.ID
}

// List returns a snapshot of all known devices sorted by display name, then id.
func (r *DeviceRegistry) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[id]; !ok {
		return false
	}
	delete(r.m, id)
	return true
}

func (r *DeviceRegistry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Device, 0, len(r.m))
	for _, d := range r.m {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return deviceLess(out[i], out[j]) })
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
