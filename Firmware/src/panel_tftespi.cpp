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
// FreeSansBold24pt7b / FreeSans12pt7b (Adafruit's stock bundled set) come
// from TFT_eSPI.h -> gfxfont.h, which #includes all 48 GFXFF fonts
// unconditionally when LOAD_GFXFF is set. GoRegular*/Caveat39pt7b below are
// ours -- not part of that bundle -- so they need an explicit #include.
// lodepng's PSRAM-backed allocator overrides live in
// lib/lodepng/lodepng_psram_allocator.cpp, not here -- see that file's
// comment for why they can't live in our own .cpp files.
#include "fonts/GoRegular23pt7b.h"
#include "fonts/GoRegular30pt7b.h"
#include "fonts/GoRegular42pt7b.h"
#include "fonts/Caveat39pt7b.h"
// GoRegular30pt7b/Caveat39pt7b aren't referenced by any status screen yet --
// staged for on-device weather/caption rendering (matching joyous-hub's
// "medium" line size and photo-name caption face respectively) discussed
// alongside this conversion but not yet implemented. Reference them
// directly via epaper.setFreeFont(&GoRegular30pt7b)/&Caveat39pt7b when that
// lands, same as titleFont()/bodyFont() do below for the other two sizes.

extern EPaper epaper; // owned by main.cpp

// GT32L32S0140 font-chip footprint on Seeed's schematic (U6, SPI1) is
// present but unpopulated on that board rev -- confirmed against the
// physical unit, so there's no on-board glyph ROM to draw from there.
// GooDisplay's board isn't known to have an equivalent font chip either.
// GFXFF vector fonts are the best available quality on either board: real
// glyphs at their point size instead of a setTextSize()-scaled GLCD font.
// Note this is strictly 1-bit rendering (TFT_eSPI's GFXFF drawChar path is
// plain drawFastHLine/fillRect runs off a monochrome bitmap) -- SMOOTH_FONT
// in tft_setup_goodisplay.h only applies to the separate, unused .vlw
// "Smooth_font" loader, which nothing here calls.
//
// Go Regular, not Adafruit's stock FreeSans -- same face joyous-hub uses
// for its own overlay text (see joyous-hub/overlay_render.go), converted
// at 42pt/23pt via Adafruit's fontconvert (see include/fonts/*.h's own
// comments for exact provenance, including why these aren't hub's
// exactly-scaled 82pt/44pt -- fontconvert's DPI default doesn't match
// hub's) so on-device status screens and hub-rendered photo overlays
// read as the same typeface family, at the same real size.
static void titleFont() { epaper.setFreeFont(&GoRegular42pt7b); }
static void bodyFont() { epaper.setFreeFont(&GoRegular23pt7b); }
static void mediumFont() { epaper.setFreeFont(&GoRegular30pt7b); }
static void captionFont() { epaper.setFreeFont(&Caveat39pt7b); }

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

// Centered title + up to 2 body lines, spaced off the active fonts' own
// fontHeight() (== GFXfont.yAdvance -- see TFT_eSPI::fontHeight()) rather
// than fixed pixel gaps. Fixed gaps tuned for FreeSans's small yAdvance
// (title 56, body 29) silently became too tight once these screens moved
// to GoRegular42pt7b/GoRegular23pt7b (yAdvance 95/52) to unify with
// joyous-hub's typeface -- confirmed on real hardware, 2026-08-12: body
// lines overlapped/clipped. Deriving from fontHeight() means this can't
// drift out of sync with whatever font sizes end up in fonts/*.h again.
// portrait -- draws upright in the device's current orientation preference
// rather than always the fixed mount rotation, same setRotation(0)
// toggle-and-restore technique showOrientation() already established
// (and renderPngBytes's nativeDims branch reuses for actual images).
static void drawCenteredStatus(const String &title, const String &body1, bool portrait, const String &body2 = String()) {
    if (portrait) {
        epaper.setRotation(0);
    }
    const int16_t w = epaper.width();
    const int16_t h = epaper.height();
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);
    epaper.setTextDatum(MC_DATUM);

    titleFont();
    int titleH = epaper.fontHeight();
    epaper.drawString(title, w / 2, h / 2 - titleH / 2 - 10);

    bodyFont();
    int bodyH = epaper.fontHeight();
    int bodyY = h / 2 + bodyH / 2 + 10;
    epaper.drawString(body1, w / 2, bodyY);
    if (body2.length()) {
        epaper.drawString(body2, w / 2, bodyY + bodyH);
    }

    epaper.setTextDatum(TL_DATUM);
    epaper.setFreeFont(nullptr);
    epaper.update();
    if (portrait) {
        epaper.setRotation(PANEL_ROTATION);
    }
}

