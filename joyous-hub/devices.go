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
	LastOverlayHash   string    `json:"last_overlay_hash,omitempty"`
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

func samsungID(ip string) string { return samsungProvisionalRegistryID(ip) }

func (r *DeviceRegistry) findSamsungByIPLocked(ip string) *Device {
	if ip == "" {
		return nil
	}
	if d, ok := r.m[samsungProvisionalRegistryID(ip)]; ok {
		return d
	}
	for _, d := range r.m {
		if d.Type == DeviceTypeSamsung && d.IP == ip {
			return d
		}
	}
	return nil
}

// FindSamsungByIP returns a Samsung device currently at the given IP.
func (r *DeviceRegistry) FindSamsungByIP(ip string) *Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d := r.findSamsungByIPLocked(ip)
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

// FindSamsungByMAC returns a Samsung device with the given WiFi MAC.
func (r *DeviceRegistry) FindSamsungByMAC(mac string) *Device {
	norm, ok := normalizeSamsungMAC(mac)
	if !ok {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if d, ok := r.m[samsungRegistryID(norm)]; ok {
		cp := *d
		return &cp
	}
	for _, d := range r.m {
		if d.Type != DeviceTypeSamsung {
			continue
		}
		if m, ok := samsungDeviceMAC(d); ok && m == norm {
			cp := *d
			return &cp
		}
	}
	return nil
}

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
	if d.LastAction == "mdc_sleep" || d.LastAction == "mdc_deep_sleep" {
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
func (r *DeviceRegistry) SetLastImage(deviceID, imageID, overlayHash string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[deviceID]
	if !ok {
		return
	}
	d.LastImageID = imageID
	d.LastOverlayHash = overlayHash
	d.DisplayPreviewAt = time.Time{}
}

// SetDisplayPreview marks an externally fetched preview as current (clears hub album thumb).
func (r *DeviceRegistry) SetDisplayPreview(mac string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.getOrCreateInkJoy(mac)
	d.DisplayPreviewAt = time.Now()
	d.LastImageID = ""
	d.LastOverlayHash = ""
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
	d := r.findSamsungByIPLocked(found.IP)
	if d == nil {
		id := samsungProvisionalRegistryID(found.IP)
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

// SetSamsungMAC stores the canonical WiFi MAC on a Samsung device.
func (r *DeviceRegistry) SetSamsungMAC(id, mac string) bool {
	norm, ok := normalizeSamsungMAC(mac)
	if !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	d.MDCMAC = norm
	d.MAC = norm
	return true
}

// UpdateSamsungIP updates the reachable IP for a Samsung device.
func (r *DeviceRegistry) UpdateSamsungIP(id, ip string) bool {
	if ip == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok || d.Type != DeviceTypeSamsung {
		return false
	}
	d.IP = ip
	return true
}

// RemoveProvisionalSamsung drops an IP-keyed registry entry without a learned MAC.
func (r *DeviceRegistry) RemoveProvisionalSamsung(ip string) {
	if ip == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pid := samsungProvisionalRegistryID(ip)
	d, ok := r.m[pid]
	if !ok {
		return
	}
	if _, hasMAC := samsungDeviceMAC(d); !hasMAC {
		delete(r.m, pid)
	}
}

// MigrateSamsungToMAC re-keys a Samsung device from IP-based id to MAC-based id.
func (r *DeviceRegistry) MigrateSamsungToMAC(oldID, mac string) *Device {
	norm, ok := normalizeSamsungMAC(mac)
	if !ok {
		return nil
	}
	newID := samsungRegistryID(norm)
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.m[oldID]
	if old == nil {
		if ip := strings.TrimPrefix(oldID, "samsung:"); strings.Contains(ip, ".") {
			old = r.findSamsungByIPLocked(ip)
		}
	}
	if old == nil {
		for _, d := range r.m {
			if d.Type == DeviceTypeSamsung {
				if m, ok := samsungDeviceMAC(d); ok && m == norm {
					old = d
					break
				}
			}
		}
	}
	if old == nil {
		return nil
	}

	if oldID == newID || old.ID == newID {
		old.MDCMAC = norm
		old.MAC = norm
		return old
	}

	var merged Device
	if existing, ok := r.m[newID]; ok {
		merged = *existing
	} else {
		merged = *old
	}
	if merged.IP == "" {
		merged.IP = old.IP
	}
	if merged.Name == "" {
		merged.Name = old.Name
	}
	if merged.MDCPin == "" {
		merged.MDCPin = old.MDCPin
	}
	if merged.USN == "" {
		merged.USN = old.USN
	}
	if merged.Location == "" {
		merged.Location = old.Location
	}
	if merged.LastSeen.Before(old.LastSeen) {
		merged.LastSeen = old.LastSeen
	}
	if merged.Battery == 0 && old.Battery > 0 {
		merged.Battery = old.Battery
		merged.PowerSource = old.PowerSource
	}
	merged.ID = newID
	merged.Type = DeviceTypeSamsung
	merged.MDCMAC = norm
	merged.MAC = norm

	delete(r.m, old.ID)
	if oldID != old.ID {
		delete(r.m, oldID)
	}
	r.m[newID] = &merged
	return &merged
}

// SetMDCMAC stores the WiFi MAC used for Samsung WoL / magic wake.
func (r *DeviceRegistry) SetMDCMAC(id, mac string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[id]
	if !ok {
		return false
	}
	if norm, ok := normalizeSamsungMAC(mac); ok {
		d.MDCMAC = norm
		d.MAC = norm
	} else {
		d.MDCMAC = strings.TrimSpace(mac)
	}
	return true
}

// NoteSamsungSlept records a successful sleep command without marking the frame awake.
// deep=true means overnight deep sleep (network standby off; button wake required).
func (r *DeviceRegistry) NoteSamsungSlept(ip string, deep bool) bool {
	if ip == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.findSamsungByIPLocked(ip)
	if d == nil {
		return false
	}
	if deep {
		d.LastAction = "mdc_deep_sleep"
		d.DeepSleepActive = true
	} else {
		d.LastAction = "mdc_sleep"
		d.DeepSleepActive = false
	}
	return true
}

// SetSamsungDeepSleep records hub-initiated overnight deep sleep on the device registry.
func (r *DeviceRegistry) SetSamsungDeepSleep(ip string, active bool) bool {
	if ip == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.findSamsungByIPLocked(ip)
	if d == nil {
		return false
	}
	d.DeepSleepActive = active
	if !active && d.LastAction == "mdc_deep_sleep" {
		d.LastAction = "mdc_sleep"
	}
	return true
}

// TouchSamsung records hub contact from a Samsung frame (HTTP poll, PNG fetch, MDC probe, etc.).
func (r *DeviceRegistry) TouchSamsung(ip, action string) bool {
	if ip == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.findSamsungByIPLocked(ip)
	if d == nil {
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
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.findSamsungByIPLocked(ip)
	if d == nil {
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
			suffix := strings.TrimPrefix(d.ID, "samsung:")
			if d.IP == "" {
				if mac, ok := normalizeSamsungMAC(suffix); ok {
					d.MDCMAC = mac
					d.MAC = mac
				} else if strings.Contains(suffix, ".") {
					d.IP = suffix
				}
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

// SamsungFrameID returns the samsung store key for a device (MAC when known, else legacy IP dash form).
func SamsungFrameID(dev *Device) string {
	if mac, ok := samsungDeviceMAC(dev); ok {
		return samsungMACFrameID(mac)
	}
	if dev.IP != "" {
		return ipToLegacyFrameID(dev.IP)
	}
	return strings.TrimPrefix(dev.ID, "samsung:")
}

func frameIDToIP(frameID string) string {
	if strings.Count(frameID, "-") == 3 && !strings.Contains(frameID, "/") {
		return strings.ReplaceAll(frameID, "-", ".")
	}
	return ""
}
