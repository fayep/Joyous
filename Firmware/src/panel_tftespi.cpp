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
#include <SD.h>
// FreeSansBold24pt7b / FreeSans12pt7b come from TFT_eSPI.h -> gfxfont.h,
// which #includes all 48 GFXFF fonts unconditionally when LOAD_GFXFF is set.

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

bool renderBinFromFile(const String &path) {
#if ENABLE_SD_CACHE
    File f = SD.open(path, FILE_READ);
    if (!f) return false;

    const int W = INKJOY_BIN_WIDTH;
    const int H = INKJOY_BIN_HEIGHT;
    static uint8_t rowBuf[INKJOY_BIN_WIDTH * 2];

    for (int fileRow = 0; fileRow < H; fileRow++) {
        int got = 0;
        while (got < W * 2) {
            int n = f.read(rowBuf + got, W * 2 - got);
            if (n <= 0) {
                f.close();
                return false;
            }
            got += n;
        }
        int imageY = H - 1 - fileRow; // bin rows are stored bottom-to-top
        for (int col = 0; col < W; col++) {
            uint8_t hi = rowBuf[col * 2];
            epaper.drawPixel(col, imageY, hiByteToColor(hi));
        }
    }

    f.close();
    epaper.update();
    return true;
#else
    (void)path;
    return false;  // no SD support on this board yet
#endif
}

} // namespace Panel
#endif // BOARD_SEEED || BOARD_GOODISPLAY
