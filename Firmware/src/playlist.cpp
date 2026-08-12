#include "config.h"
#if ENABLE_SD_CACHE
#include "playlist.h"
#include "sd_card.h"
#include <ArduinoJson.h>
#include <SD_MMC.h>
using fs::File;  // SD_MMC.h, unlike SD.h, does not do "using namespace fs;" itself
#include <string.h>

namespace Playlist {

// Path-safety check on frames[i].file, matching the real firmware's
// validator (traced 2026-08-02, ij_epd v0.7.10 @ 0x4201c6a8): reject
// NULL/empty, reject absolute paths, reject ".." traversal.
static bool validFilePath(const char *str) {
    if (!str || !str[0]) return false;
    if (str[0] == '/') return false;
    if (strstr(str, "..") != nullptr) return false;
    return true;
}

static bool g_loaded = false;
static String g_sourceDir;   // "/ij_play" or "" (root)
static uint32_t g_intervalMs = 0;
static bool g_loop = false;
static String g_frameFiles[100];
static size_t g_frameCount = 0;
static size_t g_currentFrame = 0;

// Tries one candidate directory ("/ij_play" then "" for root, matching
// the real firmware's own preference order -- see
// playlist_path_finder.c). Returns the JSON text on success, empty
// string on failure (missing file or over-size).
static String tryReadPlaylistJson(const String &dir, String &outSourceDir) {
    String path = dir + "/playlist.json";
    File f = SD_MMC.open(path, FILE_READ);
    if (!f) return String();

    size_t size = f.size();
    if (size == 0 || size > 32768) {  // matches the real firmware's 32768-byte cap
        f.close();
        return String();
    }

    String text;
    text.reserve(size + 1);
    while (f.available()) {
        text += (char)f.read();
    }
    f.close();
    outSourceDir = dir;
    return text;
}

bool begin() {
    g_loaded = false;
    g_frameCount = 0;
    g_currentFrame = 0;

    if (!SdCard::present()) return false;

    String sourceDir;
    String text = tryReadPlaylistJson("/ij_play", sourceDir);
    if (text.length() == 0) {
        text = tryReadPlaylistJson("", sourceDir);
    }
    if (text.length() == 0) {
        Serial.println("playlist: no playlist.json found (ij_play/ or root)");
        return false;
    }

    JsonDocument doc;
    if (deserializeJson(doc, text)) {
        Serial.println("playlist: json parse failed");
        return false;
    }

    JsonVariant version = doc["version"];
    if (!version.is<int>() || (version.as<int>() != 1 && version.as<int>() != 3)) {
        Serial.println("playlist: bad version (must be 1 or 3)");
        return false;
    }

    const char *bootPolicy = doc["boot_policy"] | "";
    if (strcmp(bootPolicy, "exclusive") != 0) {
        Serial.println("playlist: bad boot_policy (must be \"exclusive\")");
        return false;
    }

    JsonVariant play = doc["play"];
    const char *mode = play["mode"] | "";
    if (strcmp(mode, "sequence") != 0) {
        Serial.println("playlist: bad play.mode (only \"sequence\" implemented)");
        return false;
    }
    if (!play["loop"].is<bool>()) {
        Serial.println("playlist: missing play.loop");
        return false;
    }

    JsonVariant schedule = doc["schedule"];
    const char *scheduleType = schedule["type"] | "";
    if (strcmp(scheduleType, "interval") != 0) {
        Serial.println("playlist: bad schedule.type (only \"interval\" implemented)");
        return false;
    }
    JsonVariant intervalMin = schedule["interval_min"];
    if (!intervalMin.is<int>() || intervalMin.as<int>() < 1) {
        Serial.println("playlist: bad schedule.interval_min (must be >= 1)");
        return false;
    }

    JsonVariant encrypt = doc["encrypt"];
    if (!encrypt.is<JsonObject>()) {
        Serial.println("playlist: missing encrypt object");
        return false;
    }
    if (!encrypt["enabled"].is<bool>()) {
        Serial.println("playlist: missing encrypt.enabled");
        return false;
    }
    if (encrypt["enabled"].as<bool>()) {
        // Real firmware supports per-recipient ECIES-P256/AES-GCM here
        // (encrypt.scheme/account_uid/device_ids/recipient_count) -- not
        // implemented on this clone. Reject rather than silently ignore.
        Serial.println("playlist: encrypt.enabled=true not supported by this firmware");
        return false;
    }

    JsonArray frames = doc["frames"].as<JsonArray>();
    size_t n = frames.size();
    if (n < 1 || n > 100) {
        Serial.println("playlist: bad frames (must be 1..100 entries)");
        return false;
    }

    for (size_t i = 0; i < n; i++) {
        const char *file = frames[i]["file"] | "";
        if (!validFilePath(file)) {
            Serial.printf("playlist: frames[%u].file invalid or unsafe\n", (unsigned)i);
            return false;
        }
        g_frameFiles[i] = file;
    }

    g_sourceDir = sourceDir;
    g_intervalMs = (uint32_t)intervalMin.as<int>() * 60UL * 1000UL;
    g_loop = play["loop"].as<bool>();
    g_frameCount = n;
    g_currentFrame = 0;
    g_loaded = true;

    Serial.printf("playlist: loaded %u frame(s) from %s/playlist.json, interval=%us loop=%d\n",
                  (unsigned)n, g_sourceDir.c_str(), intervalMin.as<int>(), g_loop);
    return true;
}

bool loaded() { return g_loaded; }
size_t frameCount() { return g_frameCount; }
bool loopEnabled() { return g_loop; }
uint32_t intervalMs() { return g_intervalMs; }

String currentFramePath() {
    if (!g_loaded || g_currentFrame >= g_frameCount) return String();
    return g_sourceDir + "/" + g_frameFiles[g_currentFrame];
}

void advance() {
    if (!g_loaded || g_frameCount == 0) return;
    g_currentFrame++;
    if (g_currentFrame >= g_frameCount) {
        g_currentFrame = g_loop ? 0 : g_frameCount - 1;
    }
}

} // namespace Playlist
#endif // ENABLE_SD_CACHE
