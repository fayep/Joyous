package main

import (
	"context"
	"log"
	"time"
)

const samsungOvernightCheckInterval = time.Minute

// inactiveWindowStart returns the start of the inactive window containing now.
func inactiveWindowStart(now time.Time, begin, end string) (time.Time, bool) {
	if !InInactiveWindow(now, begin, end) {
		return time.Time{}, false
	}
	bh, bm, okB := parseHHMM(begin)
	eh, em, okE := parseHHMM(end)
	if !okB || !okE {
		return time.Time{}, false
	}
	loc := now.Location()
	beginToday := time.Date(now.Year(), now.Month(), now.Day(), bh, bm, 0, 0, loc)
	endToday := time.Date(now.Year(), now.Month(), now.Day(), eh, em, 0, 0, loc)
	bM := bh*60 + bm
	eM := eh*60 + em
	if bM < eM {
		return beginToday, true
	}
	if now.Before(endToday) {
		return beginToday.Add(-24 * time.Hour), true
	}
	return beginToday, true
}

func samsungOvernightDeepSleepEnabled(cfg SamsungFrameConfig) bool {
	if !InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) {
		return false
	}
	if cfg.OvernightDeepSleep != nil {
		return *cfg.OvernightDeepSleep
	}
	return true
}

// samsungRestoreNetworkStandbyOnPush reports whether a push from deep sleep should
// re-enable network standby. Outside the inactive window, restore remote wake;
// inside the window, leave standby off so post-push sleep returns to deep sleep.
func samsungRestoreNetworkStandbyOnPush(cfg SamsungFrameConfig, now time.Time) bool {
	if !cfg.DeepSleepActive {
		return false
	}
	if !InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) {
		return false
	}
	return !InInactiveWindow(now, cfg.InactiveBegin, cfg.InactiveEnd)
}

// samsungPushUSBSleepPlan decides MDC restore/wantDeep for a content push.
// On USB, restoreStandby is forced so the frame gets network-aware sleep; that does
// not clear sticky DeepSleepActive / overnight settings — only the on-frame standby bit.
func samsungPushUSBSleepPlan(cfg SamsungFrameConfig, powerSource string, now time.Time) (restoreStandby, wantDeep bool) {
	restoreStandby = samsungRestoreNetworkStandbyOnPush(cfg, now)
	onUSB := samsungOnUSBPower(powerSource)
	if onUSB {
		restoreStandby = true
	}
	insideInactive := InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) && InInactiveWindow(now, cfg.InactiveBegin, cfg.InactiveEnd)
	wantDeep = cfg.DeepSleepActive &&
		(!InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) || insideInactive) &&
		!onUSB
	return restoreStandby, wantDeep
}

func shouldTriggerOvernightDeepSleep(cfg SamsungFrameConfig, now time.Time) bool {
	if !samsungOvernightDeepSleepEnabled(cfg) {
		return false
	}
	if cfg.InactiveBegin == "" || cfg.InactiveEnd == "" || !InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) {
		return false
	}
	if cfg.DeepSleepActive {
		return false
	}
	if !InInactiveWindow(now, cfg.InactiveBegin, cfg.InactiveEnd) {
		return false
	}
	windowStart, ok := inactiveWindowStart(now, cfg.InactiveBegin, cfg.InactiveEnd)
	if !ok {
		return false
	}
	if !cfg.OvernightDeepSleepAt.IsZero() && !cfg.OvernightDeepSleepAt.Before(windowStart) {
		return false
	}
	return true
}

func (h *Hub) setSamsungDeepSleepState(frameID string, active bool, ranAt time.Time) {
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil {
		return
	}
	cfg.DeepSleepActive = active
	if !ranAt.IsZero() {
		cfg.OvernightDeepSleepAt = ranAt
	}
	_ = h.samsung.SaveConfig(cfg)
	h.syncSamsungDeepSleepDevice(frameID, active)
}

func (h *Hub) syncSamsungDeepSleepDevice(frameID string, active bool) {
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil {
		return
	}
	if !h.devices.SetSamsungDeepSleepByID(dev.ID, active) {
		return
	}
	_ = h.devices.Save()
}

