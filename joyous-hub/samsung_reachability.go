package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	samsungReachabilityDiscoverAttempts = 3
	samsungReachabilityDiscoverDelay    = 2 * time.Second
)

// Hooks for tests — production uses real MDC/SSDP.
var (
	samsungProbeIP       = probeMDCBanner
	samsungClassifyIP    = classifyMDCTarget
	samsungDiscoverLAN   = DiscoverPhotoFrames
	samsungQueryMAC      = QueryMDCWifiMAC
	samsungDiscoverSleep = time.Sleep
)

// ingestDiscoveredSamsung merges one LAN hit into the registry by Wi‑Fi MAC when
// possible. A new DHCP lease must update the MAC-keyed device, not create an
// orphan samsung:<ip> provisional.
func (h *Hub) ingestDiscoveredSamsung(found SSDPDevice) *Device {
	if found.IP == "" {
		return nil
	}
	pin := ""
	if existing := h.devices.FindSamsungByIP(found.IP); existing != nil {
		pin = existing.MDCPin
	}
	mac, err := samsungQueryMAC(found.IP, pin)
	if err != nil {
		logOutbound("discover mac query fail ip=%s err=%v", found.IP, err)
	} else if mac != "" {
		logOutbound("discover mac ok ip=%s mac=%s", found.IP, mac)
		if known := h.devices.FindSamsungByMAC(mac); known != nil {
			h.devices.UpdateSamsungIP(known.ID, found.IP)
			h.devices.RemoveProvisionalSamsung(found.IP)
			if pin == "" {
				pin = known.MDCPin
			}
			h.applySamsungMAC(found.IP, mac)
			if d := h.devices.FindSamsungByMAC(mac); d != nil {
				logOutbound("discover merged type=samsung id=%s ip=%s mac=%s", d.ID, d.IP, mac)
				return d
			}
		}
		h.applySamsungMAC(found.IP, mac)
		if d := h.devices.FindSamsungByMAC(mac); d != nil {
			logOutbound("discover registered type=samsung id=%s ip=%s mac=%s", d.ID, d.IP, mac)
			return d
		}
	}
	d := h.devices.UpsertSamsung(found)
	logOutbound("discover found type=samsung id=%s ip=%s", d.ID, d.IP)
	go h.ensureSamsungMAC(found.IP, pin)
	return d
}