void showProvisioning(const String &clientId) {
    // fillSprite, not fillScreen: with setRotation(1) on a 4bpp EPaper sprite,
    // fillScreen uses unswapped TFT _width/_height and only clears 1200×1200 of
    // the 1200×1600 buffer — bottom quarter keeps the previous image.
    epaper.fillSprite(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);

    // TL_DATUM (default): y is each line's top. Spacing below derived from
    // each font's own fontHeight() -- see drawCenteredStatus()'s comment.
    titleFont();
    int titleH = epaper.fontHeight();
    epaper.drawString("Setup mode", 40, 100);

    bodyFont();
    int bodyH = epaper.fontHeight();
    int y = 100 + titleH + 20;
    epaper.drawString("Adopt this frame from the hub:", 40, y);
    y += bodyH;
    epaper.drawString("look for BLE name", 40, y);

    titleFont();
    y += bodyH + 20;
    epaper.drawString("IJ_" + clientId, 40, y);

    epaper.setFreeFont(nullptr);
    epaper.update();
}

void showOrientation(bool portrait) {
    // Toast only — hub Portrait crop updates on the next encoded send.
    // Portrait text is drawn in native portrait coords (90° from PANEL_ROTATION)
    // so it reads upright when the frame is hung tall; mount rotation restored after
    // -- drawCenteredStatus's own portrait param now does this same toggle,
    // originally established right here.
    drawCenteredStatus(portrait ? "Portrait" : "Landscape",
                        "Hub crop preference saved", portrait, "Next send uses this crop");
}

void showMode(bool offline, bool portrait) {
    drawCenteredStatus(offline ? "Offline mode" : "Online mode",
                        offline ? "Playing from SD playlist" : "Waiting for hub", portrait);
}

void showFactoryReset(bool portrait) {
    drawCenteredStatus("Factory reset", "Credentials cleared", portrait, "Rebooting to setup mode");
}

void showConnected(bool portrait) {
    drawCenteredStatus("Connected", "Ready to receive images", portrait, "from Joyous Hub");
}

void showLowBattery(bool portrait) {
    drawCenteredStatus("Critical Low Battery", "Connect USB to power supply", portrait);
}

void drawDegreeGlyph(int x, int y, int fontPt, uint16_t color) {
    // Radius/placement scaled off point size the same way joyous-hub scales
    // its rain-cloud icon off fontPx (overlayIconSize in overlay_icons.go)
    // -- both exist so a hand-drawn glyph reads as roughly font-sized next
    // to real text, not a fixed size that only looks right at one scale.
    // y is the text baseline; the ring sits at cap-height,
    // offset up by roughly the font's ascent.
    int r = max(2, fontPt / 10);
    epaper.drawCircle(x + r, y - fontPt * 3 / 4, r, color);
}

// Returns false (font unset) for an unrecognized role -- caller treats that
// as a hard failure, see drawOverlayLines()'s own comment.
static bool overlayLineFont(const String &font) {
    if (font == "large") { titleFont(); return true; }
    if (font == "medium") { mediumFont(); return true; }
    if (font == "caption") { captionFont(); return true; }
    return false;
}

// White fill + 8-direction black outline, matching joyous-hub's own
// drawBorderedOverlayText (overlay_render.go) -- plain single-color text
// read poorly against a busy dithered photo in the very first real-hardware
// test of this feature (2026-08-13); an outline is what actually makes
// hub-baked overlay text legible on an arbitrary background, so the
// on-device comparison needs it too, not just position/size parity.
// 2px step, not 1px: matches hub's own overlayTextOutlineStep(), which
// steps up to 2px once fontSize>=58 -- a first pass here wrongly assumed
// none of our roles crossed that threshold by comparing against the base
// pt constants (56/40/52) instead of the real rendered pixel sizes
// (~82/59/76, all >=58 -- see include/fonts/*.h's own provenance
// comments), so hub uses 2px for all three roles we ever draw. 1px
// under-stroked relative to that, visibly leaving white showing through
// at glyph curves/diagonals on real hardware (2026-08-13).
constexpr int kOverlayOutlineStepPx = 2;

