#include "panel.h"
#include "config.h"
#include <TFT_eSPI.h>
#include <HTTPClient.h>

extern EPaper epaper; // owned by main.cpp

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

void showProvisioning(const String &clientId) {
    epaper.fillScreen(TFT_WHITE);
    epaper.setTextColor(TFT_BLACK, TFT_WHITE);

    epaper.setTextSize(3);
    epaper.setCursor(40, 80);
    epaper.println("Setup mode");

    epaper.setTextSize(2);
    epaper.setCursor(40, 160);
    epaper.println("Adopt this frame from the hub:");
    epaper.setCursor(40, 200);
    epaper.println("look for BLE name");

    epaper.setTextSize(3);
    epaper.setCursor(40, 250);
    epaper.println("IJ_" + clientId);

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

} // namespace Panel
