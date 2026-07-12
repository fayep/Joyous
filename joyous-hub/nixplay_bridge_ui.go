//go:build nixplaybridge

package main

import (
	"bytes"
	"encoding/json"
	"net/http"

	"joyous-hub/bridgehub"
	"joyous-hub/nixplaybridge"
)

// nixplayBridgeUI serves the bridge-owned Nixplay configuration page
// (choosing which galleries to hide from the hub's Devices/Send lists),
// tunneled to the hub over MQTT the same way InkJoy's config page is.
type nixplayBridgeUI struct {
	state     *nixplayBridgeState
	hidden    *nixplaybridge.HiddenStore
	republish func()
	mux       *http.ServeMux
}

func newNixplayBridgeUI(mux *http.ServeMux, state *nixplayBridgeState, hidden *nixplaybridge.HiddenStore, republish func()) *nixplayBridgeUI {
	ui := &nixplayBridgeUI{state: state, hidden: hidden, republish: republish, mux: mux}
	ui.registerRoutes()
	return ui
}

func (ui *nixplayBridgeUI) registerRoutes() {
	ui.mux.HandleFunc("GET /nixplay/", ui.handlePage)
	ui.mux.HandleFunc("GET /nixplay", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/nixplay/", http.StatusFound)
	})
	ui.mux.HandleFunc("GET /nixplay/api/galleries", ui.handleGalleries)
	ui.mux.HandleFunc("PATCH /nixplay/api/galleries/{id}", func(w http.ResponseWriter, r *http.Request) {
		ui.handleGalleryPatch(w, r, r.PathValue("id"))
	})
}

func (ui *nixplayBridgeUI) handlePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(nixplayBridgePageHTML))
}

type nixplayGalleryJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PictureCount int    `json:"picture_count"`
	Hidden       bool   `json:"hidden"`
}

func (ui *nixplayBridgeUI) handleGalleries(w http.ResponseWriter, r *http.Request) {
	playlists, err := ui.state.nx.ListPlaylists(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]nixplayGalleryJSON, 0, len(playlists))
	for _, p := range playlists {
		out = append(out, nixplayGalleryJSON{
			ID:           p.ID,
			Name:         p.Name,
			PictureCount: p.PictureCount,
			Hidden:       ui.hidden.IsHidden(p.ID),
		})
	}
	writeNixplayJSON(w, out)
}

func (ui *nixplayBridgeUI) handleGalleryPatch(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := ui.hidden.SetHidden(id, body.Hidden); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ui.republish != nil {
		ui.republish()
	}
	writeNixplayJSON(w, map[string]any{"ok": true})
}

func writeNixplayJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ServeUIHTTP implements bridgehub.UIHTTPHandler — the hub tunnels
// /nixplay/… HTTP requests here over MQTT (bridge_routes.go's generic
// bridge proxy), and this replays them against ui.mux.
func (ui *nixplayBridgeUI) ServeUIHTTP(method, path string, headers map[string]string, body []byte) (int, string, map[string]string, []byte) {
	req, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		return http.StatusBadRequest, "text/plain", nil, []byte(err.Error())
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := &nixplayResponseRecorder{header: make(http.Header)}
	ui.mux.ServeHTTP(rr, req)
	return rr.status, rr.contentType(), rr.headerMap(), rr.body.Bytes()
}

type nixplayResponseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *nixplayResponseRecorder) Header() http.Header { return r.header }

func (r *nixplayResponseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}

func (r *nixplayResponseRecorder) WriteHeader(code int) { r.status = code }

func (r *nixplayResponseRecorder) contentType() string {
	if ct := r.header.Get("Content-Type"); ct != "" {
		return ct
	}
	return "text/plain; charset=utf-8"
}

func (r *nixplayResponseRecorder) headerMap() map[string]string {
	out := map[string]string{}
	for k := range r.header {
		out[k] = r.header.Get(k)
	}
	return out
}

var _ bridgehub.UIHTTPHandler = (*nixplayBridgeUI)(nil)
