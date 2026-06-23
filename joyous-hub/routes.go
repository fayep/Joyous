package main

import (
	"net/http"
	"strings"
)

func registerRoutes(mux *http.ServeMux, hub *Hub) {
	mux.HandleFunc("POST /api/inkjoy/ble/scan", hub.handleBLEScan)
	mux.HandleFunc("POST /api/inkjoy/ble/adopt", hub.handleBLEAdopt)
	mux.HandleFunc("GET /api/devices", hub.handleDevices)
	mux.HandleFunc("PATCH /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleDevicePatch(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/devices/{id}/display-preview", func(w http.ResponseWriter, r *http.Request) {
		hub.handleDeviceDisplayPreview(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleDeviceDelete(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /api/devices/discover", hub.handleDiscover)
	mux.HandleFunc("GET /api/images", hub.handleImages)
	mux.HandleFunc("POST /api/images", hub.handleImageUpload)
	mux.HandleFunc("DELETE /api/images/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleImageDelete(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		portrait := strings.HasSuffix(file, "-p.bin")
		id := strings.TrimSuffix(strings.TrimSuffix(file, "-p.bin"), ".bin")
		hub.images.ServeBinOrientationHTTP(w, r, id, portrait)
	})
	mux.HandleFunc("GET /images/{id}/thumb", func(w http.ResponseWriter, r *http.Request) {
		hub.images.ServeThumbHTTP(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		hub.images.ServePreviewHTTP(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{id}/frame-preview", func(w http.ResponseWriter, r *http.Request) {
		portrait := r.URL.Query().Get("portrait") == "1"
		hub.images.ServeInkJoyFramePreviewHTTP(w, r, r.PathValue("id"), portrait)
	})
	mux.HandleFunc("POST /api/images/{id}/crop", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSaveCrop(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /api/images/{id}/crop", func(w http.ResponseWriter, r *http.Request) {
		hub.handleDeleteCrop(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /api/devices/{id}/display", func(w http.ResponseWriter, r *http.Request) {
		hub.handleDisplay(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /api/devices/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		hub.handleRefresh(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /api/devices/{id}/sleep", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSleep(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /api/devices/{id}/redirect", func(w http.ResponseWriter, r *http.Request) {
		hub.handleRedirect(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/mqtt/logs", hub.handleMQTTLogs)
	mux.HandleFunc("GET /api/samsung", hub.handleSamsungList)
	mux.HandleFunc("POST /api/samsung/poll", hub.handleSamsungPoll)
	mux.HandleFunc("POST /api/samsung/{frameId}/sleep", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungSleep(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("POST /api/samsung/{frameId}/wake", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungWake(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("POST /api/samsung/{frameId}/push", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungPush(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("PUT /api/samsung/{frameId}/config", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungConfigPut(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("POST /api/samsung/{frameId}/image", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungImageUpload(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /api/samsung/{frameId}/preview", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungPreview(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/{frameId}/content.json", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungContentJSON(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/{frameId}/image", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungImage(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/{frameId}/status", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungStatus(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		switch {
		case strings.HasSuffix(file, ".png"):
			hub.handleSamsungPNG(w, r, strings.TrimSuffix(file, ".png"))
		case strings.HasSuffix(file, ".lock"):
			hub.handleSamsungLock(w, r, strings.TrimSuffix(file, ".lock"))
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/", hub.handleStatic)
}
