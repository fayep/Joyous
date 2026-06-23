package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var (
	errMDCWifiMACEmpty  = errors.New("mdc wifi mac payload empty")
	errMDCWifiMACParse  = errors.New("mdc wifi mac parse failed")
)

type samsungFrameAliases struct {
	path string
	m    map[string]string // legacy frame id -> canonical MAC frame id
}

func loadSamsungFrameAliases(dir string) *samsungFrameAliases {
	a := &samsungFrameAliases{
		path: filepath.Join(dir, "aliases.json"),
		m:    make(map[string]string),
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		return a
	}
	_ = json.Unmarshal(data, &a.m)
	return a
}

func (a *samsungFrameAliases) save() error {
	if a == nil {
		return nil
	}
	b, err := json.MarshalIndent(a.m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.path, b, 0644)
}

func (a *samsungFrameAliases) canonical(frameID string) string {
	if a == nil || frameID == "" {
		return frameID
	}
	if c, ok := a.m[frameID]; ok && c != "" {
		return c
	}
	return frameID
}

func (a *samsungFrameAliases) add(alias, canonical string) {
	if a == nil || alias == "" || canonical == "" || alias == canonical {
		return
	}
	if a.m == nil {
		a.m = make(map[string]string)
	}
	a.m[alias] = canonical
}

// resolveSamsungFrameID maps legacy IP-based frame ids to the canonical MAC id when known.
func (h *Hub) resolveSamsungFrameID(frameID string) string {
	if frameID == "" {
		return frameID
	}
	if frameIDIsMAC(frameID) {
		return samsungMACFrameID(frameID)
	}
	if h.samsungAliases != nil {
		if c := h.samsungAliases.canonical(frameID); c != frameID {
			return c
		}
	}
	if dev := h.samsungDeviceByFrameID(frameID); dev != nil {
		if id := SamsungFrameID(dev); frameIDIsMAC(id) {
			return id
		}
	}
	return frameID
}

