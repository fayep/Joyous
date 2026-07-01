package catalog

import (
	"fmt"
	"strings"
	"time"
)

// NormalizeTag lowercases, trims, and collapses whitespace to hyphens.
func NormalizeTag(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), "-")
}

// SetImageTags replaces all tags for an image.
func (db *DB) SetImageTags(imageID string, tags []string) error {
	seen := make(map[string]bool)
	var norm []string
	for _, t := range tags {
		t = NormalizeTag(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		norm = append(norm, t)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM image_tags WHERE image_id = ?`, imageID); err != nil {
		return err
	}
	for _, t := range norm {
		if _, err := tx.Exec(`INSERT INTO image_tags (image_id, tag) VALUES (?, ?)`, imageID, t); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE images SET updated_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), imageID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// TagsFor returns tags for one image.
func (db *DB) TagsFor(imageID string) ([]string, error) {
	rows, err := db.sql.Query(`SELECT tag FROM image_tags WHERE image_id = ? ORDER BY tag`, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// ListTags returns distinct tags for autocomplete.
func (db *DB) ListTags() ([]string, error) {
	rows, err := db.sql.Query(`SELECT DISTINCT tag FROM image_tags ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

func (db *DB) attachTags(img *Image) error {
	tags, err := db.TagsFor(img.ID)
	if err != nil {
		return err
	}
	img.Tags = tags
	return nil
}

// TagsRevision returns a stable string of all tags for revision hashing.
func (db *DB) TagsRevision() (string, error) {
	rows, err := db.sql.Query(`SELECT image_id, tag FROM image_tags ORDER BY image_id, tag`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b []byte
	for rows.Next() {
		var id, tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return "", err
		}
		b = append(b, id...)
		b = append(b, ':')
		b = append(b, tag...)
		b = append(b, '|')
	}
	return string(b), rows.Err()
}

// AlbumsRevision returns album metadata for revision hashing.
func (db *DB) AlbumsRevision() (string, error) {
	rows, err := db.sql.Query(`SELECT id, filter_json, default_sort, updated_at FROM albums ORDER BY id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b []byte
	for rows.Next() {
		var id, filter, sort, updated string
		if err := rows.Scan(&id, &filter, &sort, &updated); err != nil {
			return "", err
		}
		b = append(b, fmt.Sprintf("%s|%s|%s|%s\n", id, filter, sort, updated)...)
	}
	return string(b), rows.Err()
}
