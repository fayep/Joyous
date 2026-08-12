#include "config.h"
#if defined(BOARD_SEEED) || defined(BOARD_GOODISPLAY)
// Shared TFT_eSPI/Seeed_GFX implementation for both boards -- Seeed's EE02
// (T133A01_DRIVER) and GooDisplay's ESP32-133C02 (GDEP133C02, wired via
// tft_setup_goodisplay.h) are the same underlying driver silicon, confirmed
// by an identical register map (2026-08-08). GooDisplay's board first got a
// from-scratch ESP-IDF driver (panel_goodisplay.cpp, kept in the tree but
// excluded from the build -- see platformio.ini's goodisplay_c02
// build_src_filter) while a Seeed_GFX attempt was blocked by a TFT_DC/
// TFT_RST pin-aliasing bug; once that was fixed and a real DC/X pin traced
// from the board (see tft_setup_goodisplay.h), Seeed_GFX worked immediately
// with correct rotation and no manual transform math, so it became the real
// implementation for GooDisplay too.
#include "panel.h"
#include <TFT_eSPI.h>
#include <HTTPClient.h>
#include <SD_MMC.h>
using fs::File;  // SD_MMC.h, unlike SD.h, does not do "using namespace fs;" itself
#include "lodepng.h"
#include "lodepng_psram_allocator.h"
// FreeSansBold24pt7b / FreeSans12pt7b come from TFT_eSPI.h -> gfxfont.h,
// which #includes all 48 GFXFF fonts unconditionally when LOAD_GFXFF is set.
// lodepng's PSRAM-backed allocator overrides live in
// lib/lodepng/lodepng_psram_allocator.cpp, not here -- see that file's
// comment for why they can't live in our own .cpp files.

extern EPaper epaper; // owned by main.cpp

// GT32L32S0140 font-chip footprint on Seeed's schematic (U6, SPI1) is
// present but unpopulated on that board rev -- confirmed against the
// physical unit, so there's no on-board glyph ROM to draw from there.
// GooDisplay's board isn't known to have an equivalent font chip either.
// These GFXFF vector fonts (already compiled in via LOAD_GFXFF/SMOOTH_FONT
// in Setup510_.../tft_setup_goodisplay.h) are the best available quality on
// either board: real glyphs at their point size instead of a
// setTextSize()-scaled GLCD font.
static void titleFont() { epaper.setFreeFont(&FreeSansBold24pt7b); }
static void bodyFont() { epaper.setFreeFont(&FreeSans12pt7b); }

namespace Panel {

// InkJoy .bin hi byte -> this library's 6-color palette (T133A01_Defines.h,
// USE_COLORFULL_EPAPER branch). See research/firmware-notes.md for the bin
// format and encode_bin.py's HI_BYTES for the source values.
static uint8_t hiByteToColor(uint8_t hi) {
    switch (hi) {
        case 0x01: return TFT_BLACK;
        case 0x02: return TFT_WHITE;
        case 0x03: return TFT_YELLOW;
        case 0x04: return TFT_RED;
        case 0x06: return TFT_BLUE;
        case 0x07: return TFT_GREEN;
        default:   return TFT_WHITE;
    }
}

// Indexed-PNG palette index -> color. Index order is a contract with the
// hub-side encoder (0=black,1=white,2=yellow,3=red,4=blue,5=green), not
// derived from the file's own PLTE chunk -- the PLTE bytes are written for
// PC-side viewers to show accurate per-device colors, but this firmware's
// decode always uses color_convert=0 (raw index, no RGBA expansion) and
// maps by position, so the actual PLTE RGB values are cosmetic here.
static uint8_t paletteIndexToColor(uint8_t idx) {
    switch (idx) {
        case 0: return TFT_BLACK;
        case 1: return TFT_WHITE;
        case 2: return TFT_YELLOW;
        case 3: return TFT_RED;
        case 4: return TFT_BLUE;
        case 5: return TFT_GREEN;
        default: return TFT_WHITE;
    }
}

void begin() {
    epaper.begin();
    epaper.setRotation(PANEL_ROTATION);
}

void sleep() {
    epaper.sleep();
}

void showProvisioning(const String &clientId) {
    // fillSprite, not fillScreen: with setRotation(1) on a 4bpp EPaper sprite,
    // fillScreen uses unswapped TFT _width/_height and only clears 1200×1200 of
    // the 1200×1600 buffer — bottom quarter keeps the previous image.
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);

