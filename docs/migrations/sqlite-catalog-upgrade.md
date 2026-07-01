# Upgrade guide: SQLite catalog & smart albums

**Branch:** `feature/sqlite-catalog`  
**Companion docs:** [ADR 0001](../adr/0001-sqlite-catalog-smart-albums.md), [implementation plan](../plans/sqlite-catalog-implementation.md)

This guide covers backing up a live Joyous Hub, upgrading to the SQLite catalog build, and rolling back if needed.

---

## What changes

| Before | After |
|--------|--------|
| Album wall scans `images/*.json` | Queryable catalog in `{data_dir}/hub.db` |
| Drag order in `album_order.json` or per-file linked list | Per-album order in SQLite `album_order` |
| No tags / smart albums | Tags, filters, smart albums, Album UI sidebar |

**Unchanged on disk:** image blobs (`images/{id}` without extension), converted cache (`cache/`), thumbnails (`thumbs/`), `devices.json`, `color.json`, `overlay.json`, Samsung configs (`samsung/`).

**Dual-write:** per-image JSON sidecars (`images/{id}.json`) are still updated for names, crops, tags, and related fields. Smart albums and per-album drag order live in **`hub.db` only**.

---

## Find your data directory

The hub stores everything under `data_dir` from config (CLI `--data-dir` overrides the file).

**macOS (typical):**

```bash
grep data_dir ~/Library/Application\ Support/Joyous/config.yaml
```

Install scripts often use something like `/Volumes/tank/Media/photoframe` — always use the value from **your** config.

---

## Backup before upgrade

**Stop the hub first** so SQLite and files are not mid-write:

```bash
launchctl bootout "gui/$(id -u)/com.joyous.hub" 2>/dev/null || true
# Or, if running via run-debug.sh: Ctrl+C in that terminal
```

### Minimum (rollback to pre-SQLite hub)

These are what the one-time migration reads on first startup:

| Path | Purpose |
|------|---------|
| `{data_dir}/images/` | Raw uploads **and** `{id}.json` metadata sidecars |
| `{data_dir}/album_order.json` | Legacy “All photos” wall order (if present) |
| `{data_dir}/album_head.json` | Legacy linked-list head (if present) |

```bash
DATA="/path/from/config.yaml"   # your data_dir
BACKUP="$HOME/joyous-pre-sqlite-$(date +%Y%m%d)"
mkdir -p "$BACKUP"

cp -a "$DATA/images" "$BACKUP/"
cp -a "$DATA/album_order.json" "$BACKUP/" 2>/dev/null || true
cp -a "$DATA/album_head.json" "$BACKUP/" 2>/dev/null || true
```

### After first run of the new build (recommended)

If the new hub has started even once, also copy the catalog:

```bash
cp -a "$DATA"/hub.db* "$BACKUP/" 2>/dev/null || true
```

Smart albums, tag index, and drag order from the new UI are stored here. Without this backup, rolling back to the old binary keeps your photos but loses smart albums and new ordering.

### Full data-dir snapshot (safest)

With the hub stopped:

```bash
DATA="/path/from/config.yaml"
BACKUP="$HOME/joyous-data-backup-$(date +%Y%m%d)"
cp -a "$DATA" "$BACKUP"
```

Optional but useful: `~/Library/Application Support/Joyous/config.yaml` (ports, `server_addr`, `data_dir`).

`cache/` and `thumbs/` are regenerable; include them only if you want faster restore.

---

## Upgrade

1. **Back up** (above).
2. **Build and install** the new hub on the Mac that runs Joyous (from a checkout of `feature/sqlite-catalog`):

   ```bash
   cd joyous-hub
   ./scripts/install-local.sh
   ```

   Adjust `INSTALL_ROOT`, `DATA_DIR`, etc. in the environment if your install differs from script defaults.

3. **Start the hub** (launchd service or foreground debug):

   ```bash
   launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.joyous.hub.plist
   # Or: ./scripts/run-debug.sh
   ```

4. **Verify in the Album tab:**
   - Sidebar: “All photos” + smart albums
   - Filter bar on All photos; drag reorder (drop on the **left edge** of a card)
   - Tags button on each photo card

5. **Check logs** on first start for migration lines (`catalog migrate: …`). Migration runs only when `hub.db` exists but the `images` table is empty and `images/*.json` files are present.

---

## First startup behavior

1. Opens or creates `{data_dir}/hub.db` (SQLite, WAL mode).
2. Applies schema v1 (`images`, `image_tags`, `image_crops`, `image_formats`, `albums`, `album_order`, …).
3. If the catalog has **no images** but `images/*.json` exist:
   - Imports each sidecar into SQLite (tags, crops, orientation, formats).
   - Imports legacy order from `album_order.json`, or from `album_head.json` + `album_prev` / `album_next` in sidecars, into `album_order` for album `all`.
4. Seeds album row `id = all` (“All photos”) if missing.
5. Continues dual-writing `{id}.json` on metadata changes.

Re-running migration is a no-op once the catalog has image rows.

---

## Rollback

### A. Back to the old hub (before using smart albums)

1. Stop the hub.
2. Remove the catalog (only if you have a JSON backup from before upgrade):

   ```bash
   rm -f "$DATA/hub.db" "$DATA/hub.db-wal" "$DATA/hub.db-shm"
   ```

3. Restore backed-up `images/` (and `album_order.json` / `album_head.json` if you used them).
4. Install and run the **previous** `joyous-hub` binary.

### B. After using smart albums or new drag order

Keep your `hub.db` backup. Restoring only `images/` is not enough — smart albums and per-album order are in SQLite. To preserve that work, restore `hub.db*` as well, or stay on the SQLite build.

### C. Restore from full data-dir snapshot

1. Stop the hub.
2. Replace `{data_dir}` with the snapshot (or merge `images/` + `hub.db*` from the snapshot).
3. Start the matching hub version.

---

## Troubleshooting

| Symptom | Likely cause |
|---------|----------------|
| Empty album after upgrade | Migration skipped corrupt sidecars or missing raw files; check logs for `catalog migrate: skip …` |
| Order wrong once, then fixed | Legacy order imported; use drag reorder or check `album_order` in a fresh `hub.db` restore |
| Smart albums missing after rollback | Expected if you removed `hub.db` and reverted binary — restore `hub.db` from backup |
| `hub.db` locked / corrupt backup | Hub was running during copy; stop service and copy again |

Inspect catalog (optional):

```bash
sqlite3 "$DATA/hub.db" "SELECT COUNT(*) FROM images;"
sqlite3 "$DATA/hub.db" "SELECT id, name, kind FROM albums;"
```

---

## API surface (for scripts / devices)

- `GET /api/images?tag=&tag_any=&tag_none=&orientation=&format=&album_id=`
- `GET /api/tags`, `PATCH /api/images/{id}` (includes `tags`)
- `GET|POST|PATCH|DELETE /api/albums`
- `GET /api/albums/{id}/images`, `/count`
- `PATCH /api/albums/{id}/order` — `{ "move": { "id", "target" } }` (insert before target)

Device playlist rotation (Phase 5) is not in this branch yet; smart albums are UI- and API-ready for manual use.
