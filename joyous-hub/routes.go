package main

import (
	"net/http"
	"strings"
)

func registerRoutes(mux *http.ServeMux, hub *Hub) {
	// BLE scan/adopt are handled by inkjoy-bridge (see inkjoy_bridge_ui.go)
	// and reached through the generic bridge proxy at /inkjoy/api/ble/…
	// (bridge_routes.go); the hub itself has no Bluetooth dependency.
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
	mux.HandleFunc("GET /api/devices/{id}/schedule", func(w http.ResponseWriter, r *http.Request) {
		hub.handleScheduledSendGet(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("PUT /api/devices/{id}/schedule", func(w http.ResponseWriter, r *http.Request) {
		hub.handleScheduledSendPut(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/ui/revision", hub.handleUIRevision)
	mux.HandleFunc("GET /api/tags", hub.handleTagsList)
	mux.HandleFunc("GET /api/albums", hub.handleAlbumsList)
	mux.HandleFunc("POST /api/albums", hub.handleAlbumsCreate)
	mux.HandleFunc("GET /api/albums/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleAlbumGet(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("PATCH /api/albums/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleAlbumPatch(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /api/albums/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleAlbumDelete(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/albums/{id}/images", func(w http.ResponseWriter, r *http.Request) {
		hub.handleAlbumImages(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/albums/{id}/count", func(w http.ResponseWriter, r *http.Request) {
		hub.handleAlbumCount(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("PATCH /api/albums/{id}/order", func(w http.ResponseWriter, r *http.Request) {
		hub.handleAlbumOrder(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/images", hub.handleImages)
	mux.HandleFunc("POST /api/images", hub.handleImageUpload)
	mux.HandleFunc("DELETE /api/images/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleImageDelete(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("PATCH /api/images/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleImagePatch(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/images/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleImageGet(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		id, overlayToken, portrait := parseImageBinFilename(file)
		hub.images.ServeBinOrientationHTTP(w, r, id, portrait, overlayToken)
	})
	mux.HandleFunc("GET /images/{id}/thumb", func(w http.ResponseWriter, r *http.Request) {
		hub.images.ServeThumbHTTP(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		hub.images.ServePreviewHTTP(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{id}/original", func(w http.ResponseWriter, r *http.Request) {
		hub.images.ServeOriginalHTTP(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{id}/frame-preview", func(w http.ResponseWriter, r *http.Request) {
		portrait := r.URL.Query().Get("portrait") == "1"
		overlay := r.URL.Query().Get("overlay")
		hub.images.ServeInkJoyFramePreviewHTTP(w, r, r.PathValue("id"), portrait, overlay)
	})
	mux.HandleFunc("GET /api/overlay", hub.handleOverlayGet)
	mux.HandleFunc("PUT /api/overlay", hub.handleOverlayPut)
	mux.HandleFunc("GET /api/overlay/preview", hub.handleOverlayPreview)
	mux.HandleFunc("POST /api/overlay/preview", hub.handleOverlayPreview)
	mux.HandleFunc("POST /api/overlay/metrics", hub.handleOverlayMetrics)
	mux.HandleFunc("GET /api/color", hub.handleColorGet)
	mux.HandleFunc("PUT /api/color", hub.handleColorPut)
	mux.HandleFunc("GET /api/color/presets", hub.handleColorPresets)
	mux.HandleFunc("GET /api/calibration/{kind}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleCalibrationPNG(w, r, r.PathValue("kind"))
	})
	mux.HandleFunc("POST /api/calibration/inkjoy/send", hub.handleInkJoyCalibrationSend)
	mux.HandleFunc("POST /api/calibration/inkjoy-black-248/send", hub.handleInkJoyBlack248CalibrationSend)
	mux.HandleFunc("POST /api/calibration/inkjoy-lo-ladder/send", hub.handleInkJoyLoLadderCalibrationSend)
	mux.HandleFunc("POST /api/overlay/send", hub.handleOverlaySend)
	mux.HandleFunc("POST /api/images/{id}/crop", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSaveCrop(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /api/images/{id}/crop", func(w http.ResponseWriter, r *http.Request) {
		hub.handleDeleteCrop(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /api/devices/{id}/display", func(w http.ResponseWriter, r *http.Request) {
		hub.handleDisplay(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/send/{sendId}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSendStatus(w, r, r.PathValue("sendId"))
	})
	mux.HandleFunc("GET /api/sends/active", hub.handleActiveSends)
	mux.HandleFunc("GET /api/events", hub.handleEvents)
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
	mux.HandleFunc("GET /api/samsung/logs", hub.handleSamsungLogs)
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
	mux.HandleFunc("POST /api/samsung/{frameId}/calibration", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungCalibration(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("PUT /api/samsung/{frameId}/config", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungConfigPut(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /api/samsung/{frameId}/daily-refresh", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungDailyRefreshGet(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("PUT /api/samsung/{frameId}/daily-refresh", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungDailyRefreshPut(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("POST /api/samsung/{frameId}/daily-refresh/sync-inactive", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungDailyRefreshSyncInactive(w, r, r.PathValue("frameId"))
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
	mux.HandleFunc("HEAD /samsung/{frameId}/image", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("HEAD /samsung/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		if strings.HasSuffix(file, ".png") {
			hub.handleSamsungPNG(w, r, strings.TrimSuffix(file, ".png"))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/", hub.handleStatic)
}
