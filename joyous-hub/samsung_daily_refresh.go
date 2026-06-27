package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	mdcSubCmdDailyRefresh      = 0xB0
	samsungMorningRestoreLead  = 10 * time.Minute // probe from this long before inactive end
	samsungMorningRestorePoll  = 5 * time.Second
)

var samsungMorningRestoreActive sync.Map // frameID -> struct{}

// QueryMDCDailyRefreshTime reads the frame's scheduled daily refresh time (HH:MM).
func QueryMDCDailyRefreshTime(ip, pin string) (hour, minute int, err error) {
	if pin == "" {
		pin = defaultMDCPin
	}
	s, err := openMDCSession(ip, pin, mdcConnectTimeout)
	if err != nil {
		return 0, 0, err
	}
	defer s.Close()

	pkt := mdcSubCommandQueryPacket(mdcCmdBattery, mdcSubCmdDailyRefresh)
	if err := s.transact(pkt); err != nil {
		return 0, 0, err
	}
	s.setDeadline(mdcCommandReadTimeout)
	resp, err := s.readMDCPacket()
	if err != nil {
		return 0, 0, fmt.Errorf("mdc daily refresh read: %w", err)
	}
	hour, minute, err = parseMDCDailyRefreshResponse(resp)
	if err != nil {
		logOutbound("mdc daily refresh parse fail ip=%s resp=% x err=%v", ip, resp, err)
		return 0, 0, err
	}
	logOutbound("mdc daily refresh ok ip=%s time=%02d:%02d", ip, hour, minute)
	return hour, minute, nil
}

// SetMDCDailyRefreshTime schedules the frame's daily e-ink refresh at hour:minute local time.
func SetMDCDailyRefreshTime(ip, pin string, hour, minute int) error {
	if pin == "" {
		pin = defaultMDCPin
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return fmt.Errorf("invalid daily refresh time %02d:%02d", hour, minute)
	}
	pkt := mdcDailyRefreshSetPacket(hour, minute)
	return transactMDCCommand(ip, pin, pkt, "daily_refresh_set", fmt.Sprintf("%02d:%02d", hour, minute))
}

func mdcDailyRefreshSetPacket(hour, minute int) []byte {
	data := []byte{mdcSubCmdDailyRefresh, 0x80, 0x02, byte(hour), byte(minute)}
	pkt := make([]byte, 0, 5+len(data))
	pkt = append(pkt, 0xAA, mdcCmdBattery, 0x00, byte(len(data)))
	pkt = append(pkt, data...)
	sum := 0
	for i := 1; i < len(pkt); i++ {
		sum += int(pkt[i])
	}
	return append(pkt, byte(sum&0xFF))
}

func parseMDCDailyRefreshResponse(resp []byte) (hour, minute int, err error) {
	if len(resp) < 7 {
		return 0, 0, fmt.Errorf("mdc daily refresh response too short: % x", resp)
	}
	if resp[0] != 0xAA || resp[1] != 0xFF {
		return 0, 0, fmt.Errorf("unexpected mdc daily refresh header: % x", resp)
	}
	switch resp[4] {
	case 'A':
	case 'N':
		return 0, 0, fmt.Errorf("mdc daily refresh NAK")
	default:
		return 0, 0, fmt.Errorf("mdc daily refresh ack 0x%02x", resp[4])
	}
	if resp[5] != mdcCmdBattery || resp[6] != mdcSubCmdDailyRefresh {
		return 0, 0, fmt.Errorf("mdc daily refresh cmd mismatch: % x", resp[5:7])
	}
	payload := resp[7 : len(resp)-1]
	if h, m, ok := parseMDCDailyRefreshPayload(payload); ok {
		return h, m, nil
	}
	return 0, 0, fmt.Errorf("mdc daily refresh payload: % x", payload)
}

func parseMDCDailyRefreshPayload(payload []byte) (hour, minute int, ok bool) {
	for i := 0; i < len(payload); i++ {
		if payload[i] != 0x81 {
			continue
		}
		if i+1 >= len(payload) {
			return 0, 0, false
		}
		n := int(payload[i+1])
		start := i + 2
		end := start + n
		if end > len(payload) || n < 2 {
			return 0, 0, false
		}
		return int(payload[start]), int(payload[start+1]), true
	}
	if len(payload) >= 2 {
		return int(payload[0]), int(payload[1]), true
	}
	return 0, 0, false
}

func hhmmFromClock(hour, minute int) string {
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func (h *Hub) querySamsungDailyRefresh(frameID string) (string, error) {
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil || dev.IP == "" {
		return "", fmt.Errorf("samsung device not found for frame %s", frameID)
	}
	hour, minute, err := QueryMDCDailyRefreshTime(dev.IP, dev.MDCPin)
	if err != nil {
		return "", err
	}
	return hhmmFromClock(hour, minute), nil
}

func (h *Hub) setSamsungDailyRefresh(frameID, hhmm string) error {
	hour, minute, ok := parseHHMM(hhmm)
	if !ok {
		return fmt.Errorf("invalid time %q", hhmm)
	}
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil || dev.IP == "" {
		return fmt.Errorf("samsung device not found for frame %s", frameID)
	}
	if err := SetMDCDailyRefreshTime(dev.IP, dev.MDCPin, hour, minute); err != nil {
		return err
	}
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil {
		return err
	}
	cfg.DailyRefreshTime = hhmmFromClock(hour, minute)
	return h.samsung.SaveConfig(cfg)
}

func (h *Hub) syncSamsungDailyRefreshToInactiveEnd(frameID string) (string, error) {
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil {
		return "", err
	}
	if !InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) {
		return "", fmt.Errorf("inactive schedule not configured")
	}
	if err := h.setSamsungDailyRefresh(frameID, cfg.InactiveEnd); err != nil {
		return "", err
	}
	return cfg.InactiveEnd, nil
}

