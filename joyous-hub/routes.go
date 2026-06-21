package main

import (
	"net/http"
	"strings"
)

func registerRoutes(mux *http.ServeMux, hub *Hub) {
	mux.HandleFunc("GET /api/devices", hub.handleDevices)
	mux.HandleFunc("POST /api/devices/discover", hub.handleDiscover)
	mux.HandleFunc("GET /api/images", hub.handleImages)
	mux.HandleFunc("POST /api/images", hub.handleImageUpload)
	mux.HandleFunc("DELETE /api/images/{id}", func(w http.ResponseWriter, r *http.Request) {
		hub.handleImageDelete(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{file}", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(r.PathValue("file"), ".bin")
		hub.images.ServeBinHTTP(w, r, id)
	})
	mux.HandleFunc("GET /images/{id}/thumb", func(w http.ResponseWriter, r *http.Request) {
		hub.images.ServeThumbHTTP(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /images/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		hub.images.ServePreviewHTTP(w, r, r.PathValue("id"))
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
	mux.HandleFunc("POST /api/devices/{id}/redirect", func(w http.ResponseWriter, r *http.Request) {
		hub.handleRedirect(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/samsung", hub.handleSamsungList)
	mux.HandleFunc("PUT /api/samsung/{frameId}/config", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungConfigPut(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("POST /api/samsung/{frameId}/image", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungImageUpload(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/sssp_config.xml", hub.handleSamsungSSSPConfig)
	mux.HandleFunc("GET /samsung/"+widgetFile, hub.handleSamsungWGT)
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
	mux.HandleFunc("GET /samsung/", hub.handleSamsungIndex)
	mux.HandleFunc("/", hub.handleStatic)
}
