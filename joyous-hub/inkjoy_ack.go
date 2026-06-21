package main

import "fmt"

// InkJoy MQTT ack result encoding (frame → broker, data.result).
//
// Results are a bitfield, not arbitrary sequential ints. Confirmed on v0.5.6
// captures and by the InkJoy Android app (PhotoViewModel.handleMqttMessage):
//   - 104 — play interrupted (106 − bit 2); same ack_msgid, frame drops soon after
//   - 106 — command accepted / work started
//   - 182, 184, 186, 188 — download 20%, 40%, 60%, 80%
//   - 113 — finished OK
//
// Low-byte bit layout:
//
//	7  128  progress phase (play/ota download streaming)
//	6   64  base (present in all observed acks)
//	5   32  base
//	4   16  complete phase
//	3    8  accepted / work queued
//	2    4  set during progress steps
//	1    2  accepted / work queued
//	0    1  success / done
//
// Common composites:
//
//	104 = 64+32+8     interrupted — 106 with bit 2 cleared; play aborted before complete
//	106 = 64+32+8+2   accepted / work started
//	113 = 64+32+16+1  finished OK (play/ota/image_refresh complete)
//	182…188 (step 2)  download progress at 20% intervals when bit 128 is set
//	                  (progress drops bit 64; adds 128 — see inkjoyProgressFirst)
//
// Accepted → complete clears 8+2 and sets 16+1: 106 + 16 + 1 − 8 − 2 = 113.
// Failed play: 106 (started) may be followed by 104 (106 − 2) on the same ack_msgid
// when the frame disconnects without finishing (no progress stream, no 113).
//
// Hub-blocked OTA/fpga uses the same 106 → 104 pair via buildBlockedOTAAcks (not forwarded).
//
// One-shot command acks (wifi_sleep, mqtt_config) only need 106. Long-running
// work (play, ota) sends 106, then 182+ progress codes, then 113. Some older
// notes mention 255 (0xFF) as done; current app and captures use 113.
const (
	inkjoyResBitProgress  = 1 << 7 // 128 — progress streaming phase
	inkjoyResBitBase6     = 1 << 6 // 64
	inkjoyResBitBase5     = 1 << 5 // 32
	inkjoyResBitComplete  = 1 << 4 // 16
	inkjoyResBitQueued    = 1 << 3 // 8
	inkjoyResBitProg4     = 1 << 2 // 4
	inkjoyResBitAccepted  = 1 << 1 // 2
	inkjoyResBitOK        = 1 << 0 // 1

	inkjoyResBase     = inkjoyResBitBase6 | inkjoyResBitBase5                  // 96
	inkjoyResQueued   = inkjoyResBitQueued                                               // 8
	inkjoyResAccepted = inkjoyResBitQueued | inkjoyResBitAccepted                        // 10
	inkjoyResDone     = inkjoyResBitComplete | inkjoyResBitOK                            // 17

	// 104 = 106 − 2: play started then aborted. Observed as a second play_ack on the
	// same ack_msgid shortly before MQTT disconnect (battery/sleep/crash — no 113).
	inkjoyAckInterrupted = inkjoyResBase | inkjoyResQueued // 104 = inkjoyAckAccepted - inkjoyResBitAccepted
	inkjoyAckAccepted    = inkjoyResBase | inkjoyResAccepted // 106
	inkjoyAckComplete = inkjoyResBase | inkjoyResDone     // 113

	// Progress codes (play/ota download). Low bits step by 2; Android maps
	// 182/184/186/188 → 20/40/60/80%. Bit 64 (0x40) is clear; bit 128 is set.
	inkjoyProgressFirst   = 182 // 128+32+16+4+2
	inkjoyProgressLast    = 188 // 128+32+16+8
	inkjoyProgressStep    = 2
	inkjoyProgressPctStep = 20
)

// inkjoyProgressPercent decodes download % from a progress-phase result (182–188).
func inkjoyProgressPercent(result int) (percent int, ok bool) {
	if result < inkjoyProgressFirst || result > inkjoyProgressLast {
		return 0, false
	}
	if (result-inkjoyProgressFirst)%inkjoyProgressStep != 0 {
		return 0, false
	}
	step := (result - inkjoyProgressFirst) / inkjoyProgressStep
	return inkjoyProgressPctStep + step*inkjoyProgressPctStep, true
}

// inkjoyProgressResult encodes download progress for synthetic play_ack/ota_ack
// replies. Values below 20% map to accepted (106); 100%+ maps to complete (113).
func inkjoyProgressResult(percent int) int {
	switch {
	case percent < inkjoyProgressPctStep:
		return inkjoyAckAccepted
	case percent >= 100:
		return inkjoyAckComplete
	default:
		step := (percent / inkjoyProgressPctStep) - 1
		return inkjoyProgressFirst + step*inkjoyProgressStep
	}
}

// inkjoyIsProgressResult reports whether result is a download progress code.
func inkjoyIsProgressResult(result int) bool {
	_, ok := inkjoyProgressPercent(result)
	return ok
}

// inkjoyOTAAckAction maps an intercepted cloud→frame OTA push to its ack action.
func inkjoyOTAAckAction(interceptedAction string) string {
	if interceptedAction == "fpga" {
		return "fpga_ota_ack"
	}
	return "ota_ack"
}

// buildBlockedOTAAcks returns synthetic ack payloads when the hub intercepts ota/fpga
// without forwarding to the frame: 106 (accepted) then 104 (interrupted), matching
// the play-abort pattern seen before disconnect.
func buildBlockedOTAAcks(mac, interceptedAction, ackMsgid string) [][]byte {
	ackAction := inkjoyOTAAckAction(interceptedAction)
	return [][]byte{
		buildAckPayloadFor(mac, ackAction, ackMsgid, map[string]any{"result": inkjoyAckAccepted}),
		buildAckPayloadFor(mac, ackAction, ackMsgid, map[string]any{"result": inkjoyAckInterrupted}),
	}
}

// inkjoyAckResultLabel returns a short label for logging/UI (best-effort).
func inkjoyAckResultLabel(result int) string {
	switch result {
	case inkjoyAckInterrupted:
		return "interrupted"
	case inkjoyAckAccepted:
		return "accepted"
	case inkjoyAckComplete:
		return "complete"
	default:
		if pct, ok := inkjoyProgressPercent(result); ok {
			return fmt.Sprintf("progress %d%%", pct)
		}
		return fmt.Sprintf("unknown (%d)", result)
	}
}
