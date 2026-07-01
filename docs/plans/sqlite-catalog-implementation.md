# Implementation plan: SQLite catalog, smart albums, per-album order

Companion to [ADR 0001](../adr/0001-sqlite-catalog-smart-albums.md).

## Goals (this project)

1. Queryable image catalog in SQLite; blobs unchanged on disk.
2. Tags + derived orientation/formats; filter API.
3. Albums table with `all` + smart albums; per-album `sort_key` order.
4. Migrate existing JSON metas and legacy order files.
5. Album UI: smart album sidebar, filters, drag reorder scoped to current album.
6. *(Phase 2)* Device `playlist_source` + rotation worker.

## Non-goals (initial PR series)

- Manual (hand-picked) albums — schema ready, UI later.
- Automatic device rotation timers — API-ready list only first.
- Consolidating `devices.json` / `color.json` into SQLite.
- Removing `{id}.json` entirely — deprecate after stable dual-write.

---

## Schema (v1)

```sql
-- schema_version in hub_meta(key, value)

CREATE TABLE images (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  size         INTEGER NOT NULL DEFAULT 0,
  width        INTEGER NOT NULL DEFAULT 0,
  height       INTEGER NOT NULL DEFAULT 0,
  orientation  TEXT NOT NULL DEFAULT '',  -- landscape | portrait | square
  flat_rgb     INTEGER NOT NULL DEFAULT 0,
  chroma_boost INTEGER,                   -- NULL = global; 0/1 = override
  people_likely    INTEGER NOT NULL DEFAULT 0,
  people_analyzed  INTEGER NOT NULL DEFAULT 0,
  people_detect_ver INTEGER NOT NULL DEFAULT 0,
  content_hash     TEXT,                -- optional; v1 uploads do not dedup by hash
  storage_kind     TEXT NOT NULL DEFAULT 'hub',  -- hub | photos_ref
  source_provider  TEXT,
  source_asset_id  TEXT,
  rel_path         TEXT NOT NULL,         -- hub: images/{id}; photos_ref: logical key
  added_at     TEXT NOT NULL,              -- RFC3339
  updated_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_images_source_asset ON images(source_provider, source_asset_id)
  WHERE source_provider IS NOT NULL AND source_asset_id IS NOT NULL;

CREATE TABLE image_crops (
  image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  format   TEXT NOT NULL,
  x REAL NOT NULL, y REAL NOT NULL, w REAL NOT NULL, h REAL NOT NULL,
  PRIMARY KEY (image_id, format)
);

CREATE TABLE image_tags (
  image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  tag      TEXT NOT NULL,
  PRIMARY KEY (image_id, tag)
);
CREATE INDEX idx_image_tags_tag ON image_tags(tag);

CREATE TABLE image_formats (
  image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  format   TEXT NOT NULL,                  -- 4:3, 3:4, 16:9, 9:16, native:16:9, …
  PRIMARY KEY (image_id, format)
);
CREATE INDEX idx_image_formats_format ON image_formats(format);

CREATE TABLE albums (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  kind         TEXT NOT NULL,              -- all | smart | manual
  filter_json  TEXT NOT NULL DEFAULT '{}',
  default_sort TEXT NOT NULL DEFAULT 'album_order',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE album_members (
  album_id TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
  image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  PRIMARY KEY (album_id, image_id)
);

CREATE TABLE album_order (
  album_id  TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
  image_id  TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  sort_key  REAL NOT NULL,
  PRIMARY KEY (album_id, image_id)
);
CREATE INDEX idx_album_order_album_sort ON album_order(album_id, sort_key);
```

**Seed:** insert `albums` row `(id='all', name='All photos', kind='all', …)` on first migrate.

### `filter_json` shape

```json
{
  "tags_all":  ["bedroom"],
  "tags_any":  [],
  "tags_none": ["archive"],
  "orientation": "portrait",
  "formats_any": ["3:4", "9:16"],
  "people_likely": null
}
```

Null/absent fields = no constraint.

---

## New Go packages / files

| File | Responsibility |
|------|----------------|
| `joyous-hub/catalog/db.go` | Open DB, pragmas, migrate |
| `joyous-hub/catalog/schema.sql` | Embedded schema + version |
| `joyous-hub/catalog/images.go` | CRUD, derive orientation/formats |
| `joyous-hub/catalog/tags.go` | Set tags, list distinct tags |
| `joyous-hub/catalog/filter.go` | `MatchFilter`, build SQL |
| `joyous-hub/catalog/albums.go` | Album CRUD, list images in order |
| `joyous-hub/catalog/order.go` | `MoveInAlbum`, fractional sort_key |
| `joyous-hub/catalog/migrate.go` | Import from `images/*.json`, legacy order |
| `joyous-hub/catalog/catalog.go` | `Catalog` struct wired into `Hub` |

`ImageStore` keeps blob paths, encode, cache, thumbs. It calls `Catalog` on meta-changing operations.

**Dependency:** `modernc.org/sqlite` (pure Go, no CGO).

---

## Phases

### Phase 0 — Prep (small PR)

- [ ] Add ADR + this plan (done).
- [ ] Add `modernc.org/sqlite` to `go.mod`.
- [ ] Scaffold `catalog` package: open DB, migrations, empty tests.
- [ ] Wire `Catalog` into `main.go` beside `ImageStore` (`data_dir/hub.db`).

**Exit:** `go test ./...` passes; hub starts with empty or migrated DB.

---

### Phase 1 — Catalog + migration (backend)

