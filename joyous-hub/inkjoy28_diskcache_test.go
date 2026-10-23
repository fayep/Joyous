package main

import (
	"os"
	"path/filepath"
	"testing"
)

func resetInkJoy28MemCache() {
	inkjoy28IdxCache.mu.Lock()
	inkjoy28IdxCache.m = map[string][][]byte{}
	inkjoy28IdxCache.order = nil
	inkjoy28IdxCache.mu.Unlock()
}

func inkjoy28DiskEntries(t *testing.T, store *ImageStore, id string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(store.cacheDir(), id+".ij28-*"))
	return m
}

// A 28.5" render survives a restart (empty memory cache) via the disk cache,
// and a changed input (orientation, override) gets its own entry.
func TestInkJoy28RenderDiskCache(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id := storeSynthImage(t, store, 1527, 1080)
	resetInkJoy28MemCache()
	t.Cleanup(resetInkJoy28MemCache)

	first, err := store.inkjoy28BaseIndices(id, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := inkjoy28DiskEntries(t, store, id)
	if len(files) != 1 {
		t.Fatalf("disk entries %v, want 1", files)
	}

	// "Restart": drop memory, and make a re-dither impossible by removing the
	// source — the grid must come from disk, unchanged.
	resetInkJoy28MemCache()
	src := store.rawPath(id)
	b, _ := os.ReadFile(src)
	st, _ := os.Stat(src)
	if err := os.WriteFile(src, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(src, st.ModTime(), st.ModTime())
	if err := os.Truncate(src, st.Size()); err != nil { // same size+mtime, garbage content
		t.Fatal(err)
	}
	os.Chtimes(src, st.ModTime(), st.ModTime())
	again, err := store.inkjoy28BaseIndices(id, true, nil)
	if err != nil {
		t.Fatalf("disk hit failed: %v", err)
	}
	for y := range first {
		if string(first[y]) != string(again[y]) {
			t.Fatalf("row %d differs after disk reload", y)
		}
	}
	os.WriteFile(src, b, 0o644)

	// Other inputs → other entries; ClearBinCache removes them all.
	override := make([]PaletteRGB, 8)
	for i, c := range paletteInkJoy28Display {
		override[i] = PaletteRGB{int(c[0]), int(c[1]), int(c[2])}
	}
	if _, err := store.inkjoy28BaseIndices(id, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.inkjoy28BaseIndices(id, true, override); err != nil {
		t.Fatal(err)
	}
	if n := len(inkjoy28DiskEntries(t, store, id)); n < 3 {
		t.Fatalf("%d disk entries after portrait/landscape/override renders, want ≥3", n)
	}
	if err := store.ClearBinCache(); err != nil {
		t.Fatal(err)
	}
	if n := len(inkjoy28DiskEntries(t, store, id)); n != 0 {
		t.Fatalf("%d disk entries after ClearBinCache", n)
	}
}

// An override equal to the built-in 28.5" palette renders exactly like no
// override (the 8-ink override path changes nothing by itself).
func TestInkJoy28DefaultValuedOverrideMatchesNoOverride(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id := storeSynthImage(t, store, 1527, 1080)
	resetInkJoy28MemCache()
	t.Cleanup(resetInkJoy28MemCache)
	override := make([]PaletteRGB, 8)
	for i, c := range paletteInkJoy28Display {
		override[i] = PaletteRGB{int(c[0]), int(c[1]), int(c[2])}
	}
	a, err := store.ServeInkJoy28IJI3(id, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.ServeInkJoy28IJI3(id, true, override)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("default-valued 8-ink override renders differently from no override")
	}
}
