package inkjoybridge

import (
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