    titleFont();
    epaper.drawString("Setup mode", 40, 100);

    bodyFont();
    epaper.drawString("Adopt this frame from the hub:", 40, 220);
    epaper.drawString("look for BLE name", 40, 260);

    titleFont();
    epaper.drawString("IJ_" + clientId, 40, 360);

    epaper.setFreeFont(nullptr);
    epaper.update();
}

void showOrientation(bool portrait) {
    // Toast only — hub Portrait crop updates on the next encoded send.
    // Portrait text is drawn in native portrait coords (90° from PANEL_ROTATION)
    // so it reads upright when the frame is hung tall; mount rotation restored after.
    if (portrait) {
        epaper.setRotation(0); // flip to 2 if this reads upside down
    }
    const int16_t w = epaper.width();
    const int16_t h = epaper.height();
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);
    epaper.setTextDatum(MC_DATUM);
    titleFont();
    epaper.drawString(portrait ? "Portrait" : "Landscape", w / 2, h / 2 - 60);
    bodyFont();
    epaper.drawString("Hub crop preference saved", w / 2, h / 2 + 60);
    epaper.drawString("Next send uses this crop", w / 2, h / 2 + 110);
    epaper.setTextDatum(TL_DATUM);
    epaper.setFreeFont(nullptr);
    epaper.update();
    if (portrait) {
        epaper.setRotation(PANEL_ROTATION);
    }
}

void showMode(bool offline) {
    const int16_t w = epaper.width();
    const int16_t h = epaper.height();
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);
    epaper.setTextDatum(MC_DATUM);
    titleFont();
    epaper.drawString(offline ? "Offline mode" : "Online mode", w / 2, h / 2 - 60);
    bodyFont();
    epaper.drawString(offline ? "Playing from SD playlist" : "Waiting for hub", w / 2, h / 2 + 60);
    epaper.setTextDatum(TL_DATUM);
    epaper.setFreeFont(nullptr);
    epaper.update();
}

void showFactoryReset() {
    const int16_t w = epaper.width();
    const int16_t h = epaper.height();
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);
    epaper.setTextDatum(MC_DATUM);
    titleFont();
    epaper.drawString("Factory reset", w / 2, h / 2 - 60);
    bodyFont();
    epaper.drawString("Credentials cleared", w / 2, h / 2 + 60);
    epaper.drawString("Rebooting to setup mode", w / 2, h / 2 + 110);
    epaper.setTextDatum(TL_DATUM);
    epaper.setFreeFont(nullptr);
    epaper.update();
}

void showConnected() {
    const int16_t w = epaper.width();
    const int16_t h = epaper.height();
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);
    epaper.setTextDatum(MC_DATUM);
    titleFont();
    epaper.drawString("Connected", w / 2, h / 2 - 60);
    bodyFont();
    epaper.drawString("Ready to receive images", w / 2, h / 2 + 60);
    epaper.drawString("from Joyous Hub", w / 2, h / 2 + 110);
    epaper.setTextDatum(TL_DATUM);
    epaper.setFreeFont(nullptr);
    epaper.update();
}

void showLowBattery() {
    const int16_t w = epaper.width();
    const int16_t h = epaper.height();
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);
    epaper.setTextDatum(MC_DATUM);
    titleFont();
    epaper.drawString("Critical Low Battery", w / 2, h / 2 - 60);
    bodyFont();
    epaper.drawString("Connect USB to power supply", w / 2, h / 2 + 60);
    epaper.setTextDatum(TL_DATUM);
    epaper.setFreeFont(nullptr);
    epaper.update();
}