// ensureSamsungReachable returns a device with a live MDC IP.
//
//	live     — reuse stored IP
//	absent   — host down / asleep: do not rediscover; caller should wake (or ask for button)
//	foreign  — something else has this IP: LAN rediscovery by Wi‑Fi MAC
func (h *Hub) ensureSamsungReachable(dev *Device) (*Device, error) {
	if dev == nil || dev.Type != DeviceTypeSamsung {
		return nil, fmt.Errorf("samsung device required")
	}
	wantMAC, haveMAC := samsungDeviceMAC(dev)
	id := dev.ID

	if dev.IP != "" {
		switch samsungClassifyIP(dev.IP, 800*time.Millisecond) {
		case mdcTargetLive:
			logOutbound("reachability ok id=%s ip=%s (probe)", id, dev.IP)
			return dev, nil
		case mdcTargetAbsent:
			logOutbound("reachability host down id=%s ip=%s — need wake, not rediscover", id, dev.IP)
			return nil, fmt.Errorf("%w at %s", errMDCHostDown, dev.IP)
		case mdcTargetForeign:
			logOutbound("reachability foreign id=%s ip=%s — not MDC; rediscovering", id, dev.IP)
		}
	} else {
		logOutbound("reachability start id=%s ip=(none) — discovering", id)
	}

	var lastErr error
	for attempt := 1; attempt <= samsungReachabilityDiscoverAttempts; attempt++ {
		if attempt > 1 {
			logOutbound("reachability discover retry id=%s attempt=%d/%d", id, attempt, samsungReachabilityDiscoverAttempts)
			samsungDiscoverSleep(samsungReachabilityDiscoverDelay)
		}
		logOutbound("reachability discover begin id=%s attempt=%d/%d subnets=%v", id, attempt, samsungReachabilityDiscoverAttempts, discoverSubnets)

		frames, ssdpSeen, err := samsungDiscoverLAN(0)
		if err != nil {
			logOutbound("reachability discover fail id=%s attempt=%d err=%v", id, attempt, err)
			lastErr = fmt.Errorf("discover: %w", err)
			continue
		}
		logOutbound("reachability discover done id=%s attempt=%d ssdp=%d frames=%d", id, attempt, ssdpSeen, len(frames))

		matched := h.matchDiscoveredSamsung(id, wantMAC, haveMAC, frames)
		if matched == nil || matched.IP == "" {
			logOutbound("reachability discover no match id=%s attempt=%d mac=%v", id, attempt, haveMAC)
			lastErr = fmt.Errorf("frame not found on LAN (rediscover failed)")
			continue
		}
		switch samsungClassifyIP(matched.IP, 800*time.Millisecond) {
		case mdcTargetLive:
			log.Printf("samsung reachable id=%s ip=%s", matched.ID, matched.IP)
			logOutbound("reachability ok id=%s ip=%s (rediscover attempt=%d)", matched.ID, matched.IP, attempt)
			return matched, nil
		case mdcTargetAbsent:
			logOutbound("reachability discover match host down id=%s ip=%s attempt=%d", matched.ID, matched.IP, attempt)
			lastErr = fmt.Errorf("%w at %s", errMDCHostDown, matched.IP)
		default:
			logOutbound("reachability discover match not MDC id=%s ip=%s attempt=%d", matched.ID, matched.IP, attempt)
			lastErr = fmt.Errorf("%w at %s", errMDCForeignHost, matched.IP)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("frame not found on LAN (rediscover failed)")
	}
	return nil, lastErr
}

func (h *Hub) matchDiscoveredSamsung(id, wantMAC string, haveMAC bool, frames []SSDPDevice) *Device {
	var matched *Device
	for _, sd := range frames {
		d := h.ingestDiscoveredSamsung(sd)
		if d == nil {
			continue
		}
		if d.ID == id {
			matched = d
			break
		}
		if haveMAC {
			if m, ok := samsungDeviceMAC(d); ok && m == wantMAC {
				matched = d
				break
			}
		}
	}
	if matched == nil {
		// Re-read registry in case ingest updated our id under lock elsewhere.
		if d, ok := h.devices.Get(id); ok && d.Type == DeviceTypeSamsung && d.IP != "" {
			if samsungProbeIP(d.IP) {
				matched = d
			}
		}
	}
	return matched
}

// ensureSamsungWakeTarget returns a device with an IP suitable for WoL / magic wake.
// Unlike ensureSamsungReachable, a stored IP (or LastIP) is kept even when MDC is
// down — that is the normal network-sleep case wake is meant to recover from.
func (h *Hub) ensureSamsungWakeTarget(dev *Device) (*Device, error) {
	if dev == nil || dev.Type != DeviceTypeSamsung {
		return nil, fmt.Errorf("samsung device required")
	}
	ip := strings.TrimSpace(dev.IP)
	if ip == "" {
		ip = strings.TrimSpace(dev.LastIP)
	}
	if ip != "" {
		if strings.TrimSpace(dev.IP) == "" {
			h.devices.UpdateSamsungIP(dev.ID, ip)
			if d, ok := h.devices.Get(dev.ID); ok {
				dev = d
			} else {
				cp := *dev
				cp.IP = ip
				dev = &cp
			}
			logOutbound("wake target id=%s ip=%s (from last_ip)", dev.ID, ip)
		} else {
			logOutbound("wake target id=%s ip=%s (last-known)", dev.ID, ip)
		}
		return dev, nil
	}
	logOutbound("wake target id=%s ip=(none) — rediscovering", dev.ID)
	return h.ensureSamsungReachable(dev)
}
