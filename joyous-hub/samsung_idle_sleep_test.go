package main

import (
	"testing"
	"time"
)

func TestParseMDCIdleSleepPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		want    int
		wantErr bool
	}{
		{"off", []byte{0}, 0, false},
		{"5m", []byte{6}, 5, false},
		{"10m", []byte{7}, 10, false},
		{"20m", []byte{8}, 20, false},
		{"30m", []byte{9}, 30, false},
		{"60m", []byte{10}, 60, false},
		{"custom 15", []byte{240, 0, 15}, 15, false},
		{"custom 120", []byte{240, 0, 120}, 120, false},
		{"empty", nil, 0, true},
		{"bad type", []byte{3}, 0, true},
		{"custom short", []byte{240, 1}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMDCIdleSleepPayload(tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestParseMDCIdleSleepResponse(t *testing.T) {
	// AA FF 00 05 'A' C6 81 08 CS  — 20 min preset
	resp := []byte{0xAA, 0xFF, 0x00, 0x05, 'A', mdcCmdSleepTime, mdcSubCmdSleepTime, 0x08, 0}
	sum := 0
	for i := 1; i < len(resp)-1; i++ {
		sum += int(resp[i])
	}
	resp[len(resp)-1] = byte(sum & 0xFF)
	mins, err := parseMDCIdleSleepResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if mins != 20 {
		t.Fatalf("got %d want 20", mins)
	}
}

func TestIdleSleepMarkResetByLaterSend(t *testing.T) {
	ip := "192.0.2.51"
	reg := NewDeviceRegistry(t.TempDir())
	reg.UpsertSamsung(SSDPDevice{IP: ip, Server: "Samsung MDC"})
	reg.TouchSamsung(ip, "mdc_push")
	h := &Hub{devices: reg}
	lastIdleSleepMinutes.Store(ip, 20)

	// Simulate first send scheduling without a live MDC query (use cache).
	seq1 := bumpSleepAfterPushSeq(ip)
	fired1 := make(chan struct{}, 1)
	go func(seq uint64) {
		time.Sleep(40 * time.Millisecond)
		if currentSleepAfterPushSeq(ip) != seq {
			return
		}
		h.markSamsungIdleSlept(ip)
		fired1 <- struct{}{}
	}(seq1)

	// Second send resets the deadline — bump starts a new generation.
	seq2 := bumpSleepAfterPushSeq(ip)
	fired2 := make(chan struct{}, 1)
	go func(seq uint64) {
		time.Sleep(40 * time.Millisecond)
		if currentSleepAfterPushSeq(ip) != seq {
			return
		}
		h.markSamsungIdleSlept(ip)
		fired2 <- struct{}{}
	}(seq2)

	select {
	case <-fired1:
		t.Fatal("first idle mark should have been reset by second send")
	case <-time.After(80 * time.Millisecond):
	}
	select {
	case <-fired2:
	case <-time.After(80 * time.Millisecond):
		t.Fatal("second idle mark should fire after reset")
	}
	d, _ := reg.Get("samsung:" + ip)
	if d.LastAction != "mdc_sleep" {
		t.Fatalf("LastAction=%q want mdc_sleep", d.LastAction)
	}
}