bool renderBinFromHttp(const String &host, int port, const String &path, void (*onProgress)(int)) {
    HTTPClient http;
    String url = "http://" + host + ":" + String(port) + path;
    if (!http.begin(url)) return false;

    int code = http.GET();
    if (code != HTTP_CODE_OK) {
        http.end();
        return false;
    }

    WiFiClient *stream = http.getStreamPtr();
    const int W = INKJOY_BIN_WIDTH;
    const int H = INKJOY_BIN_HEIGHT;
    static uint8_t rowBuf[INKJOY_BIN_WIDTH * 2];
    int nextPct = 20;

    for (int fileRow = 0; fileRow < H; fileRow++) {
        int got = 0;
        while (got < W * 2) {
            int n = stream->readBytes(rowBuf + got, W * 2 - got);
            if (n <= 0) {
                http.end();
                return false;
            }
            got += n;
        }
        int imageY = H - 1 - fileRow; // bin rows are stored bottom-to-top
        for (int col = 0; col < W; col++) {
            uint8_t hi = rowBuf[col * 2];
            epaper.drawPixel(col, imageY, hiByteToColor(hi));
        }
        if (nextPct <= 80 && fileRow >= (H * nextPct) / 100) {
            if (onProgress) onProgress(nextPct);
            nextPct += 20;
        }
    }

    http.end();
    epaper.update();
    return true;
}

// Legacy streaming .bin decode, factored out so both renderBinFromFile
// (path known to be .bin, by extension) and renderBinFromHttp share it
// instead of duplicating the row-by-row loop.
static bool renderBinStream(Stream &in) {
    const int W = INKJOY_BIN_WIDTH;
    const int H = INKJOY_BIN_HEIGHT;
    static uint8_t rowBuf[INKJOY_BIN_WIDTH * 2];

    for (int fileRow = 0; fileRow < H; fileRow++) {
        int got = 0;
        while (got < W * 2) {
            int n = in.readBytes(rowBuf + got, W * 2 - got);
            if (n <= 0) return false;
            got += n;
        }
        int imageY = H - 1 - fileRow; // bin rows are stored bottom-to-top
        for (int col = 0; col < W; col++) {
            uint8_t hi = rowBuf[col * 2];
            epaper.drawPixel(col, imageY, hiByteToColor(hi));
        }
    }
    return true;
}

bool renderBinFromFile(const String &path) {
#if ENABLE_SD_CACHE
    File f = SD_MMC.open(path, FILE_READ);
    if (!f) return false;

    bool ok;
    if (path.endsWith(".png")) {
        size_t len = f.size();
        uint8_t *buf = (uint8_t *)heap_caps_malloc(len, MALLOC_CAP_SPIRAM);
        if (!buf) {
            f.close();
            return false;
        }
        size_t got = f.read(buf, len);
        f.close();
        ok = (got == len) && renderPngBytes(buf, len);
        heap_caps_free(buf);
        return ok;  // renderPngBytes() already calls epaper.update() on success
    }

    ok = renderBinStream(f);
    f.close();
    if (ok) epaper.update();
    return ok;
#else
    (void)path;
    return false;  // no SD support on this board yet
#endif
}

bool renderBinBytes(const uint8_t *bytes, size_t len) {
    const size_t expected = (size_t)INKJOY_BIN_WIDTH * INKJOY_BIN_HEIGHT * 2;
    if (len != expected) return false;

    // MemoryStream-style adapter isn't available here without pulling in
    // another dependency; renderBinStream() reads from an Arduino Stream&,
    // so just reimplement the same loop directly against the buffer -- this
    // path is a straight linear scan either way, no real duplication of
    // logic worth sharing beyond what renderBinStream already factors out
    // for the two streaming callers.
    const int W = INKJOY_BIN_WIDTH;
    const int H = INKJOY_BIN_HEIGHT;
    for (int fileRow = 0; fileRow < H; fileRow++) {
        const uint8_t *rowBuf = bytes + (size_t)fileRow * W * 2;
        int imageY = H - 1 - fileRow; // bin rows are stored bottom-to-top
        for (int col = 0; col < W; col++) {
            uint8_t hi = rowBuf[col * 2];
            epaper.drawPixel(col, imageY, hiByteToColor(hi));
        }
    }
    epaper.update();
    return true;
}

