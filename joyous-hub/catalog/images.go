package catalog

import (
	"database/sql"
	"fmt"
	"time"
)

// DefaultRelPath is the hub storage path for image id.
func DefaultRelPath(id string) string {
	return "images/" + id
}

// UpsertImage inserts or replaces a catalog row and optional crops.
func (db *DB) UpsertImage(img Image, crops map[string]Crop) error {
	if img.ID == "" {
		return fmt.Errorf("image id required")
	}
	if img.RelPath == "" {
		img.RelPath = DefaultRelPath(img.ID)
	}
	if img.StorageKind == "" {
		img.StorageKind = StorageHub
	}
	now := time.Now().UTC()
	if img.AddedAt.IsZero() {
		img.AddedAt = now
	}
	if img.UpdatedAt.IsZero() {
		img.UpdatedAt = now
	}
	if img.Orientation == "" {
		img.Orientation = OrientationFromDimensions(img.Width, img.Height)
	}

	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	flat := 0
	if img.FlatRGB {
		flat = 1
	}
	var chroma sql.NullInt64
	if img.ChromaBoost != nil {
		chroma = sql.NullInt64{Int64: boolToInt(*img.ChromaBoost), Valid: true}
	}
	_, err = tx.Exec(`INSERT INTO images (
		id, name, size, width, height, orientation, flat_rgb, chroma_boost,
		people_likely, people_analyzed, people_detect_ver, rotate_override,
		content_hash, storage_kind, source_provider, source_asset_id, rel_path,
		added_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name, size=excluded.size, width=excluded.width, height=excluded.height,
		orientation=excluded.orientation, flat_rgb=excluded.flat_rgb, chroma_boost=excluded.chroma_boost,
		people_likely=excluded.people_likely, people_analyzed=excluded.people_analyzed,
		people_detect_ver=excluded.people_detect_ver, rotate_override=excluded.rotate_override,
		content_hash=excluded.content_hash, storage_kind=excluded.storage_kind,
		source_provider=excluded.source_provider, source_asset_id=excluded.source_asset_id,
		rel_path=excluded.rel_path, updated_at=excluded.updated_at`,
		img.ID, img.Name, img.Size, img.Width, img.Height, img.Orientation, flat, chroma,
		boolToInt(img.PeopleLikely), boolToInt(img.PeopleAnalyzed), img.PeopleDetectVer, img.RotateOverride,
		nullStr(img.ContentHash), img.StorageKind, nullStr(img.SourceProvider), nullStr(img.SourceAssetID),
		img.RelPath, formatTime(img.AddedAt), formatTime(img.UpdatedAt),
	)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM image_crops WHERE image_id = ?`, img.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM image_formats WHERE image_id = ?`, img.ID); err != nil {
		return err
	}
	for format, c := range crops {
		if c.W <= 0 || c.H <= 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO image_crops (image_id, format, x, y, w, h) VALUES (?, ?, ?, ?, ?, ?)`,
			img.ID, format, c.X, c.Y, c.W, c.H); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO image_formats (image_id, format) VALUES (?, ?)`, img.ID, format); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetImage loads one image and its crops.
func (db *DB) GetImage(id string) (Image, error) {
	row := db.sql.QueryRow(`SELECT`+imageSelectCols+` FROM images WHERE id = ?`, id)
	img, err := scanImage(row)
	if err == sql.ErrNoRows {
		return Image{}, fmt.Errorf("image %s not found", id)
	}
	if err != nil {
		return Image{}, err
	}
	crops, err := db.cropsFor(id)
	if err != nil {
		return Image{}, err
	}
	img.Crops = crops
	_ = db.attachTags(&img)
	return img, nil
}

// DeleteImage removes a catalog row (cascades tags, crops, formats, order).
func (db *DB) DeleteImage(id string) error {
	_, err := db.sql.Exec(`DELETE FROM images WHERE id = ?`, id)
	return err
}

// UpdateDimensions sets width/height and orientation.
func (db *DB) UpdateDimensions(id string, width, height int) error {
	orient := OrientationFromDimensions(width, height)
	_, err := db.sql.Exec(`UPDATE images SET width = ?, height = ?, orientation = ?, updated_at = ? WHERE id = ?`,
		width, height, orient, formatTime(time.Now().UTC()), id)
	return err
}

const imageSelectCols = `
	id, name, size, width, height, orientation, flat_rgb, chroma_boost,
	people_likely, people_analyzed, people_detect_ver, rotate_override,
	COALESCE(content_hash,''), COALESCE(storage_kind,'hub'),
	COALESCE(source_provider,''), COALESCE(source_asset_id,''),
	rel_path, added_at, updated_at`

func (db *DB) cropsFor(id string) (map[string]Crop, error) {
	rows, err := db.sql.Query(`SELECT format, x, y, w, h FROM image_crops WHERE image_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]Crop)
	for rows.Next() {
		var format string
		var c Crop
		if err := rows.Scan(&format, &c.X, &c.Y, &c.W, &c.H); err != nil {
			return nil, err
		}
		out[format] = c
	}
	return out, rows.Err()
}

func scanImage(row *sql.Row) (Image, error) {
	var img Image
	var flat, peopleLikely, peopleAnalyzed int
	var chroma sql.NullInt64
	var addedAt, updatedAt string
	err := row.Scan(
		&img.ID, &img.Name, &img.Size, &img.Width, &img.Height, &img.Orientation, &flat, &chroma,
		&peopleLikely, &peopleAnalyzed, &img.PeopleDetectVer, &img.RotateOverride,
		&img.ContentHash, &img.StorageKind, &img.SourceProvider, &img.SourceAssetID,
		&img.RelPath, &addedAt, &updatedAt,
	)
	if err != nil {
		return img, err
	}
	img.FlatRGB = flat != 0
	img.PeopleLikely = peopleLikely != 0
	img.PeopleAnalyzed = peopleAnalyzed != 0
	if chroma.Valid {
		v := chroma.Int64 != 0
		img.ChromaBoost = &v
	}
	img.AddedAt = parseTime(addedAt)
	img.UpdatedAt = parseTime(updatedAt)
	return img, nil
}

func scanImageRows(rows *sql.Rows) (Image, error) {
	var img Image
	var flat, peopleLikely, peopleAnalyzed int
	var chroma sql.NullInt64
	var addedAt, updatedAt string
	err := rows.Scan(
		&img.ID, &img.Name, &img.Size, &img.Width, &img.Height, &img.Orientation, &flat, &chroma,
		&peopleLikely, &peopleAnalyzed, &img.PeopleDetectVer, &img.RotateOverride,
		&img.ContentHash, &img.StorageKind, &img.SourceProvider, &img.SourceAssetID,
		&img.RelPath, &addedAt, &updatedAt,
	)
	if err != nil {
		return img, err
	}
	img.FlatRGB = flat != 0
	img.PeopleLikely = peopleLikely != 0
	img.PeopleAnalyzed = peopleAnalyzed != 0
	if chroma.Valid {
		v := chroma.Int64 != 0
		img.ChromaBoost = &v
	}
	img.AddedAt = parseTime(addedAt)
	img.UpdatedAt = parseTime(updatedAt)
	return img, nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}
