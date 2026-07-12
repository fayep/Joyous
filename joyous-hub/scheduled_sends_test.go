package main

import (
	"encoding/json"
	"math/rand"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"joyous-hub/catalog"
)

func TestScheduledSendDueTimes(t *testing.T) {
	loc := time.Local
	base := ScheduledSendConfig{
		DeviceID: "dev1",
		AlbumID:  "album1",
		Times:    []string{"09:00", "18:30"},
		Enabled:  true,
	}

	now := time.Date(2026, 7, 12, 9, 0, 0, 0, loc)
	due := scheduledSendDueTimes(base, now)
	if len(due) != 1 || due[0] != "09:00" {
		t.Fatalf("got %v, want [09:00]", due)
	}

	// Not due if the minute doesn't match.
	notDue := scheduledSendDueTimes(base, time.Date(2026, 7, 12, 9, 1, 0, 0, loc))
	if len(notDue) != 0 {
		t.Fatalf("got %v, want none", notDue)
	}

	// Already fired today: not due again even at the exact minute.
	fired := base
	fired.LastFiredDates = map[string]string{"09:00": "2026-07-12"}
	notDue2 := scheduledSendDueTimes(fired, now)
	if len(notDue2) != 0 {
		t.Fatalf("got %v, want none (already fired today)", notDue2)
	}

	// Fired on a previous day: due again today.
	firedYesterday := base
	firedYesterday.LastFiredDates = map[string]string{"09:00": "2026-07-11"}
	due2 := scheduledSendDueTimes(firedYesterday, now)
	if len(due2) != 1 || due2[0] != "09:00" {
		t.Fatalf("got %v, want [09:00]", due2)
	}

	// Disabled config is never due.
	disabled := base
	disabled.Enabled = false
	if got := scheduledSendDueTimes(disabled, now); len(got) != 0 {
		t.Fatalf("disabled config got %v, want none", got)
	}

	// No album configured is never due.
	noAlbum := base
	noAlbum.AlbumID = ""
	if got := scheduledSendDueTimes(noAlbum, now); len(got) != 0 {
		t.Fatalf("no-album config got %v, want none", got)
	}
}

func TestNextScheduledImageEvenDistribution(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	candidates := []string{"a", "b", "c", "d"}

	var queue []string
	counts := map[string]int{}
	const cycles = 50
	for i := 0; i < len(candidates)*cycles; i++ {
		var id string
		var ok bool
		id, queue, ok = nextScheduledImage(queue, candidates, rng)
		if !ok {
			t.Fatalf("nextScheduledImage returned !ok at iteration %d", i)
		}
		counts[id]++
		// Every full cycle (len(candidates) picks), every candidate must have
		// appeared exactly (cycle number) times so far — that's the even-distribution
		// guarantee: no photo repeats before the others have had a turn.
		if (i+1)%len(candidates) == 0 {
			want := (i + 1) / len(candidates)
			for _, c := range candidates {
				if counts[c] != want {
					t.Fatalf("after %d picks, count[%s]=%d want %d (counts=%v)", i+1, c, counts[c], want, counts)
				}
			}
		}
	}
}

func TestNextScheduledImageDropsStaleQueueEntries(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	// Queue has an ID no longer in the album; it must be filtered out rather than sent.
	queue := []string{"stale", "b"}
	id, newQueue, ok := nextScheduledImage(queue, []string{"a", "b"}, rng)
	if !ok {
		t.Fatal("expected ok")
	}
	if id != "b" {
		t.Fatalf("got %q, want %q (stale id should be dropped)", id, "b")
	}
	if len(newQueue) != 0 {
		t.Fatalf("got queue %v, want empty", newQueue)
	}
}

func TestNextScheduledImageEmptyCandidates(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	if _, _, ok := nextScheduledImage(nil, nil, rng); ok {
		t.Fatal("expected !ok for empty candidate list")
	}
}

func TestScheduledSendStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewScheduledSendStore(dir)

	if _, err := store.Get("dev:1"); err != nil {
		t.Fatalf("Get on missing config: %v", err)
	}

	cfg := ScheduledSendConfig{
		DeviceID: "dev:1",
		AlbumID:  "album1",
		Times:    []string{"07:00", "19:00"},
		Enabled:  true,
		Queue:    []string{"x", "y"},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get("dev:1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AlbumID != cfg.AlbumID || len(got.Times) != 2 || !got.Enabled || len(got.Queue) != 2 {
		t.Fatalf("got %+v, want %+v", got, cfg)
	}

	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 || all[0].DeviceID != "dev:1" {
		t.Fatalf("All() = %+v", all)
	}

	if err := store.Delete("dev:1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got2, err := store.Get("dev:1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got2.Enabled || got2.AlbumID != "" {
		t.Fatalf("got %+v, want zero-value config after delete", got2)
	}
}

func TestScheduledSendFileNameIsPathSafe(t *testing.T) {
	dangerous := []string{"../etc/passwd", "samsung:1.2.3.4", "a/b\\c", ".."}
	for _, id := range dangerous {
		name := scheduledSendFileName(id)
		if name == "" || name == ".json" {
			continue
		}
		if containsPathSeparatorOrTraversal(name) {
			t.Fatalf("scheduledSendFileName(%q) = %q, unsafe", id, name)
		}
	}
}

func containsPathSeparatorOrTraversal(s string) bool {
	for _, r := range s {
		if r == '/' || r == '\\' {
			return true
		}
	}
	return len(s) >= 2 && (s == ".." || (len(s) > 2 && s[:2] == ".."))
}

func TestNormalizeScheduledSendTimes(t *testing.T) {
	times, err := normalizeScheduledSendTimes([]string{"9:00", "18:30", "09:00", " 07:05 "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"07:05", "09:00", "18:30"}
	if len(times) != len(want) {
		t.Fatalf("got %v want %v", times, want)
	}
	sort.Strings(times)
	for i := range want {
		if times[i] != want[i] {
			t.Fatalf("got %v want %v", times, want)
		}
	}

	if _, err := normalizeScheduledSendTimes([]string{"25:00"}); err == nil {
		t.Fatal("expected error for invalid time")
	}
}

func TestScheduledSendHandlersRoundTrip(t *testing.T) {
	h := buildTestHub(t)
	h.devices.MarkConnected("AABBCCDDEEFF")
	deviceID := "AABBCCDDEEFF"

	album, err := h.images.CreateSmartAlbum("album1", "Test Album", catalog.Filter{})
	if err != nil {
		t.Fatalf("CreateSmartAlbum: %v", err)
	}

	// GET before any schedule exists: disabled zero-value config.
	rec := httptest.NewRecorder()
	h.handleScheduledSendGet(rec, httptest.NewRequest("GET", "/api/devices/"+deviceID+"/schedule", nil), deviceID)
	if rec.Code != 200 {
		t.Fatalf("initial GET: %d %s", rec.Code, rec.Body.String())
	}
	var got ScheduledSendConfig
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Enabled {
		t.Fatalf("expected disabled by default, got %+v", got)
	}

	// PUT enabling with a valid album and times.
	body := `{"album_id":"` + album.ID + `","times":["09:00","18:30"],"enabled":true}`
	putRec := httptest.NewRecorder()
	putReq := httptest.NewRequest("PUT", "/api/devices/"+deviceID+"/schedule", strings.NewReader(body))
	putReq.Header.Set("Content-Type", "application/json")
	h.handleScheduledSendPut(putRec, putReq, deviceID)
	if putRec.Code != 200 {
		t.Fatalf("PUT: %d %s", putRec.Code, putRec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	h.handleScheduledSendGet(rec2, httptest.NewRequest("GET", "/api/devices/"+deviceID+"/schedule", nil), deviceID)
	var got2 ScheduledSendConfig
	json.NewDecoder(rec2.Body).Decode(&got2)
	if !got2.Enabled || got2.AlbumID != album.ID || len(got2.Times) != 2 {
		t.Fatalf("got %+v", got2)
	}

	// PUT enabling without an album is rejected.
	badRec := httptest.NewRecorder()
	badReq := httptest.NewRequest("PUT", "/api/devices/"+deviceID+"/schedule", strings.NewReader(`{"enabled":true,"times":["09:00"]}`))
	badReq.Header.Set("Content-Type", "application/json")
	h.handleScheduledSendPut(badRec, badReq, deviceID)
	if badRec.Code != 400 {
		t.Fatalf("expected 400 for missing album, got %d %s", badRec.Code, badRec.Body.String())
	}

	// GET/PUT for an unknown device is rejected.
	unknownRec := httptest.NewRecorder()
	h.handleScheduledSendGet(unknownRec, httptest.NewRequest("GET", "/api/devices/nope/schedule", nil), "nope")
	if unknownRec.Code != 404 {
		t.Fatalf("expected 404 for unknown device, got %d", unknownRec.Code)
	}
}