bool renderPngBytes(const uint8_t *bytes, size_t len) {
    LodePNGState state;
    lodepng_state_init(&state);
    state.decoder.color_convert = 0;  // keep the file's own raw indexed data

    unsigned char *raw = nullptr;
    unsigned width = 0, height = 0;
    unsigned err = lodepng_decode(&raw, &width, &height, &state, bytes, len);
    bool ok = (err == 0) && raw &&
              width == INKJOY_BIN_WIDTH && height == INKJOY_BIN_HEIGHT &&
              state.info_png.color.colortype == LCT_PALETTE &&
              state.info_png.color.bitdepth == 4;
    if (!ok) {
        Serial.printf("panel: renderPngBytes: decode failed (err=%u w=%u h=%u)\n", err, width, height);
        if (raw) lodepng_free(raw);
        lodepng_state_cleanup(&state);
        return false;
    }

    const int W = INKJOY_BIN_WIDTH;
    const int H = INKJOY_BIN_HEIGHT;
    const int rowBytes = (W * 4 + 7) / 8;  // 800 for W=1600, no row padding
    for (int row = 0; row < H; row++) {
        // No flip needed here, unlike renderBinFromHttp's imageY = H-1-fileRow:
        // .bin's wire format stores rows bottom-to-top (see ToBin in
        // joyous-hub/dither.go), so that flip is what UN-inverts it back to
        // top-to-bottom for drawPixel. PNG rows are already stored
        // top-to-bottom by the PNG spec itself, and joyous-hub's
        // EncodeIndexedPNG4 writes hi[] (already top-to-bottom, see FromBin)
        // straight across with no flip of its own -- so PNG row 0 here is
        // already the image's actual top row, same as what drawPixel wants.
        // Confirmed the hard way (2026-08-12): the H-1-row flip that used to
        // be here, copied from renderBinFromHttp without accounting for this
        // difference, rendered every PNG play upside down.
        int imageY = row;
        const unsigned char *rowPtr = raw + (size_t)row * rowBytes;
        for (int col = 0; col < W; col++) {
            uint8_t byte = rowPtr[col / 2];
            uint8_t idx = (col % 2 == 0) ? (byte >> 4) : (byte & 0x0F);
            epaper.drawPixel(col, imageY, paletteIndexToColor(idx));
        }
    }

    lodepng_free(raw);
    lodepng_state_cleanup(&state);
    epaper.update();
    return true;
}

// Same shape as renderBinFromHttp, but for the indexed-PNG format: unlike
// .bin, PNG can't be decoded row-by-row as it streams in (deflate spans
// the whole image), so the whole file is buffered in PSRAM first, then
// handed to renderPngBytes. Used whenever a play's imgurl is .png but
// there's no usable on-device cache to route it through ContentCache
// (either ENABLE_SD_CACHE is off, or the SD addon isn't physically present
// even though the hub thinks this firmware is cache-capable -- see
// main.cpp's handlePlay fallback).
bool renderPngFromHttp(const String &host, int port, const String &path) {
    HTTPClient http;
    String url = "http://" + host + ":" + String(port) + path;
    if (!http.begin(url)) return false;

    int code = http.GET();
    if (code != HTTP_CODE_OK) {
        http.end();
        return false;
    }

    int contentLen = http.getSize();
    if (contentLen <= 0) {
        // Need a known size up front to allocate a whole-file PSRAM buffer;
        // our own hub always sets Content-Length on this route (see
        // joyous-hub's inkjoy_cache.go ServeHTTPExt), so this should only
        // trip on a genuinely malformed response.
        http.end();
        return false;
    }
    uint8_t *buf = (uint8_t *)heap_caps_malloc(contentLen, MALLOC_CAP_SPIRAM);
    if (!buf) {
        http.end();
        return false;
    }

    WiFiClient *stream = http.getStreamPtr();
    int written = 0;
    bool ok = true;
    while (http.connected() && written < contentLen) {
        size_t avail = stream->available();
        if (!avail) {
            if (!http.connected()) break;
            delay(1);
            continue;
        }
        int n = stream->readBytes(buf + written, min((size_t)(contentLen - written), avail));
        if (n <= 0) {
            ok = false;
            break;
        }
        written += n;
    }
    http.end();

    if (!ok || written != contentLen) {
        heap_caps_free(buf);
        return false;
    }

    ok = renderPngBytes(buf, (size_t)contentLen);
    heap_caps_free(buf);
    return ok;
}

} // namespace Panel
#endif // BOARD_SEEED || BOARD_GOODISPLAY