func (s *SamsungStore) migrateFrameFiles(oldID, newID string) error {
	if oldID == "" || newID == "" || oldID == newID {
		return nil
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	for _, ext := range []string{".json", ".png", ".lock"} {
		oldPath := filepath.Join(s.dir, oldID+ext)
		newPath := filepath.Join(s.dir, newID+ext)
		if _, err := os.Stat(oldPath); err != nil {
			continue
		}
		if _, err := os.Stat(newPath); err == nil {
			_ = os.Remove(oldPath)
			continue
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
	}
	cfg, err := s.LoadConfig(newID)
	if err == nil {
		cfg.FrameID = newID
		if mac, ok := normalizeSamsungMAC(newID); ok {
			cfg.WifiMAC = mac
		}
		_ = s.SaveConfig(cfg)
	}
	return nil
}

func (h *Hub) migrateSamsungFrameStore(oldFrameID, macFrameID, mac string) error {
	if oldFrameID == "" || macFrameID == "" || oldFrameID == macFrameID {
		return nil
	}
	if err := h.samsung.migrateFrameFiles(oldFrameID, macFrameID); err != nil {
		return err
	}
	if h.samsungAliases != nil {
		h.samsungAliases.add(oldFrameID, macFrameID)
		_ = h.samsungAliases.save()
	}
	if oldPush := getSamsungPushFileID(oldFrameID); oldPush != "" && getSamsungPushFileID(macFrameID) == "" {
		setSamsungPushFileID(macFrameID, oldPush)
	}
	return nil
}

func (h *Hub) applySamsungMAC(ip, mac string) {
	norm, ok := normalizeSamsungMAC(mac)
	if !ok {
		return
	}
	macFrameID := samsungMACFrameID(norm)
	macRegistryID := samsungRegistryID(norm)

	dev := h.devices.FindSamsungByIP(ip)
	if dev == nil {
		dev = h.devices.FindSamsungByMAC(norm)
	}
	var oldFrameID string
	var oldRegistryID string
	if dev != nil {
		oldFrameID = SamsungFrameID(dev)
		oldRegistryID = dev.ID
	} else {
		oldFrameID = ipToLegacyFrameID(ip)
		oldRegistryID = samsungProvisionalRegistryID(ip)
	}

	if frameIDIsMAC(oldFrameID) && oldFrameID == macFrameID {
		h.devices.SetSamsungMAC(oldRegistryID, norm)
		if ip != "" {
			h.devices.UpdateSamsungIP(oldRegistryID, ip)
			h.devices.RemoveProvisionalSamsung(ip)
		}
		_ = h.devices.Save()
		return
	}

	if existing := h.devices.FindSamsungByMAC(norm); existing != nil && existing.ID != oldRegistryID {
		oldRegistryID = existing.ID
		if oldFrameID == "" || !frameIDIsMAC(oldFrameID) {
			oldFrameID = SamsungFrameID(existing)
		}
	}

	if err := h.migrateSamsungFrameStore(oldFrameID, macFrameID, norm); err != nil {
		log.Printf("warn: migrate samsung frame %s -> %s: %v", oldFrameID, macFrameID, err)
	}

	merged := h.devices.MigrateSamsungToMAC(oldRegistryID, norm)
	if merged == nil && dev != nil {
		merged = h.devices.MigrateSamsungToMAC(dev.ID, norm)
	}
	if merged == nil {
		merged = h.devices.MigrateSamsungToMAC(samsungProvisionalRegistryID(ip), norm)
	}
	if merged != nil && ip != "" {
		h.devices.UpdateSamsungIP(merged.ID, ip)
		h.devices.RemoveProvisionalSamsung(ip)
	}

	if h.samsungBattery != nil {
		for _, oldID := range []string{oldRegistryID, samsungProvisionalRegistryID(ip)} {
			if oldID != "" && oldID != macRegistryID {
				h.samsungBattery.MigrateDeviceID(oldID, macRegistryID)
			}
		}
		_ = h.samsungBattery.Save()
	}

	cfg, err := h.samsung.LoadConfig(macFrameID)
	if err == nil {
		cfg.WifiMAC = norm
		cfg.FrameID = macFrameID
		_ = h.samsung.SaveConfig(cfg)
	}

	_ = h.devices.Save()
}

func (h *Hub) ensureSamsungMAC(ip, pin string) {
	if ip == "" {
		return
	}
	if dev := h.devices.FindSamsungByIP(ip); dev != nil {
		if _, ok := samsungDeviceMAC(dev); ok {
			return
		}
	}
	mac, err := QueryMDCWifiMAC(ip, pin)
	if err != nil || mac == "" {
		return
	}
	h.applySamsungMAC(ip, mac)
}

func (h *Hub) migrateSamsungFramesOnStartup() {
	if h.samsungAliases == nil {
		h.samsungAliases = loadSamsungFrameAliases(h.samsung.dir)
	}
	frames, err := h.samsung.ListFrames()
	if err != nil {
		log.Printf("warn: list samsung frames for migration: %v", err)
		return
	}
	for _, id := range frames {
		if frameIDIsMAC(id) {
			continue
		}
		if !frameIDLooksLikeIP(id) {
			continue
		}
		cfg, err := h.samsung.LoadConfig(id)
		if err != nil {
			continue
		}
		mac, ok := normalizeSamsungMAC(cfg.WifiMAC)
		if !ok {
			continue
		}
		macID := samsungMACFrameID(mac)
		if macID != id {
			if err := h.migrateSamsungFrameStore(id, macID, mac); err != nil {
				log.Printf("warn: startup migrate samsung frame %s -> %s: %v", id, macID, err)
			}
		}
	}
	for _, d := range h.devices.List() {
		if d.Type != DeviceTypeSamsung {
			continue
		}
		mac, ok := samsungDeviceMAC(&d)
		if !ok {
			continue
		}
		frameID := SamsungFrameID(&d)
		if frameIDIsMAC(frameID) {
			continue
		}
		macID := samsungMACFrameID(mac)
		if macID == frameID {
			continue
		}
		_ = h.migrateSamsungFrameStore(frameID, macID, mac)
		if strings.Contains(d.ID, ".") {
			h.devices.MigrateSamsungToMAC(d.ID, mac)
			_ = h.devices.Save()
		}
	}
}
