package catalog

import (
	"fmt"
	"time"
)

func (db *DB) ensureAllAlbum() error {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM albums WHERE id = ?`, AlbumAll).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := formatTime(time.Now().UTC())
	_, err = db.sql.Exec(`INSERT INTO albums (id, name, kind, filter_json, default_sort, created_at, updated_at)
		VALUES (?, ?, ?, '{}', ?, ?, ?)`,
		AlbumAll, "All photos", "all", SortAlbumOrder, now, now)
	return err
}

// ListAlbumImageIDs returns image ids for albumID in display order.
func (db *DB) ListAlbumImageIDs(albumID string) ([]string, error) {
	if albumID == "" {
		albumID = AlbumAll
	}
	album, err := db.GetAlbum(albumID)
	if err != nil {
		return nil, err
	}
	return db.listAlbumMemberIDsOrdered(album)
}

// ListAlbumImages returns full image rows for an album in display order.
func (db *DB) ListAlbumImages(albumID string) ([]Image, error) {
	return db.ListFilteredImages(albumID, Filter{})
}

// ListFilteredImages lists images in an album with an optional extra filter (query API).
func (db *DB) ListFilteredImages(albumID string, extra Filter) ([]Image, error) {
	if albumID == "" {
		albumID = AlbumAll
	}
	album, err := db.GetAlbum(albumID)
	if err != nil {
		return nil, err
	}
	ids, err := db.listAlbumMemberIDsOrdered(album)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	// Apply extra inline filter by intersecting with matching ids.
	extra = extra.normalized()
	if !extra.isEmpty() {
		matched, err := db.imageIDsMatchingFilter(extra)
		if err != nil {
			return nil, err
		}
		have := make(map[string]bool, len(matched))
		for _, id := range matched {
			have[id] = true
		}
		filtered := ids[:0]
		for _, id := range ids {
			if have[id] {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	out := make([]Image, 0, len(ids))
	for _, id := range ids {
		img, err := db.GetImage(id)
		if err != nil {
			continue
		}
		out = append(out, img)
	}
	return out, nil
}

func (f Filter) isEmpty() bool {
	f = f.normalized()
	return len(f.TagsAll) == 0 && len(f.TagsAny) == 0 && len(f.TagsNone) == 0 &&
		f.Orientation == "" && len(f.FormatsAny) == 0 && f.PeopleLikely == nil
}

// SetAlbumOrder rebuilds explicit sort keys for albumID from a full ID list (tests / migration).
func (db *DB) SetAlbumOrder(albumID string, ids []string) error {
	if albumID == "" {
		albumID = AlbumAll
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM album_order WHERE album_id = ?`, albumID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(`INSERT INTO album_order (album_id, image_id, sort_key) VALUES (?, ?, ?)`,
			albumID, id, float64(i+1)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MoveInAlbum inserts draggedID immediately before targetID in albumID.
func (db *DB) MoveInAlbum(albumID, draggedID, targetID string) error {
	if albumID == "" {
		albumID = AlbumAll
	}
	if draggedID == targetID {
		return nil
	}
	ids, err := db.ListAlbumImageIDs(albumID)
	if err != nil {
		return err
	}
	fromIdx, toIdx := -1, -1
	for i, id := range ids {
		if id == draggedID {
			fromIdx = i
		}
		if id == targetID {
			toIdx = i
		}
	}
	if fromIdx < 0 {
		return fmt.Errorf("unknown image id %q", draggedID)
	}
	if toIdx < 0 {
		return fmt.Errorf("unknown target id %q", targetID)
	}
	if fromIdx+1 == toIdx {
		return nil
	}
	item := ids[fromIdx]
	ids = append(ids[:fromIdx], ids[fromIdx+1:]...)
	if fromIdx < toIdx {
		toIdx--
	}
	ids = append(ids[:toIdx], append([]string{item}, ids[toIdx:]...)...)

	// Assign sort keys 1..n for explicit order.
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM album_order WHERE album_id = ?`, albumID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(`INSERT INTO album_order (album_id, image_id, sort_key) VALUES (?, ?, ?)`,
			albumID, id, float64(i+1)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AlbumOrderRevision returns a stable string reflecting album_order for revision hashing.
func (db *DB) AlbumOrderRevision(albumID string) (string, error) {
	if albumID == "" {
		albumID = AlbumAll
	}
	rows, err := db.sql.Query(`SELECT image_id, sort_key FROM album_order WHERE album_id = ? ORDER BY sort_key`, albumID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b []byte
	for rows.Next() {
		var id string
		var sk float64
		if err := rows.Scan(&id, &sk); err != nil {
			return "", err
		}
		b = append(b, id...)
		b = append(b, '|')
	}
	if len(b) == 0 {
		return "", nil
	}
	return string(b), rows.Err()
}

// HasAlbumOrder reports whether any explicit order rows exist for an album.
func (db *DB) HasAlbumOrder(albumID string) (bool, error) {
	if albumID == "" {
		albumID = AlbumAll
	}
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM album_order WHERE album_id = ?`, albumID).Scan(&n)
	return n > 0, err
}
