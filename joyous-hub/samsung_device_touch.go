package main

import (
	"log"
	"time"

	"joyous-hub/protocol"
)

// applySamsungDeviceTouch handles CmdDeviceTouch on samsung-bridge: learn client IP,
// update LastSeen/action, clear deep-sleep, and manage sleep-after-push around pulls.
func applySamsungDeviceTouch(hub *Hub, deviceID string, body protocol.DeviceTouchBody) {
	if body.Action == "" {
		body.Action = "hub_contact"
	}
	dev, ok := hub.devices.Get(deviceID)
	if !ok || dev.Type != DeviceTypeSamsung {
		log.Printf("samsung-bridge device.touch: device %q not found", deviceID)
		return
	}
	ip := dev.IP
	if body.ClientIP != "" {
		if hub.isHubSideCacheClientIP(body.ClientIP) {
			logOutbound("device.touch ignore client_ip=%s device=%s (hub/bridge self-probe)", body.ClientIP, deviceID)
			return
		}
		if body.ClientIP != dev.IP {
			hub.devices.UpdateSamsungIP(dev.ID, body.ClientIP)
			logOutbound("device.touch learned ip=%s was=%s device=%s", body.ClientIP, dev.IP, deviceID)
		}
		ip = body.ClientIP
	}
	if ip == "" {
		log.Printf("samsung-bridge device.touch: device %q has no IP", deviceID)
		return
	}
	switch body.Action {
	case "mdc_sleep":
		hub.devices.NoteSamsungSlept(ip, false)
	case "mdc_deep_sleep":
		hub.devices.NoteSamsungSlept(ip, true)
	default:
		hub.devices.TouchSamsung(ip, body.Action)
		if body.Action == "mdc_wake" || body.Action == "content.json" || body.Action == "png" ||
			body.Action == "mdc_push" || body.Action == "mdc_session" {
			hub.maybeClearSamsungDeepSleepOnFrameContact(SamsungFrameID(dev))
		}
		// Cancel sleep-after-push while the frame is actively pulling; png
		// means the image finished — reschedule sleep from now if configured.
		if body.Action == "content.json" || body.Action == "png" {
			bumpSleepAfterPushSeq(ip)
			if body.Action == "png" {
				rescheduleSamsungSleepAfterPull(hub, dev, ip)
			}
		}
	}
	log.Printf("samsung-bridge device.touch device=%s action=%s client_ip=%s", deviceID, body.Action, body.ClientIP)
}

// rescheduleSamsungSleepAfterPull starts sleep-after-push from image download complete
// (hub notified via device.touch action=png), so the timer does not fire mid-transfer.
func rescheduleSamsungSleepAfterPull(hub *Hub, dev *Device, ip string) {
	if hub == nil || hub.samsung == nil || ip == "" {
		return
	}
	frameID := SamsungFrameID(dev)
	cfg, err := hub.samsung.LoadConfig(frameID)
	if err != nil || !samsungAutoSleepAfterPush(cfg) {
		return
	}
	sleepAfter := samsungSleepAfterPushSec(cfg)
	seq := bumpSleepAfterPushSeq(ip)
	if sleepAfter <= 0 {
		sleepAfter = defaultSleepAfterPushSec
	}
	delay := time.Duration(sleepAfter) * time.Second
	pin := dev.MDCPin
	go func(seq uint64) {
		time.Sleep(delay)
		if currentSleepAfterPushSeq(ip) != seq {
			logOutbound("mdc sleep after pull skipped ip=%s (superseded)", ip)
			return
		}
		if sleepErr := hub.sleepSamsungDisplay(ip, pin); sleepErr != nil {
			logOutbound("mdc sleep after pull fail ip=%s err=%v", ip, sleepErr)
			return
		}
		logOutbound("mdc sleep after pull ok ip=%s", ip)
		// sleepSamsungDisplay already recorded sleep / sticky state.
	}(seq)
}