- [ ] Implement `images` CRUD; crops in `image_crops` (sync `image_formats` on crop write/delete).
- [ ] On `Store()`: insert catalog row; derive orientation from w×h; formats from crops (empty at first).
- [ ] On `SetCrop` / `DeleteCrop`: update `image_crops` + `image_formats` rows.
- [ ] On `PatchMeta` / `Rename` / delete: sync catalog.
- [ ] `migrate.go`: scan `images/*.json` → upsert; read `album_order.json` / `album_head.json` / `album_prev|next` → `album_order` for `all` with sequential sort_keys.
- [ ] `ListImages` reads from catalog (join order for `all`), not directory scan.
- [ ] Keep writing `{id}.json` on meta changes (dual-write) for rollback.
- [ ] Remove or gut `album_list.go` once parity tests pass.

**Tests:**

- Migrate from fixture dir with legacy JSON only.
- Migrate from linked-list + head file.
- Upload → list order = ID sort until explicit reorder.
- Crop → `image_formats` contains `4:3`.

**Exit:** Existing album UI works unchanged; data in SQLite.

---

### Phase 2 — Tags + query API

- [x] `PATCH /api/images/{id}` accepts `tags: string[]`.
- [x] `GET /api/images?tag=foo&tag=bar&orientation=portrait&format=16:9&album_id=all`.
- [x] `GET /api/tags` — autocomplete list.
- [x] Update `AlbumRevision` / catalog revision hash when tags or filters change.

**Tests:** filter combinations; tag index used (explain query in test optional).

**Exit:** API supports everything needed for UI filters.

---

### Phase 3 — Smart albums + per-album order

- [x] `GET/POST/PATCH/DELETE /api/albums`.
- [x] `GET /api/albums/{id}/images`, `GET /api/albums/{id}/count`.
- [x] `PATCH /api/albums/{id}/order` — body `{ "move": { "id", "target" } }` (same UX as today).
- [x] Listing algorithm:
  1. Resolve member IDs (all images | filter query | manual members).
  2. LEFT JOIN `album_order` ON `(album_id, image_id)`.
  3. ORDER BY `sort_key IS NULL`, `sort_key`, tiebreaker per `default_sort`.

**Tests:**

- Smart album count changes when tag added.
- Reorder in smart album does not affect `all` order.
- Reorder in `all` reflects in smart album when `default_sort = album_order`.

**Exit:** Backend complete for smart albums.

---

### Phase 4 — Album UI

- [ ] Sidebar: “All photos” + smart albums list + “New smart album”.
- [ ] Smart album editor: name, filter chips (tags, orientation, formats).
- [ ] Wall renders current album’s image list.
- [ ] Drag-drop calls `PATCH /api/albums/{id}/order`.
- [ ] Unsaved filter → “Save as smart album”.
- [ ] Tag editing on image card or detail (PATCH tags).

**Exit:** User-visible smart albums and per-album reorder.

---

### Phase 5 — Device playlists (follow-on)

- [ ] Extend `Device` / `devices.json`: `playlist_source: { "album_id": "…" }`.
- [ ] `NextPlaylistImage(deviceID) (imageID, error)` in catalog.
- [ ] Rotation worker: interval per device, respect sleep windows, call existing send paths.
- [ ] Device UI: pick album for rotation, interval, shuffle override.

**Exit:** Bedroom frame rotates through smart album.

---

## Fractional sort keys (reorder)

```go
// Move image B before target T in album A.
// sort_key(B) = (sort_key(prev) + sort_key(T)) / 2
// If prev missing: sort_key(B) = sort_key(T) - 1.0
// If no rows yet: assign 1.0, 2.0, 3.0 from tiebreaker order on first drag only
```

Rebalance: if gap too small, renumber `1..n` for that `album_id` only (rare).

---

## Migration / rollback

| Step | Action |
|------|--------|
| Upgrade | Auto-migrate on startup if `hub.db` missing or `schema_version` bump |
| Rebuild | CLI flag or env `JOYOUS_REBUILD_CATALOG=1` — wipe catalog tables, reimport JSON |
| Rollback | Older hub ignores `hub.db`; JSON dual-write keeps it usable |

Document rebuild in hub README / CHANGELOG when shipping.

---

## PR slicing (suggested)

1. **PR1:** Phase 0 + Phase 1 (catalog, migration, list parity) — no UI change.
2. **PR2:** Phase 2 (tags + filter API).
3. **PR3:** Phase 3 (albums API + per-album order).
4. **PR4:** Phase 4 (UI).
5. **PR5:** Phase 5 (device rotation).

Each PR should keep `go test ./...` green.

---

## Open questions (resolve during Phase 3–4)

1. **Native format buckets** — derive `native:16:9` from w/h tolerance, or only count saved crops for `formats_any`?
2. **Smart album default_sort** — default to `album_order` (inherit global) vs `added`?
3. **Tag normalization** — lowercase trim, max length, disallow spaces?
4. **JSON deprecation** — when to stop dual-write to `{id}.json`?

Default proposals: (1) saved crops only first, add native later; (2) `album_order`; (3) lowercase trimmed, hyphen allowed; (4) stop dual-write after one release with rebuild docs.

---

## Success criteria

- [ ] 500-image fixture: filtered `GET /api/albums/{id}/images` &lt; 50ms on dev laptop.
- [ ] Drag reorder in any album touches ≤ 3 DB rows.
- [ ] Smart album membership updates without manual ID maintenance.
- [ ] Fresh install and upgraded install from JSON-only data both work.
- [ ] Device rotation prototype pulls from smart album list (Phase 5).
