package main

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

const (
	mdcCmdSleepTime    = 0xC6 // MDC_COMMAND_SLEEP_TIME
	mdcSubCmdSleepTime = 0x81
)

// lastIdleSleepMinutes caches the last successful MDC idle-sleep reading per frame IP
// so a transient query failure on a later send can still reset the deadline.
var lastIdleSleepMinutes sync.Map // ip -> int

// QueryMDCIdleSleepMinutes reads the frame's configured idle auto-sleep (Samsung app
// "sleep time"). Returns 0 for always-on / off. The frame must be awake (MDC reachable).
func QueryMDCIdleSleepMinutes(ip, pin string) (minutes int, err error) {
	if pin == "" {
		pin = defaultMDCPin
	}
	s, err := openMDCSession(ip, pin, mdcConnectTimeout)
	if err != nil {
		return 0, err
	}
	defer s.Close()

	pkt := mdcSubCommandQueryPacket(mdcCmdSleepTime, mdcSubCmdSleepTime)
	if err := s.transact(pkt); err != nil {
		return 0, err
	}
	s.setDeadline(mdcCommandReadTimeout)
	resp, err := s.readMDCPacket()
	if err != nil {
		return 0, fmt.Errorf("mdc sleep time read: %w", err)
	}
	minutes, err = parseMDCIdleSleepResponse(resp)
	if err != nil {
		logOutbound("mdc sleep time parse fail ip=%s resp=% x err=%v", ip, resp, err)
		return 0, err
	}
	logOutbound("mdc sleep time ok ip=%s minutes=%d", ip, minutes)
	return minutes, nil
}

func parseMDCIdleSleepResponse(resp []byte) (minutes int, err error) {
	if len(resp) < 8 {
		return 0, fmt.Errorf("mdc sleep time response too short: % x", resp)
	}
	if resp[0] != 0xAA || resp[1] != 0xFF {
		return 0, fmt.Errorf("unexpected mdc sleep time header: % x", resp)
	}
	switch resp[4] {
	case 'A':
	case 'N':
		return 0, fmt.Errorf("mdc sleep time NAK")
	default:
		return 0, fmt.Errorf("mdc sleep time ack 0x%02x", resp[4])
	}
	if resp[5] != mdcCmdSleepTime || resp[6] != mdcSubCmdSleepTime {
		return 0, fmt.Errorf("mdc sleep time cmd mismatch: % x", resp[5:7])
	}
	payload := resp[7 : len(resp)-1]
	return parseMDCIdleSleepPayload(payload)
}

// parseMDCIdleSleepPayload decodes MDCSleepTimeCommand response data (type byte + optional custom minutes).
func parseMDCIdleSleepPayload(payload []byte) (minutes int, err error) {
	if len(payload) < 1 {
		return 0, fmt.Errorf("mdc sleep time payload empty")
	}
	switch payload[0] {
	case 0:
		return 0, nil // always on
	case 6:
		return 5, nil
	case 7:
		return 10, nil
	case 8:
		return 20, nil
	case 9:
		return 30, nil
	case 10:
		return 60, nil
	case 240:
		if len(payload) < 3 {
			return 0, fmt.Errorf("mdc sleep time custom too short: % x", payload)
		}
		mins := int(binary.BigEndian.Uint16(payload[1:3]))
		if mins < 0 {
			mins = 0
		}
		return mins, nil
	default:
		return 0, fmt.Errorf("mdc sleep time type 0x%02x", payload[0])
	}
}

func resolveIdleSleepMinutes(ip, pin string) (minutes int, ok bool) {
	minutes, err := QueryMDCIdleSleepMinutes(ip, pin)
	if err == nil {
		lastIdleSleepMinutes.Store(ip, minutes)
		return minutes, true
	}
	logOutbound("mdc idle sleep query fail ip=%s err=%v", ip, err)
	if v, cached := lastIdleSleepMinutes.Load(ip); cached {
		minutes = v.(int)
		logOutbound("mdc idle sleep using cached minutes=%d ip=%s", minutes, ip)
		return minutes, true
	}
	return 0, false
}

// scheduleSamsungIdleSleepMark resets the hub's asleep deadline to N minutes from
// this send (N = frame idle sleep setting). A later send calls this again and the
// new countdown replaces the previous one. Always-on (N=0) clears any pending mark.
func (h *Hub) scheduleSamsungIdleSleepMark(ip, pin string) {
	if ip == "" {
		return
	}
	minutes, ok := resolveIdleSleepMinutes(ip, pin)
	if !ok {
		logOutbound("mdc idle sleep mark not reset ip=%s (no reading)", ip)
		return
	}
	seq := bumpSleepAfterPushSeq(ip)
	if minutes <= 0 {
		logOutbound("mdc idle sleep off (always on) ip=%s — cleared pending mark", ip)
		return
	}
	delay := time.Duration(minutes) * time.Minute
	logOutbound("mdc idle sleep mark reset ip=%s in=%s", ip, delay)
	go func(seq uint64, minutes int) {
		time.Sleep(time.Duration(minutes) * time.Minute)
		if currentSleepAfterPushSeq(ip) != seq {
			logOutbound("mdc idle sleep mark skipped ip=%s (reset by later send)", ip)
			return
		}
		h.markSamsungIdleSlept(ip)
	}(seq, minutes)
}

func (h *Hub) markSamsungIdleSlept(ip string) {
	if !h.devices.NoteSamsungSlept(ip, false) {
		return
	}
	_ = h.devices.Save()
	logOutbound("mdc idle sleep mark applied ip=%s", ip)
	if d := h.devices.FindSamsungByIP(ip); d != nil {
		h.notifySamsungBridgeContact(d.ID, "mdc_sleep")
	}
}