// Empirical correction for hub and firmware disagreeing on what "ascent"
// means for the same nominal font: hub's Go rendering positions text using
// the font's *design* ascent metric (face.Metrics().Ascent), while this
// GFXFF path positions using glyph_ab -- TFT_eSPI's own computed *maximum
// actual ink extent* across the converted glyph set, a different, smaller
// quantity for most fonts. Both are legitimate, correct-for-their-own-
// convention values, not a bug in either renderer -- confirmed against the
// hub's own baked-in overlay (the ground truth) on real hardware, which
// showed this GFXFF path landing too high, uniformly across every line;
// first measured ~5px (2026-08-13), refined to 8px after a second real-
// hardware check. Belongs here, not as a hub-side wire-coordinate fudge,
// since it's specific to this rendering path's own ascent convention, not
// something hub's device-agnostic layout math should need to know about.
constexpr int kOverlayYCalibrationPx = 8;

static void drawOverlayLineStroked(const String &text, int x, int y) {
    y += kOverlayYCalibrationPx;
    static const int8_t kOffsets[8][2] = {
        {-kOverlayOutlineStepPx, 0}, {kOverlayOutlineStepPx, 0},
        {0, -kOverlayOutlineStepPx}, {0, kOverlayOutlineStepPx},
        {-kOverlayOutlineStepPx, -kOverlayOutlineStepPx}, {-kOverlayOutlineStepPx, kOverlayOutlineStepPx},
        {kOverlayOutlineStepPx, -kOverlayOutlineStepPx}, {kOverlayOutlineStepPx, kOverlayOutlineStepPx},
    };
    epaper.setTextColor(TFT_BLACK);
    for (auto &o : kOffsets) {
        epaper.drawString(text, x + o[0], y + o[1]);
    }
    epaper.setTextColor(TFT_WHITE);
    epaper.drawString(text, x, y);
}

// Draws overlay text into whatever's already in the sprite buffer, but
// does NOT call epaper.update() -- shared by every render*() function
// (each draws its image, then this, then its own single epaper.update())
// and by the public drawOverlayLines() below (image already on screen,
// draws this then updates itself). One implementation, one place that
// knows how overlay text gets drawn; two callers with different ideas of
// when the actual refresh should happen.
static bool drawOverlayLinesNoUpdate(const OverlayLine *lines, int count, const OverlayLine *caption) {
    if (count == 0 && caption == nullptr) return true;  // nothing to draw
    // No fillSprite() -- overlay draws on top of whatever's already in the
    // buffer (the image render*() just did, or whatever was last shown,
    // for the standalone drawOverlayLines() caller). Transparent glyphs
    // throughout: the single-arg setTextColor() means "background not
    // over-written" (TFT_eSPI.h's own doc comment on that overload),
    // unlike the two-arg form every status screen above uses, which would
    // opaque-fill each glyph's cell and clobber the photo underneath.
    // Default TL_DATUM, no setTextDatum()/padX call -- required for the
    // top-left x/y convention the hub already computes its coordinates in
    // (confirmed against TFT_eSPI::drawString's GFXFF path: poY += ascent
    // always runs before the textdatum switch, and that switch is skipped
    // entirely at the default TL_DATUM/padX=0, so passing "top of line" as
    // y and letting the library add ascent internally is exactly what
    // already happens with no further adjustment needed).
    bool ok = true;
    for (int i = 0; i < count; i++) {
        if (!overlayLineFont(lines[i].font)) { ok = false; break; }
        drawOverlayLineStroked(lines[i].text, lines[i].x, lines[i].y);
    }
    if (ok && caption != nullptr) {
        if (!overlayLineFont(caption->font)) {
            ok = false;
        } else {
            drawOverlayLineStroked(caption->text, caption->x, caption->y);
        }
    }
    epaper.setFreeFont(nullptr);
    return ok;
}

bool drawOverlayLines(const OverlayLine *lines, int count, const OverlayLine *caption) {
    if (!drawOverlayLinesNoUpdate(lines, count, caption)) return false;
    epaper.update();
    return true;
}

bool renderBinFromHttp(const String &host, int port, const String &path, void (*onProgress)(int),
                        const OverlayLine *lines, int lineCount, const OverlayLine *caption) {
    HTTPClient http;
    String url = "http://" + host + ":" + String(port) + path;
    if (!http.begin(url)) return false;

    int code = http.GET();
    if (code != HTTP_CODE_OK) {
        http.end();
        return false;
    }

    WiFiClient *stream = http.getStreamPtr();
    // Always the fixed shape -- .bin has no header to self-describe
    // dimensions, so a native-dims (swapped) send can only ever ride the
    // PNG protocol (see renderPngBytes's nativeDims handling); real InkJoy
    // hardware and any clone firmware below InkJoyNativeDimsProtocolT only
    // ever receives this fixed shape via either protocol anyway.
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
    drawOverlayLinesNoUpdate(lines, lineCount, caption);  // ignored on failure -- image already succeeded
    epaper.update();
    return true;
}

