package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB is the SQLite image catalog (hub.db).
type DB struct {
	path string
	sql  *sql.DB
}

// Open creates or opens hub.db under dataDir.
func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "hub.db")
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	db := &DB{path: path, sql: sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	if db.sql == nil {
		return nil
	}
	return db.sql.Close()
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

func (db *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS hub_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS images (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			orientation TEXT NOT NULL DEFAULT '',
			flat_rgb INTEGER NOT NULL DEFAULT 0,
			chroma_boost INTEGER,
			people_likely INTEGER NOT NULL DEFAULT 0,
			people_analyzed INTEGER NOT NULL DEFAULT 0,
			people_detect_ver INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT,
			storage_kind TEXT NOT NULL DEFAULT 'hub',
			source_provider TEXT,
			source_asset_id TEXT,
			rel_path TEXT NOT NULL,
			added_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_images_source_asset
			ON images(source_provider, source_asset_id)
			WHERE source_provider IS NOT NULL AND source_asset_id IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS image_crops (
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			format TEXT NOT NULL,
			x REAL NOT NULL, y REAL NOT NULL, w REAL NOT NULL, h REAL NOT NULL,
			PRIMARY KEY (image_id, format)
		)`,
		`CREATE TABLE IF NOT EXISTS image_tags (
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			tag TEXT NOT NULL,
			PRIMARY KEY (image_id, tag)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_image_tags_tag ON image_tags(tag)`,
		`CREATE TABLE IF NOT EXISTS image_formats (
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			format TEXT NOT NULL,
			PRIMARY KEY (image_id, format)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_image_formats_format ON image_formats(format)`,
		`CREATE TABLE IF NOT EXISTS albums (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			filter_json TEXT NOT NULL DEFAULT '{}',
			default_sort TEXT NOT NULL DEFAULT 'album_order',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS album_members (
			album_id TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			PRIMARY KEY (album_id, image_id)
		)`,
		`CREATE TABLE IF NOT EXISTS album_order (
			album_id TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
			image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
			sort_key REAL NOT NULL,
			PRIMARY KEY (album_id, image_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_album_order_album_sort ON album_order(album_id, sort_key)`,
	}
	for _, stmt := range stmts {
		if _, err := db.sql.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	ver, err := db.metaInt("schema_version")
	if err != nil {
		return err
	}
	if ver == 0 {
		if err := db.setMeta("schema_version", fmt.Sprintf("%d", SchemaVersion)); err != nil {
			return err
		}
	} else if ver != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d (want %d)", ver, SchemaVersion)
	}
	return db.ensureAllAlbum()
}

func (db *DB) metaInt(key string) (int, error) {
	var val string
	err := db.sql.QueryRow(`SELECT value FROM hub_meta WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscanf(val, "%d", &n)
	return n, err
}

func (db *DB) setMeta(key, value string) error {
	_, err := db.sql.Exec(`INSERT INTO hub_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (db *DB) ImageCount() (int, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM images`).Scan(&n)
	return n, err
}
