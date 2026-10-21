package inkjoybridge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// Ack result constants (frame → broker, data.result).
const (
	AckInterrupted = 104
	AckAccepted    = 106
	AckComplete    = 113
)

// BuildAckPayload builds a frame→cloud ack matching real frame shape.
func BuildAckPayload(mac, ackAction, ackMsgid string, data map[string]any) []byte {
	if data == nil {
		data = map[string]any{}
	}
	if ackMsgid != "" {
		data["ack_msgid"] = ackMsgid
	}
	if _, ok := data["result"]; !ok {
		data["result"] = AckAccepted
	}
	msg := map[string]any{
		"action":   ackAction,
		"clientid": mac,
		"msgid":    fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac":   mac,
		"data":     data,
	}
	b, _ := json.Marshal(msg)
	return b
}

func otaAckAction(interceptedAction string) string {
	if interceptedAction == "fpga" {
		return "fpga_ota_ack"
	}
	return "ota_ack"
}

// BuildBlockedOTAAcks returns synthetic ack payloads when ota/fpga is intercepted.
func BuildBlockedOTAAcks(mac, interceptedAction, ackMsgid string) [][]byte {
	ackAction := otaAckAction(interceptedAction)
	return [][]byte{
		BuildAckPayload(mac, ackAction, ackMsgid, map[string]any{"result": AckAccepted}),
		BuildAckPayload(mac, ackAction, ackMsgid, map[string]any{"result": AckInterrupted}),
	}
}

// AckResultLabel returns a short label for logging/UI.
func AckResultLabel(result int) string {
	switch result {
	case AckInterrupted:
		return "interrupted"
	case AckAccepted:
		return "accepted"
	case AckComplete:
		return "complete"
	default:
		return fmt.Sprintf("unknown (%d)", result)
	}
}

// BuildBrokerAckPayload builds a broker→frame ack (login_ack / heart_ack shape).
func BuildBrokerAckPayload(mac, action, ackMsgid string, data map[string]any) []byte {
	if data == nil {
		data = map[string]any{}
	}
	if ackMsgid != "" {
		data["ack_msgid"] = ackMsgid
	}
	msg := map[string]any{
		"action": action,
		"msgid":  fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac": mac,
		"data":   data,
	}
	b, _ := json.Marshal(msg)
	return b
}

// localOnlySessionIDs returns stable synthetic device_id/uid for Seeed frames
// (real cloud values aren't available when we keep the session local).
func localOnlySessionIDs(mac string) (deviceID, uid string) {
	sum := sha256.Sum256([]byte("joyous-local-only:" + mac))
	deviceID = fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	uid = fmt.Sprintf("%x-%x-%x-%x-%x", sum[16:20], sum[20:22], sum[22:24], sum[24:26], sum[26:32])
	return deviceID, uid
}

// BuildLoginAck builds a broker→frame login_ack for a local-only session.
func BuildLoginAck(mac, ackMsgid string) []byte {
	deviceID, uid := localOnlySessionIDs(mac)
	return BuildBrokerAckPayload(mac, "login_ack", ackMsgid, map[string]any{
		"device_id": deviceID,
		"uid":       uid,
	})
}

// BuildHeartAck builds a broker→frame heart_ack.
func BuildHeartAck(mac, ackMsgid string) []byte {
	return BuildBrokerAckPayload(mac, "heart_ack", ackMsgid, nil)
}
