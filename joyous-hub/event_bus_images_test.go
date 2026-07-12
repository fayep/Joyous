package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"joyous-hub/catalog"
)

func drainImagesEvent(t *testing.T, ch <-chan []byte) imagesEventPayload {
	t.Helper()
	select {
	case payload := <-ch:
		var env struct {
			Type string              `json:"type"`
			Data imagesEventPayload `json:"data"`
		}
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("unmarshal event: %v (payload=%s)", err, payload)
		}
		if env.Type != "images" {
			t.Fatalf("got event type %q, want images (payload=%s)", env.Type, payload)
		}
		return env.Data
	case <-time.After(time.Second):
		t.Fatal("did not receive an images event")
	}
	return imagesEventPayload{}
}

func TestImagePatchPublishesImagesUpdated(t *testing.T) {
	h := buildTestHub(t)
	id, err := h.images.Store(bytes.NewReader([]byte{1}), "tagged.jpg")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	ch, cancel := h.events.Subscribe("watcher")
	defer cancel()

	req := httptest.NewRequest("PATCH", "/api/images/"+id, strings.NewReader(`{"tags":["bedroom"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleImagePatch(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}

	data := drainImagesEvent(t, ch)
	if len(data.Updated) != 1 || data.Updated[0].ID != id {
		t.Fatalf("got %+v, want one updated image with id %s", data, id)
	}
	if len(data.Updated[0].Tags) != 1 || data.Updated[0].Tags[0] != "bedroom" {
		t.Fatalf("updated image missing new tags: %+v", data.Updated[0])
	}
}

func TestImageUploadPublishesImagesUpdated(t *testing.T) {
	h := buildTestHub(t)
	ch, cancel := h.events.Subscribe("watcher")
	defer cancel()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "new.jpg")
	fw.Write([]byte{1, 2, 3})
	mw.Close()
	req := httptest.NewRequest("POST", "/api/images", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.handleImageUpload(rec, req)
	if rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}

	data := drainImagesEvent(t, ch)
	if len(data.Updated) != 1 || data.Updated[0].Name != "new.jpg" {
		t.Fatalf("got %+v, want one updated image named new.jpg", data)
	}
}

func TestImageDeletePublishesImagesRemoved(t *testing.T) {
	h := buildTestHub(t)
	id, err := h.images.Store(bytes.NewReader([]byte{1}), "gone.jpg")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	ch, cancel := h.events.Subscribe("watcher")
	defer cancel()

	rec := httptest.NewRecorder()
	h.handleImageDelete(rec, httptest.NewRequest("DELETE", "/api/images/"+id, nil), id)
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	data := drainImagesEvent(t, ch)
	if len(data.Removed) != 1 || data.Removed[0] != id {
		t.Fatalf("got %+v, want removed=[%s]", data, id)
	}
}

func TestImageCropPublishesImagesUpdated(t *testing.T) {
	h := buildTestHub(t)
	id, err := h.images.Store(bytes.NewReader([]byte{1}), "crop.jpg")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	ch, cancel := h.events.Subscribe("watcher")
	defer cancel()

	body := `{"format":"4:3","x":0,"y":0,"w":0.5,"h":0.5}`
	req := httptest.NewRequest("POST", "/api/images/"+id+"/crop", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleSaveCrop(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("save crop: %d %s", rec.Code, rec.Body.String())
	}

	data := drainImagesEvent(t, ch)
	if len(data.Updated) != 1 || data.Updated[0].ID != id {
		t.Fatalf("got %+v, want one updated image with id %s", data, id)
	}
	if _, ok := data.Updated[0].Crops["4:3"]; !ok {
		t.Fatalf("updated image missing the new crop: %+v", data.Updated[0])
	}
}

func TestAlbumOrderPublishesReorderedAlbum(t *testing.T) {
	h := buildTestHub(t)
	id1, _ := h.images.Store(bytes.NewReader([]byte{1}), "a.jpg")
	id2, _ := h.images.Store(bytes.NewReader([]byte{2}), "b.jpg")
	album, err := h.images.CreateSmartAlbum("album1", "Test Album", catalog.Filter{})
	if err != nil {
		t.Fatalf("CreateSmartAlbum: %v", err)
	}
	ch, cancel := h.events.Subscribe("watcher")
	defer cancel()

	body := `{"move":{"id":"` + id2 + `","target":"` + id1 + `"}}`
	req := httptest.NewRequest("PATCH", "/api/albums/"+album.ID+"/order", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleAlbumOrder(rec, req, album.ID)
	if rec.Code != 200 {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}

	data := drainImagesEvent(t, ch)
	if len(data.ReorderedAlbums) != 1 || data.ReorderedAlbums[0] != album.ID {
		t.Fatalf("got %+v, want reordered_albums=[%s]", data, album.ID)
	}
}
