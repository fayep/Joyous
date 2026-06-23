package main

import (
	"encoding/hex"
	"regexp"
	"strings"
)

var samsungMACPattern = regexp.MustCompile(`^[0-9A-F]{12}$`)

// normalizeSamsungMAC returns a 12-character uppercase hex WiFi MAC (InkJoy-style, no separators).
func normalizeSamsungMAC(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	s = strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(s)
	if len(s) != 12 {
		return "", false
	}
	s = strings.ToUpper(s)
	if !samsungMACPattern.MatchString(s) {
		return "", false
	}
	return s, true
}

func frameIDIsMAC(frameID string) bool {
	_, ok := normalizeSamsungMAC(frameID)
	return ok
}

func frameIDLooksLikeIP(frameID string) bool {
	return strings.Count(frameID, "-") == 3 && !strings.Contains(frameID, "/")
}

func samsungMACFrameID(mac string) string {
	m, ok := normalizeSamsungMAC(mac)
	if !ok {
		return ""
	}
	return m
}

func samsungRegistryID(mac string) string {
	m, ok := normalizeSamsungMAC(mac)
	if !ok {
		return ""
	}
	return "samsung:" + m
}

func samsungProvisionalRegistryID(ip string) string {
	return "samsung:" + ip
}

func samsungDeviceMAC(dev *Device) (string, bool) {
	if dev == nil {
		return "", false
	}
	for _, s := range []string{dev.MDCMAC, dev.MAC} {
		if mac, ok := normalizeSamsungMAC(s); ok {
			return mac, true
		}
	}
	return "", false
}

func samsungDeviceRegistryID(dev *Device) string {
	if dev == nil {
		return ""
	}
	if mac, ok := samsungDeviceMAC(dev); ok {
		return samsungRegistryID(mac)
	}
	if dev.IP != "" {
		return samsungProvisionalRegistryID(dev.IP)
	}
	return dev.ID
}

func ipToLegacyFrameID(ip string) string {
	if ip == "" {
		return ""
	}
	return strings.ReplaceAll(ip, ".", "-")
}

func parseMDCWifiMACPayload(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", errMDCWifiMACEmpty
	}
	if len(payload) == 6 {
		return strings.ToUpper(hex.EncodeToString(payload)), nil
	}
	var sb strings.Builder
	for i := 0; i+1 < len(payload); i += 2 {
		if sb.Len() > 0 {
			sb.WriteByte(':')
		}
		sb.WriteByte(payload[i])
		sb.WriteByte(payload[i+1])
	}
	mac, ok := normalizeSamsungMAC(sb.String())
	if !ok {
		return "", errMDCWifiMACParse
	}
	return mac, nil
}
