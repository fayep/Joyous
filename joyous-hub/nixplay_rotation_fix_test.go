//go:build !inkjoybridge && !samsungbridge && !nixplaybridge

package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func buildTestNixplayDB(t *testing.T, rows map[string]int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "NixDatabase.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE photo (_id INTEGER PRIMARY KEY, lookup TEXT, rotation INTEGER DEFAULT 0)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	i := 0
	for lookup, rotation := range rows {
		i++
		if _, err := db.Exec(`INSERT INTO photo (_id, lookup, rotation) VALUES (?, ?, ?)`, i, lookup, rotation); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return path
}

// TestRunFixNixplayRotationMatchesByContentAndApplies covers the end-to-end batch flow: a
// gallery image whose raw bytes are byte-identical to a recovered Nixplay export file gets its
// RotateOverride set from that file's DB-recorded rotation — the only place the rotation info
// exists, since it was never in EXIF (see runFixNixplayRotation's doc comment).
func TestRunFixNixplayRotationMatchesByContentAndApplies(t *testing.T) {
	recoveredDir := t.TempDir()
	rotatedBytes := []byte("fake-jpeg-bytes-for-rotated-photo")
	unrotatedBytes := []byte("fake-jpeg-bytes-for-unrotated-photo")
	if err := os.WriteFile(filepath.Join(recoveredDir, "lookupA.jpg"), rotatedBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recoveredDir, "lookupB.jpg"), unrotatedBytes, 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := buildTestNixplayDB(t, map[string]int{
		"lookupA": 90,
		"lookupB": 0,
		"lookupC": 180, // no recovered file for this one — should be reported as unmatched, not applied to anything
	})

	store := NewImageStore(t.TempDir())
	idA, err := store.Store(bytes.NewReader(rotatedBytes), "photoA.jpg")
	if err != nil {
		t.Fatal(err)
	}
	idB, err := store.Store(bytes.NewReader(unrotatedBytes), "photoB.jpg")
	if err != nil {
		t.Fatal(err)
	}
	idUnrelated, err := store.Store(bytes.NewReader([]byte("not from nixplay at all")), "other.jpg")
	if err != nil {
		t.Fatal(err)
	}

	// Dry run must not write anything.
	if err := runFixNixplayRotation(store, recoveredDir, dbPath, true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	meta, _ := store.readMeta(idA)
	if meta.RotateOverride != 0 {
		t.Fatalf("dry run must not persist changes, got RotateOverride=%d", meta.RotateOverride)
	}

	if err := runFixNixplayRotation(store, recoveredDir, dbPath, false); err != nil {
		t.Fatalf("apply: %v", err)
	}

	metaA, err := store.readMeta(idA)
	if err != nil {
		t.Fatal(err)
	}
	if metaA.RotateOverride != 90 {
		t.Fatalf("photoA RotateOverride: got %d want 90", metaA.RotateOverride)
	}

	metaB, err := store.readMeta(idB)
	if err != nil {
		t.Fatal(err)
	}
	if metaB.RotateOverride != 0 {
		t.Fatalf("photoB RotateOverride: got %d want 0 (matched but rotation=0 in db)", metaB.RotateOverride)
	}

	metaUnrelated, err := store.readMeta(idUnrelated)
	if err != nil {
		t.Fatal(err)
	}
	if metaUnrelated.RotateOverride != 0 {
		t.Fatalf("unrelated image must be untouched, got RotateOverride=%d", metaUnrelated.RotateOverride)
	}

	// Re-running must be idempotent — no error, no double-rotation.
	if err := runFixNixplayRotation(store, recoveredDir, dbPath, false); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	metaA2, _ := store.readMeta(idA)
	if metaA2.RotateOverride != 90 {
		t.Fatalf("photoA RotateOverride after rerun: got %d want 90 (unchanged)", metaA2.RotateOverride)
	}
}

func TestRunFixNixplayRotationRequiresBothPaths(t *testing.T) {
	store := NewImageStore(t.TempDir())
	if err := runFixNixplayRotation(store, "", "", true); err == nil {
		t.Fatal("expected error when recoveredDir and dbPath are both empty")
	}
	if err := runFixNixplayRotation(store, t.TempDir(), "", true); err == nil {
		t.Fatal("expected error when dbPath is empty")
	}
}