// Legacy streaming .bin decode, factored out so both renderBinFromFile
// (path known to be .bin, by extension) and renderBinFromHttp share it
// instead of duplicating the row-by-row loop.
static bool renderBinStream(Stream &in) {
    // Always the fixed shape -- see renderBinFromHttp's comment on this
    // same point; .bin has no header, so it never carries native (swapped)
    // dims.
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

bool renderBinFromFile(const String &path,
                        const OverlayLine *lines, int lineCount, const OverlayLine *caption) {
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
        ok = (got == len) && renderPngBytes(buf, len, lines, lineCount, caption);
        heap_caps_free(buf);
        return ok;  // renderPngBytes() already draws overlay + calls epaper.update() on success
    }

    ok = renderBinStream(f);
    f.close();
    if (ok) {
        drawOverlayLinesNoUpdate(lines, lineCount, caption);  // ignored on failure -- image already succeeded
        epaper.update();
    }
    return ok;
#else
    (void)path;
    (void)lines;
    (void)lineCount;
    (void)caption;
    return false;  // no SD support on this board yet
#endif
}

bool renderBinBytes(const uint8_t *bytes, size_t len,
                     const OverlayLine *lines, int lineCount, const OverlayLine *caption) {
    // Always the fixed shape -- see renderBinFromHttp's comment on this
    // same point; .bin has no header, so it never carries native (swapped)
    // dims.
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
    drawOverlayLinesNoUpdate(lines, lineCount, caption);  // ignored on failure -- image already succeeded
    epaper.update();
    return true;
}

bool renderPngBytes(const uint8_t *bytes, size_t len,
                     const OverlayLine *lines, int lineCount, const OverlayLine *caption) {
    LodePNGState state;
    lodepng_state_init(&state);
    state.decoder.color_convert = 0;  // keep the file's own raw indexed data

    unsigned char *raw = nullptr;
    unsigned width = 0, height = 0;
    unsigned err = lodepng_decode(&raw, &width, &height, &state, bytes, len);
    // A native-dims send (our own Seeed/GooDisplay clones, portrait content,
    // hub gated on inkjoybridge.InkJoyNativeDimsProtocolT) arrives swapped
    // -- INKJOY_BIN_HEIGHT x INKJOY_BIN_WIDTH -- since the hub skips the
    // rotate90CCW-into-fixed-shape step entirely for these devices; real
    // InkJoy hardware and any clone below that protocol threshold only ever
    // sends the fixed INKJOY_BIN_WIDTH x INKJOY_BIN_HEIGHT shape.
    bool nativeDims = (width == INKJOY_BIN_HEIGHT && height == INKJOY_BIN_WIDTH);
    bool ok = (err == 0) && raw &&
              ((width == INKJOY_BIN_WIDTH && height == INKJOY_BIN_HEIGHT) || nativeDims) &&
              state.info_png.color.colortype == LCT_PALETTE &&
              state.info_png.color.bitdepth == 4;
    if (!ok) {
        Serial.printf("panel: renderPngBytes: decode failed (err=%u w=%u h=%u)\n", err, width, height);
        if (raw) lodepng_free(raw);
        lodepng_state_cleanup(&state);
        return false;
    }

    // setRotation(0) here mirrors showOrientation()'s own proven
    // toggle-and-restore pattern: at rotation 0, TFT_eSprite::drawPixel
    // applies no coordinate remap at all (4bpp branch, Seeed_GFX's
    // Sprite.cpp), so raw (col,imageY) below lands directly in the panel's
    // native 1200x1600 portrait buffer -- exactly matching a native-dims
    // decoded image with zero rotation math on either side.
    if (nativeDims) {
        epaper.setRotation(0);
    }

    const int W = (int)width;
    const int H = (int)height;
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
    // drawOverlayLinesNoUpdate draws at whatever rotation is currently
    // active -- called here, before restoring PANEL_ROTATION, so native-dims
    // overlay text (already computed hub-side in the same identity/upright
    // coordinate space) lands correctly with no transform on either side.
    drawOverlayLinesNoUpdate(lines, lineCount, caption);  // ignored on failure -- image already succeeded
    epaper.update();
    if (nativeDims) {
        epaper.setRotation(PANEL_ROTATION);
    }
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
bool renderPngFromHttp(const String &host, int port, const String &path,
                        const OverlayLine *lines, int lineCount, const OverlayLine *caption) {
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

    ok = renderPngBytes(buf, (size_t)contentLen, lines, lineCount, caption);
    heap_caps_free(buf);
    return ok;
}

} // namespace Panel
#endif // BOARD_SEEED || BOARD_GOODISPLAY
