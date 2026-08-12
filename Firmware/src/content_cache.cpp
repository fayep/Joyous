#include "config.h"
#if ENABLE_SD_CACHE
#include "content_cache.h"
#include "panel.h"
#include <HTTPClient.h>
#include <SD_MMC.h>
using fs::File;  // SD_MMC.h, unlike SD.h, does not do "using namespace fs;" itself
#include <esp_heap_caps.h>
#include <vector>
#include <algorithm>

namespace {

const char *kCacheRoot = "/ij_play/cache";
constexpr size_t kCacheMaxFiles = 200;  // per orientation, not combined
constexpr uint64_t kCacheMaxBytes = 400ULL * 1024 * 1024;  // per orientation;
    // well under a realistic card's real capacity (a 256GB card holds
    // ~128,000 frames at this compression ratio) -- just a sane bound
    // against unbounded growth, not a real space constraint. See
    // research/sd-content-cache-plan.md.

// ORIENT_PORTRAIT/ORIENT_LANDSCAPE (config.h) are 0/90 -- not meant as
// directory names directly, so map to fixed short ones instead.
String dirFor(int orientation) {
    return String(kCacheRoot) + (orientation == ORIENT_PORTRAIT ? "/portrait" : "/landscape");
}
String pathFor(const String &hash, int orientation, const char *ext) {
    return dirFor(orientation) + "/" + hash + ext;
}
String tmpPathFor(const String &hash, int orientation, const char *ext) {
    return dirFor(orientation) + "/" + hash + ext + ".tmp";
}

// Which extension this hash is actually cached under, if any -- during the
// hub's format cutover (dual-format acceptance, see Panel::renderBinFromFile's
// comment), a given hash could be cached as either. Returns nullptr if
// neither exists.
const char *cachedExtension(const String &hash, int orientation) {
    if (SD_MMC.exists(pathFor(hash, orientation, ".png"))) return ".png";
    if (SD_MMC.exists(pathFor(hash, orientation, ".bin"))) return ".bin";
    return nullptr;
}

// Creates `path` and every missing parent directory, like `mkdir -p`.
// SD_MMC.mkdir() (FatFs f_mkdir) is NOT recursive -- it fails outright if the
// immediate parent doesn't exist yet, so a single SD_MMC.mkdir("/ij_play/cache")
// silently does nothing on a card that's never had an /ij_play directory
// created on it before (confirmed the hard way, 2026-08-12: the previous
// version here called SD_MMC.mkdir(kCacheRoot) without ever checking its
// return value, then proceeded to SD_MMC.open(FILE_WRITE) into a directory
// that was never actually created). Returns true if `path` exists or now
// does; false only on a genuine SD_MMC.mkdir() failure.
bool ensureDir(const String &path) {
    if (path.isEmpty() || path == "/") return true;
    if (SD_MMC.exists(path)) return true;
    int slash = path.lastIndexOf('/');
    if (slash > 0 && !ensureDir(path.substring(0, slash))) return false;
    if (SD_MMC.mkdir(path)) return true;
    // Another play could have raced this exact directory into existence
    // between the SD_MMC.exists() check above and this mkdir call -- that's
    // success, not failure.
    return SD_MMC.exists(path);
}

// Reads and decodes `path` fully into a PSRAM buffer, then renders it --
// dispatching on the file's own extension. Shared by render() (a specific
// hash lookup) and renderFrame() (offline-mode cycling by index) so
// there's one open/read/decode path, not two.
bool renderFile(const String &path) {
    File f = SD_MMC.open(path, FILE_READ);
    if (!f) return false;

    size_t len = f.size();
    uint8_t *buf = (uint8_t *)heap_caps_malloc(len, MALLOC_CAP_SPIRAM);
    if (!buf) {
        Serial.println("content_cache: renderFile: alloc failed");
        f.close();
        return false;
    }
    size_t got = f.read(buf, len);
    f.close();
    if (got != len) {
        Serial.println("content_cache: renderFile: short read");
        heap_caps_free(buf);
        return false;
    }

    bool ok = path.endsWith(".png") ? Panel::renderPngBytes(buf, len)
                                     : Panel::renderBinBytes(buf, len);
    heap_caps_free(buf);
    return ok;
}

// Lists real cache entries (skips directories and orphaned .tmp files,
// same exclusions as evictIfNeeded()), sorted by filename -- a stable
// order across boots/reinsertion, unlike mtime or directory-entry order
// (FatFs-implementation-dependent, not something to rely on), so offline-
// mode cycling advances through the same sequence every time rather than
// reshuffling whenever a new file gets cached.
std::vector<String> listCachedFiles(int orientation) {
    std::vector<String> files;
    String dir = dirFor(orientation);
    File d = SD_MMC.open(dir);
    if (!d || !d.isDirectory()) {
        if (d) d.close();
        return files;
    }
    File f = d.openNextFile();
    while (f) {
        String name = String(f.name());
        if (!f.isDirectory() && !name.endsWith(".tmp")) {
            files.push_back(dir + "/" + name);
        }
        f.close();
        f = d.openNextFile();
    }
    d.close();
    std::sort(files.begin(), files.end());
    return files;
}

} // namespace

