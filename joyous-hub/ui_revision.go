package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"joyous-hub/internal/linkmeta"
)

const uiRevisionPlaceholder = "__JOYOUS_UI_REVISION__"

var uiRevision string

func init() {
	sum := sha256.Sum256([]byte(linkmeta.Version + "\x00" + indexHTML))
	uiRevision = hex.EncodeToString(sum[:])[:12]
}

func uiRevisionHTML() string {
	return strings.Replace(indexHTML, uiRevisionPlaceholder, uiRevision, 1)
}

func (h *Hub) handleUIRevision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(map[string]string{"revision": uiRevision})
}
