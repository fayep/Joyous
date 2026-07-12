//go:build !inkjoybridge && !samsungbridge && !nixplaybridge

package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// runFixNixplayRotation batch-corrects gallery image rotation for photos originally imported
// from a Nixplay export. Nixplay stores rotation as a column in its own local SQLite database
// (photo.rotation, keyed by photo.lookup — a content hash), not as EXIF orientation, so a gallery
// image imported directly from a recovered export can be rendered with the wrong orientation
// everywhere (thumbnail, preview, and every device's send path) even though decodeAnyImage's EXIF
// correction ran successfully — there was simply nothing in EXIF to correct.
//
// Matching a gallery image to its recovered counterpart is done by raw byte content (sha256),
// not by name or ID — a "straight import pass" (see the joyous-hub session that added this) can
// use the recovered file's bytes directly as the gallery upload, in which case the two are
// byte-identical, so this is exact: no perceptual/fuzzy matching, no risk of correcting the wrong
// photo. Gallery images that don't match any recovered file are left untouched and reported.
//
// The fix is non-destructive: it only sets ImageMeta.RotateOverride (see (*ImageStore).PatchMeta
// and applyRotateOverride), the same mechanism the manual rotate buttons in the Album view use.
// Nothing is rewritten on disk; run again with a different match or run PatchMeta with 0 to undo.
func runFixNixplayRotation(store *ImageStore, recoveredDir, dbPath string, dryRun bool) error {
	if recoveredDir == "" || dbPath == "" {
		return fmt.Errorf("both -nixplay-recovered-dir and -nixplay-db are required")
	}

	rotationByLookup, err := loadNixplayRotations(dbPath)
	if err != nil {
		return fmt.Errorf("load nixplay db: %w", err)
	}
	log.Printf("fix-nixplay-rotation: loaded %d photo rows from %s (%d with nonzero rotation)",
		len(rotationByLookup), dbPath, countNonzero(rotationByLookup))

	recoveredByHash, err := hashRecoveredPhotos(recoveredDir)
	if err != nil {
		return fmt.Errorf("hash recovered photos: %w", err)
	}
	log.Printf("fix-nixplay-rotation: hashed %d files in %s", len(recoveredByHash), recoveredDir)

	metas, err := store.ListImages()
	if err != nil {
		return fmt.Errorf("list gallery images: %w", err)
	}

	matched, corrected, alreadyCorrect, unmatchedGallery := 0, 0, 0, 0
	usedLookups := make(map[string]bool, len(recoveredByHash))

	for _, meta := range metas {
		raw, err := os.ReadFile(store.rawPath(meta.ID))
		if err != nil {
			log.Printf("fix-nixplay-rotation: skip %s (%s): read raw: %v", meta.ID, meta.Name, err)
			continue
		}
		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		lookup, ok := recoveredByHash[hash]
		if !ok {
			unmatchedGallery++
			continue
		}
		matched++
		usedLookups[lookup] = true
		rotation, ok := rotationByLookup[lookup]
		if !ok || rotation == 0 {
			alreadyCorrect++
			continue
		}
		if meta.RotateOverride == rotation {
			alreadyCorrect++
			continue
		}
		if dryRun {
			log.Printf("fix-nixplay-rotation: [dry run] would set %s (%q) rotate_override=%d (lookup=%s)",
				meta.ID, meta.Name, rotation, lookup)
			corrected++
			continue
		}
		if _, err := store.PatchMeta(meta.ID, nil, "", nil, &rotation); err != nil {
			log.Printf("fix-nixplay-rotation: %s (%q): PatchMeta: %v", meta.ID, meta.Name, err)
			continue
		}
		log.Printf("fix-nixplay-rotation: %s (%q) rotate_override=%d (lookup=%s)", meta.ID, meta.Name, rotation, lookup)
		corrected++
	}

	unmatchedRecovered := 0
	for _, lookup := range recoveredByHash {
		if !usedLookups[lookup] {
			unmatchedRecovered++
		}
	}

	mode := "applied"
	if dryRun {
		mode = "dry run — pass -fix-nixplay-rotation-dry-run=false to apply"
	}
	log.Printf("fix-nixplay-rotation: done (%s). gallery=%d matched=%d corrected=%d already_correct=%d gallery_unmatched=%d recovered_unmatched=%d",
		mode, len(metas), matched, corrected, alreadyCorrect, unmatchedGallery, unmatchedRecovered)
	return nil
}

// loadNixplayRotations reads photo.lookup -> photo.rotation from a recovered NixDatabase.db,
// opened read-only since this is a forensic copy, not a live database this process should ever
// write to.
func loadNixplayRotations(dbPath string) (map[string]int, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT lookup, rotation FROM photo WHERE lookup IS NOT NULL AND lookup != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var lookup string
		var rotation int
		if err := rows.Scan(&lookup, &rotation); err != nil {
			return nil, err
		}
		out[lookup] = normalizeNixplayRotation(rotation)
	}
	return out, rows.Err()
}

// normalizeNixplayRotation wraps into [0,360) and snaps to the nearest 0/90/180/270 — Nixplay's
// rotation column has only ever been observed at those four values, but this guards against any
// unexpected value causing normalizeRotateDegrees to reject the whole row later.
func normalizeNixplayRotation(d int) int {
	d = ((d % 360) + 360) % 360
	return (d + 45) / 90 % 4 * 90
}

func countNonzero(m map[string]int) int {
	n := 0
	for _, v := range m {
		if v != 0 {
			n++
		}
	}
	return n
}

// hashRecoveredPhotos sha256-hashes every file in dir, keyed by hash, valued by the Nixplay
// lookup hash (its filename without extension) — see photo.lookup in loadNixplayRotations.
func hashRecoveredPhotos(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("fix-nixplay-rotation: skip %s: %v", path, err)
			continue
		}
		sum := sha256.Sum256(data)
		lookup := e.Name()[:len(e.Name())-len(filepath.Ext(e.Name()))]
		out[hex.EncodeToString(sum[:])] = lookup
	}
	return out, nil
}