func (h *Hub) runSamsungOvernightDeepSleep(frameID string) {
	cfg, err := h.samsung.LoadConfig(frameID)
	if err == nil && !shouldTriggerOvernightDeepSleep(cfg, time.Now()) {
		return
	}
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil {
		return
	}
	if samsungOnUSBPower(dev.PowerSource) {
		log.Printf("samsung overnight: skip %s — usb power", frameID)
		logOutbound("mdc overnight deep sleep skip id=%s — usb power", dev.ID)
		return
	}
	mac := h.samsungWakeMAC(frameID, dev)
	if mac == "" {
		log.Printf("samsung overnight: skip %s — wifi MAC required", frameID)
		return
	}
	reachable, err := h.ensureSamsungReachable(dev)
	if err != nil {
		log.Printf("samsung overnight deep sleep %s: %v", frameID, err)
		logOutbound("mdc overnight deep sleep fail id=%s err=%v", frameID, err)
		return
	}
	dev = reachable
	if samsungOnUSBPower(dev.PowerSource) {
		log.Printf("samsung overnight: skip %s — usb power", frameID)
		logOutbound("mdc overnight deep sleep skip id=%s — usb power", dev.ID)
		return
	}
	enteredDeep, err := EnterSamsungDeepSleep(dev.IP, dev.MDCPin, mac, h.sleepSamsungDeepDisplay)
	if err != nil {
		log.Printf("samsung overnight deep sleep %s: %v", frameID, err)
		logOutbound("mdc overnight deep sleep fail ip=%s err=%v", dev.IP, err)
		return
	}
	if !enteredDeep {
		// USB detected after wake: network sleep only; do not sticky deep-sleep.
		h.devices.NoteSamsungSlept(dev.IP, false)
		_ = h.devices.Save()
		log.Printf("samsung overnight network sleep (usb): %s", frameID)
		logOutbound("mdc overnight network sleep ok ip=%s (usb)", dev.IP)
		return
	}
	h.setSamsungDeepSleepState(frameID, true, time.Now())
	log.Printf("samsung overnight deep sleep ok: %s", frameID)
	logOutbound("mdc overnight deep sleep ok ip=%s", dev.IP)
}

func (h *Hub) clearSamsungDeepSleepAfterPush(frameID string) {
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil || !cfg.DeepSleepActive {
		return
	}
	cfg.DeepSleepActive = false
	_ = h.samsung.SaveConfig(cfg)
	h.syncSamsungDeepSleepDevice(frameID, false)
}

func (h *Hub) checkSamsungOvernightSchedules() {
	now := time.Now()
	seen := make(map[string]struct{})
	for _, d := range h.devices.List() {
		if d.Type != DeviceTypeSamsung {
			continue
		}
		frameID := SamsungFrameID(&d)
		if frameID == "" {
			continue
		}
		seen[frameID] = struct{}{}
		cfg, err := h.samsung.LoadConfig(frameID)
		if err != nil {
			continue
		}
		if shouldTriggerOvernightDeepSleep(cfg, now) {
			h.runSamsungOvernightDeepSleep(frameID)
		}
		if shouldTriggerMorningStandbyRestore(cfg, now) {
			h.runSamsungMorningStandbyRestore(frameID)
		}
	}
	for _, frameID := range mustSamsungFrameIDs(h) {
		if _, ok := seen[frameID]; ok {
			continue
		}
		cfg, err := h.samsung.LoadConfig(frameID)
		if err != nil {
			continue
		}
		if shouldTriggerOvernightDeepSleep(cfg, now) {
			h.runSamsungOvernightDeepSleep(frameID)
		}
		if shouldTriggerMorningStandbyRestore(cfg, now) {
			h.runSamsungMorningStandbyRestore(frameID)
		}
	}
}

func mustSamsungFrameIDs(h *Hub) []string {
	ids, err := h.samsung.ListFrames()
	if err != nil {
		return nil
	}
	return ids
}

func startSamsungOvernightScheduler(ctx context.Context, h *Hub) {
	ticker := time.NewTicker(samsungOvernightCheckInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.checkSamsungOvernightSchedules()
			}
		}
	}()
}
