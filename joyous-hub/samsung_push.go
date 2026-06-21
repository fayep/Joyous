package main

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
)

var (
	samsungPushMu sync.RWMutex
	samsungPush   = map[string]string{} // frameID -> mobile deploy file_id (UUID)
)

func setSamsungPushFileID(frameID, fileID string) {
	samsungPushMu.Lock()
	samsungPush[frameID] = fileID
	samsungPushMu.Unlock()
}

func getSamsungPushFileID(frameID string) string {
	samsungPushMu.RLock()
	defer samsungPushMu.RUnlock()
	return samsungPush[frameID]
}

func newSamsungPushFileID() string {
	var u [16]byte
	_, _ = rand.Read(u[:])
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16]))
}