namespace ContentCache {

// Logs, per orientation dir: whether it already existed, was just created,
// or failed to create -- distinct outcomes, not just pass/fail, so a boot
// log makes it obvious whether this is a fresh card (first-ever creation),
// a returning one (already there from a prior boot), or a real mkdir
// problem (SD mounted, per SdCard::begin()'s own log line, but directory
// creation still failing -- points at something other than "not mounted").
void begin() {
    for (int orientation : {ORIENT_PORTRAIT, ORIENT_LANDSCAPE}) {
        String dir = dirFor(orientation);
        bool existedBefore = SD_MMC.exists(dir);
        bool ok = existedBefore || ensureDir(dir);
        const char *state = existedBefore ? "already existed"
                            : ok           ? "created now"
                                           : "FAILED to create";
        Serial.printf("content_cache: %s: %s\n", dir.c_str(), state);
    }
}

bool has(const String &hash, int orientation) {
    return cachedExtension(hash, orientation) != nullptr;
}

bool render(const String &hash, int orientation) {
    const char *ext = cachedExtension(hash, orientation);
    if (!ext) return false;
    return renderFile(pathFor(hash, orientation, ext));
}

bool fetchAndRender(const String &host, int port, const String &path,
                     const String &hash, int orientation) {
    // Save under whichever format the hub's URL implies -- the hub
    // controls `path`, and its extension is the source of truth for what
    // format the response body actually is.
    const char *ext = path.endsWith(".png") ? ".png" : ".bin";

    String dir = dirFor(orientation);
    if (!ensureDir(dir)) {
        Serial.printf("content_cache: mkdir %s failed\n", dir.c_str());
        return false;
    }

    String tmpPath = tmpPathFor(hash, orientation, ext);
    SD_MMC.remove(tmpPath);  // stale leftover from a previous interrupted fetch, if any

    HTTPClient http;
    String url = "http://" + host + ":" + String(port) + path;
    if (!http.begin(url)) return false;

    int code = http.GET();
    if (code != HTTP_CODE_OK) {
        http.end();
        return false;
    }

    File out = SD_MMC.open(tmpPath, FILE_WRITE);
    if (!out) {
        http.end();
        return false;
    }

    WiFiClient *stream = http.getStreamPtr();
    int contentLen = http.getSize();
    uint8_t buf[1024];
    int written = 0;
    bool ok = true;
    while (http.connected() && (contentLen < 0 || written < contentLen)) {
        size_t avail = stream->available();
        if (!avail) {
            if (!http.connected()) break;
            delay(1);
            continue;
        }
        int n = stream->readBytes(buf, min(avail, sizeof(buf)));
        if (n <= 0) {
            ok = false;
            break;
        }
        if (out.write(buf, n) != (size_t)n) {
            ok = false;
            break;
        }
        written += n;
    }
    // flush() before close(): File::close() is just fclose(), which isn't
    // reliably durable on this VFS/FatFs stack on its own -- File::flush()
    // exists specifically to add an fsync() workaround for that (see its
    // own comment, citing github.com/espressif/arduino-esp32/issues/1293).
    // Skipping it here meant every cached write was potentially still
    // sitting in a libc/FatFs buffer, never actually pushed out over SPI
    // to the card, when we then turned around and read it back (render())
    // or renamed it (below) -- a real, confirmed gap, not hardware flakiness.
    out.flush();
    out.close();
    http.end();

    if (!ok || (contentLen >= 0 && written != contentLen)) {
        SD_MMC.remove(tmpPath);  // never leave a partial file at the real cache path
        return false;
    }

    // Write-then-rename: the real cache path only ever reflects a fully
    // written file, never a partial one from an interrupted fetch.
    String finalPath = pathFor(hash, orientation, ext);
    SD_MMC.remove(finalPath);  // SD_MMC.rename() requires the destination not exist
    if (!SD_MMC.rename(tmpPath, finalPath)) {
        Serial.println("content_cache: rename failed");
        SD_MMC.remove(tmpPath);
        return false;
    }

    return render(hash, orientation);
}

void evictIfNeeded(int orientation) {
    String dir = dirFor(orientation);
    File d = SD_MMC.open(dir);
    if (!d || !d.isDirectory()) {
        if (d) d.close();
        return;
    }

    struct Entry {
        String name;
        uint32_t size;
        time_t mtime;
    };
    std::vector<Entry> entries;
    uint64_t totalBytes = 0;

    File f = d.openNextFile();
    while (f) {
        // Skip a genuinely-orphaned .tmp (e.g. a crash mid-fetch that never
        // reached the cleanup path) -- not a real cache entry, and its size
        // mid-write isn't meaningful for the byte-budget count either.
        String name = String(f.name());
        if (!f.isDirectory() && !name.endsWith(".tmp")) {
            entries.push_back({name, (uint32_t)f.size(), f.getLastWrite()});
            totalBytes += f.size();
        }
        f.close();
        f = d.openNextFile();
    }
    d.close();

    if (entries.size() <= kCacheMaxFiles && totalBytes <= kCacheMaxBytes) {
        return;
    }

    std::sort(entries.begin(), entries.end(),
              [](const Entry &a, const Entry &b) { return a.mtime < b.mtime; });

    size_t i = 0;
    while (i < entries.size() &&
           (entries.size() - i > kCacheMaxFiles || totalBytes > kCacheMaxBytes)) {
        String p = dir + "/" + entries[i].name;
        if (SD_MMC.remove(p)) {
            totalBytes -= entries[i].size;
            Serial.printf("content_cache: evicted %s\n", entries[i].name.c_str());
        }
        i++;
    }
}

size_t frameCount(int orientation) {
    return listCachedFiles(orientation).size();
}

bool renderFrame(size_t index, int orientation) {
    std::vector<String> files = listCachedFiles(orientation);
    if (index >= files.size()) return false;
    return renderFile(files[index]);
}

} // namespace ContentCache
#endif // ENABLE_SD_CACHE