func inactiveEndToday(now time.Time, end string) (time.Time, bool) {
	eh, em, ok := parseHHMM(end)
	if !ok {
		return time.Time{}, false
	}
	loc := now.Location()
	endAt := time.Date(now.Year(), now.Month(), now.Day(), eh, em, 0, 0, loc)
	return endAt, true
}

// inactiveEndForMorning returns the inactive-end instant for the current wake window.
// Unlike grace/idempotency helpers, this keeps today's upcoming end before it passes
// (e.g. 06:59 → 07:00 today, not yesterday).
func inactiveEndForMorning(now time.Time, end string) (time.Time, bool) {
	return inactiveEndToday(now, end)
}

func inMorningRestoreWindow(now time.Time, begin, end string) bool {
	if !InactiveScheduleEnabled(begin, end) {
		return false
	}
	endAt, ok := inactiveEndForMorning(now, end)
	if !ok {
		return false
	}
	windowStart := endAt.Add(-samsungMorningRestoreLead)
	return !now.Before(windowStart) && !now.After(endAt)
}

func morningRestoreDeadline(now time.Time, end string) time.Time {
	endAt, ok := inactiveEndForMorning(now, end)
	if !ok {
		return now
	}
	return endAt
}

func shouldTriggerMorningStandbyRestore(cfg SamsungFrameConfig, now time.Time) bool {
	if !cfg.DeepSleepActive {
		return false
	}
	if !InactiveScheduleEnabled(cfg.InactiveBegin, cfg.InactiveEnd) {
		return false
	}
	if !inMorningRestoreWindow(now, cfg.InactiveBegin, cfg.InactiveEnd) {
		return false
	}
	endAt, ok := inactiveEndForMorning(now, cfg.InactiveEnd)
	if !ok {
		return false
	}
	windowStart := endAt.Add(-samsungMorningRestoreLead)
	if !cfg.MorningStandbyRestoredAt.IsZero() && !cfg.MorningStandbyRestoredAt.Before(windowStart) {
		return false
	}
	return true
}

func (h *Hub) setMorningStandbyRestored(frameID string, at time.Time) {
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil {
		return
	}
	cfg.MorningStandbyRestoredAt = at
	_ = h.samsung.SaveConfig(cfg)
}

func (h *Hub) runSamsungMorningStandbyRestore(frameID string) {
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil || !shouldTriggerMorningStandbyRestore(cfg, time.Now()) {
		return
	}
	if _, loaded := samsungMorningRestoreActive.LoadOrStore(frameID, struct{}{}); loaded {
		return
	}
	go func() {
		defer samsungMorningRestoreActive.Delete(frameID)
		h.morningStandbyRestoreLoop(frameID)
	}()
}

func (h *Hub) morningStandbyRestoreLoop(frameID string) {
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil || dev.IP == "" {
		return
	}
	ip, pin := dev.IP, dev.MDCPin
	cfg, err := h.samsung.LoadConfig(frameID)
	if err != nil {
		return
	}
	deadline := morningRestoreDeadline(time.Now(), cfg.InactiveEnd)
	logOutbound("samsung morning standby restore start ip=%s frame=%s until=%s", ip, frameID, deadline.Format(time.RFC3339))

	attempt := func() bool {
		if !mdcSessionOK(ip, pin) {
			return false
		}
		if err := SendMDCNetworkStandby(ip, pin, true); err != nil {
			logOutbound("samsung morning standby restore network standby fail ip=%s err=%v", ip, err)
			return false
		}
		h.clearSamsungDeepSleepAfterPush(frameID)
		h.setMorningStandbyRestored(frameID, time.Now())
		log.Printf("samsung morning standby restore ok: %s", frameID)
		logOutbound("samsung morning standby restore ok ip=%s", ip)
		return true
	}

	if attempt() {
		return
	}

	ticker := time.NewTicker(samsungMorningRestorePoll)
	defer ticker.Stop()
	for {
		if time.Now().After(deadline) {
			log.Printf("samsung morning standby restore: frame %s not reachable before %s", frameID, deadline.Format("15:04"))
			logOutbound("samsung morning standby restore gave up ip=%s", ip)
			return
		}
		cfg, err := h.samsung.LoadConfig(frameID)
		if err != nil || !cfg.DeepSleepActive {
			return
		}
		<-ticker.C
		if attempt() {
			return
		}
	}
}

func (h *Hub) handleSamsungDailyRefreshGet(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	cfg, _ := h.samsung.LoadConfig(frameID)
	cached := cfg.DailyRefreshTime
	queried, err := h.querySamsungDailyRefresh(frameID)
	if err == nil {
		cached = queried
		cfg.DailyRefreshTime = queried
		_ = h.samsung.SaveConfig(cfg)
	}
	out := map[string]any{
		"daily_refresh_time": cached,
		"inactive_end":       cfg.InactiveEnd,
	}
	if err != nil {
		out["query_error"] = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Hub) handleSamsungDailyRefreshPut(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	var body struct {
		Time string `json:"time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Time == "" {
		http.Error(w, "time required (HH:MM)", http.StatusBadRequest)
		return
	}
	if err := h.setSamsungDailyRefresh(frameID, body.Time); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "daily_refresh_time": body.Time})
}

func (h *Hub) handleSamsungDailyRefreshSyncInactive(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	t, err := h.syncSamsungDailyRefreshToInactiveEnd(frameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "daily_refresh_time": t, "synced_to": "inactive_end"})
}
