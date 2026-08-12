#include "config.h"
#ifdef BOARD_GOODISPLAY
#include "panel.h"
#include <HTTPClient.h>
#include <SD_MMC.h>
using fs::File;  // SD_MMC.h, unlike SD.h, does not do "using namespace fs;" itself
#include <driver/gpio.h>
#include <driver/spi_master.h>
#include <string.h>

// Ports GooDisplay's own ESP-IDF reference driver
// (GooDisplay/docs/vendor_code_example_usb_variant/comm.c + GDEP133C02.c)
// directly -- same register sequences, same SPI transaction shape, just
// under our own namespace/pin config instead of the vendor's app_main().
// Arduino-on-ESP32 is built on ESP-IDF, so calling driver/spi_master.h and
// driver/gpio.h from here works the same as it does in the vendor's own code.
//
// Raw buffer<->panel mapping confirmed correct (2026-08-07) via rulerTest()
// + a real photo: buffer (px,py) IS physical (px,py) directly -- top-left
// origin, px increasing right, py increasing down, CS0/CS1 split left/right
// by column (600px each, full 1600-row height -- see writePanelBuffer), no
// swap/mirror needed at that level. What was wrong was this landscape-bin ->
// portrait-panel ROTATION specifically -- see GOODISPLAY_ROTATE's values at
// setPanelPixel for what's been tried. Currently on the last untested option.
#define GOODISPLAY_ROTATE 0

namespace {

// ---- GDEP133C02 command codes (GDEP133C02.h) ----
constexpr uint8_t PSR = 0x00, PWR = 0x01, POF = 0x02, PON = 0x04;
constexpr uint8_t BTST_N = 0x05, BTST_P = 0x06, DTM = 0x10, DRF = 0x12;
constexpr uint8_t CDI = 0x50, TCON = 0x60, TRES = 0x61, AN_TM = 0x74;
constexpr uint8_t AGID = 0x86, BUCK_BOOST_VDDN = 0xB0, TFT_VCOM_POWER = 0xB1;
constexpr uint8_t EN_BUF = 0xB6, BOOST_VDDP_EN = 0xB7, CCSET = 0xE0;
constexpr uint8_t PWS = 0xE3, CMD66 = 0xF0;

// Low nibble of each ink's GDEP133C02.h color byte (0x00/0x11/0x22/0x33/
// 0x55/0x66) -- packed two-per-byte for DTM, so only the low nibble matters.
constexpr uint8_t NIBBLE_BLACK = 0x0, NIBBLE_WHITE = 0x1, NIBBLE_YELLOW = 0x2;
constexpr uint8_t NIBBLE_RED = 0x3, NIBBLE_BLUE = 0x5, NIBBLE_GREEN = 0x6;

// ---- Register value tables (GDEP133C02.c, confirmed vendor values) ----
const uint8_t PSR_V[2] = {0xDF, 0x69};
const uint8_t PWR_V[6] = {0x0F, 0x00, 0x28, 0x2C, 0x28, 0x38};
const uint8_t POF_V[1] = {0x00};
const uint8_t DRF_V[1] = {0x01};
const uint8_t CDI_V[1] = {0xF7};
const uint8_t TCON_V[2] = {0x03, 0x03};
const uint8_t TRES_V[4] = {0x04, 0xB0, 0x03, 0x20};
const uint8_t CMD66_V[6] = {0x49, 0x55, 0x13, 0x5D, 0x05, 0x10};
const uint8_t EN_BUF_V[1] = {0x07};
const uint8_t CCSET_V[1] = {0x01};
const uint8_t PWS_V[1] = {0x22};
const uint8_t AN_TM_V[9] = {0xC0, 0x1E, 0x1E, 0xCE, 0xCE, 0xCE, 0x15, 0x15, 0x55};
const uint8_t AGID_V[1] = {0x10};
const uint8_t BTST_P_V[2] = {0xE8, 0x28};
const uint8_t BOOST_VDDP_EN_V[1] = {0x01};
const uint8_t BTST_N_V[2] = {0xE8, 0x28};
const uint8_t BUCK_BOOST_VDDN_V[1] = {0x01};
const uint8_t TFT_VCOM_POWER_V[1] = {0x02};

const uint8_t kSpiCsPin[2] = {EPD_CS0, EPD_CS1};
spi_device_handle_t g_spi;

// ---- comm.c port: GPIO/SPI transaction primitives ----

void setGpioLevel(uint8_t pin, uint8_t level) { gpio_set_level((gpio_num_t)pin, level); }
uint8_t getGpioLevel(uint8_t pin) { return gpio_get_level((gpio_num_t)pin); }

void initialGpio() {
    gpio_config_t io = {};
    io.pin_bit_mask = (1ULL << EPD_RST) | (1ULL << EPD_CS0) | (1ULL << EPD_CS1) | (1ULL << LOAD_SW);
    io.mode = GPIO_MODE_OUTPUT;
    io.pull_up_en = GPIO_PULLUP_ENABLE;
    gpio_config(&io);

    io = {};
    io.pin_bit_mask = (1ULL << EPD_BUSY);
    io.mode = GPIO_MODE_INPUT;
    io.pull_down_en = GPIO_PULLDOWN_DISABLE;
    io.pull_up_en = GPIO_PULLUP_DISABLE;
    gpio_config(&io);
}

void initialSpi() {
    spi_bus_config_t bus = {};
    bus.data0_io_num = SPI_Data0;
    bus.data1_io_num = SPI_Data1;
    bus.data2_io_num = SPI_Data2;
    bus.data3_io_num = SPI_Data3;
    bus.sclk_io_num = SPI_CLK;
    bus.quadwp_io_num = -1;
    bus.quadhd_io_num = -1;
    bus.flags = SPICOMMON_BUSFLAG_MASTER;
    bus.max_transfer_sz = 32768;

    spi_device_interface_config_t dev = {};
    dev.command_bits = 8;
    dev.clock_speed_hz = SPI_MASTER_FREQ_10M;
    dev.duty_cycle_pos = 128;
    dev.queue_size = 7;
    dev.cs_ena_posttrans = 3;

    ESP_ERROR_CHECK(spi_bus_initialize(SPI3_HOST, &bus, SPI_DMA_CH_AUTO));
    ESP_ERROR_CHECK(spi_bus_add_device(SPI3_HOST, &dev, &g_spi));
}

// Command + (optional) data in one transaction -- register writes. Kept
// single-line: these are tiny (a handful of bytes) and don't need quad
// speed, matching the common 1-1-4 pattern (single-line command/config,
// quad-line bulk data) these panel controllers typically use.
void spiTransmit(uint8_t cmd, const uint8_t *data, size_t len) {
    spi_transaction_t t = {};
    t.cmd = cmd;
    t.length = len * 8;
    t.tx_buffer = data;
    spi_device_transmit(g_spi, &t);
}

void spiTransmitCommand(uint8_t cmd) {
    spi_transaction_t t = {};
    t.cmd = cmd;
    spi_device_transmit(g_spi, &t);
}

// Data-only continuation transfer -- CS already held low, command (DTM)
// already sent once by the caller. Chunks at 32KB like the vendor driver.
//
// Single-line, not QIO (reverted 2026-08-07): the real GDEP133C02 datasheet
// (Section 5.2/6.3.1.1, Table 6-1) confirms the BS0/BS1 strap pins only ever
// select among 3-wire SPI / 4-wire SPI / Standard 4-wire SPI -- all
// single-data-line -- and the panel's default strap state is "Standard
// 4-wire SPI (Default)". SI2/SI3 are documented only as inputs "in Quad
// mode", a sub-feature of Standard 4-wire SPI with no strap combination or
// documented command to enable it anywhere in this datasheet. Both the
// vendor's own working reference (comm.c, which only ever drives 2 lines)
// and Seeed_GFX (confirmed producing correct images on the same silicon on
// real EE02 hardware) exclusively use single-line transfers, never quad.
// The earlier SPI_TRANS_MODE_QIO attempt produced scrambled/tiled output
// (3x-repeated half-glyph pattern) on real hardware -- consistent with
// driving 4 lines while the chip is still only listening on 1. Reverted to
// single-line to match both known-working references.
//
// command_bits=0 override (2026-08-07): dev.command_bits=8 in initialSpi()
// makes ESP-IDF prepend an 8-bit command phase (default 0x00) to EVERY
// transaction on this device by default -- fine for spiTransmit/
// spiTransmitCommand which want that, but each of these per-row
// continuation calls was getting the same spurious extra byte injected
// before its data, shifting that row relative to the last and producing a
// cumulative diagonal shear on real hardware ("horizontal parts of the F
// displayed diagonally, offsets not the same on each line"). The real
// protocol (datasheet p.17, Standard 4-wire write sequence) sends the
// command once, then a plain continuous data stream -- no per-row command
// framing. SPI_TRANS_VARIABLE_CMD + command_bits=0 suppresses it here.
void spiTransmitData(const uint8_t *data, size_t len) {
    constexpr size_t kMax = 32768;
    while (len > 0) {
        size_t chunk = len > kMax ? kMax : len;
        spi_transaction_ext_t t = {};
        t.base.length = chunk * 8;
        t.base.tx_buffer = data;
        t.base.flags = SPI_TRANS_VARIABLE_CMD;
        t.command_bits = 0;
        spi_device_transmit(g_spi, (spi_transaction_t *)&t);
        data += chunk;
        len -= chunk;
    }
}

// ---- GDEP133C02.c port: panel-level operations ----

void resetPin(int level) { setGpioLevel(EPD_RST, level); }
void setPinCsAll(int level) { setGpioLevel(EPD_CS0, level); setGpioLevel(EPD_CS1, level); }
void setPinCs(int csx, int level) { setGpioLevel(kSpiCsPin[csx], level); }

// Bounded + logged, unlike a bare infinite busy-wait -- so a real hang shows
// up as diagnostic output instead of the board just going silent forever.
//
// EPD_BUSY is NOT a reliable "refresh actually complete" signal on this
// board/panel: confirmed on real hardware (2026-08-07) that the physical
// panel finishes its visible refresh well before this ever reads high again
// after DRF -- a 15s timeout AND a 45s timeout both fired with the panel
// already done and BUSY still reading low. The DTM/PON/DRF/POF command
// sequence itself produces a correct render regardless of whether this
// wait ever resolves true, so treat it as best-effort/advisory (a real
// initial-reset failure would still show up as an immediate, obvious
// stuck-low) rather than a hard gate worth blocking long on.
constexpr uint32_t kBusyTimeoutMs = 10000;
bool checkBusyHigh(const char *label = "") {
    uint32_t start = millis();
    uint32_t lastLog = start;
    while (!getGpioLevel(EPD_BUSY)) {
        uint32_t now = millis();
        if (now - start > kBusyTimeoutMs) {
            Serial.printf("panel: BUSY timeout after %lus (%s) -- proceeding anyway, this is expected/non-fatal\n",
                          (unsigned long)(kBusyTimeoutMs / 1000), label);
            return false;
        }
        if (now - lastLog >= 1000) {
            Serial.printf("panel: waiting on BUSY (%s), %lums elapsed\n", label, (unsigned long)(now - start));
            lastLog = now;
        }
        delay(1);
    }
    return true;
}

// Vendor's own diagnostic (GDEP133C02.c checkDriverICStatus, left commented
// out in their app_main() -- reads status register 0xF2 per CS half and
// checks bit 0). Confirms whether the SPI link is actually reaching the
// driver ICs at all, independent of the BUSY-pin question.
void checkDriverICStatus() {
    for (int csx = 0; csx < 2; csx++) {
        uint8_t buf[3] = {0, 0, 0};
        spi_transaction_t t = {};
        t.cmd = 0xF2;
        t.length = sizeof(buf) * 8;
        t.rxlength = sizeof(buf) * 8;
        t.rx_buffer = buf;
        setPinCs(csx, LOW);
        spi_device_transmit(g_spi, &t);
        setPinCs(csx, HIGH);
        Serial.printf("panel: driver IC[%d] status = 0x%02X 0x%02X 0x%02X (%s)\n",
                      csx, buf[0], buf[1], buf[2], (buf[0] & 0x01) ? "ready" : "NOT ready/no reply");
    }
}

void epdHardwareReset() {
    resetPin(LOW);
    delay(20);
    resetPin(HIGH);
    delay(20);
}

void initEPD() {
    epdHardwareReset();
    checkBusyHigh("post-reset");

    setPinCs(0, LOW);
    spiTransmit(AN_TM, AN_TM_V, sizeof(AN_TM_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(CMD66, CMD66_V, sizeof(CMD66_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(PSR, PSR_V, sizeof(PSR_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(CDI, CDI_V, sizeof(CDI_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(TCON, TCON_V, sizeof(TCON_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(AGID, AGID_V, sizeof(AGID_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(PWS, PWS_V, sizeof(PWS_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(CCSET, CCSET_V, sizeof(CCSET_V));
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(TRES, TRES_V, sizeof(TRES_V));
    setPinCsAll(HIGH);

    setPinCs(0, LOW);
    spiTransmit(PWR, PWR_V, sizeof(PWR_V));
    setPinCsAll(HIGH);

    setPinCs(0, LOW);
    spiTransmit(EN_BUF, EN_BUF_V, sizeof(EN_BUF_V));
    setPinCsAll(HIGH);

    setPinCs(0, LOW);
    spiTransmit(BTST_P, BTST_P_V, sizeof(BTST_P_V));
    setPinCsAll(HIGH);

    setPinCs(0, LOW);
    spiTransmit(BOOST_VDDP_EN, BOOST_VDDP_EN_V, sizeof(BOOST_VDDP_EN_V));
    setPinCsAll(HIGH);

    setPinCs(0, LOW);
    spiTransmit(BTST_N, BTST_N_V, sizeof(BTST_N_V));
    setPinCsAll(HIGH);

    setPinCs(0, LOW);
    spiTransmit(BUCK_BOOST_VDDN, BUCK_BOOST_VDDN_V, sizeof(BUCK_BOOST_VDDN_V));
    setPinCsAll(HIGH);

    setPinCs(0, LOW);
    spiTransmit(TFT_VCOM_POWER, TFT_VCOM_POWER_V, sizeof(TFT_VCOM_POWER_V));
    setPinCsAll(HIGH);
}

void epdDisplay() {
    setPinCsAll(LOW);
    spiTransmitCommand(PON);
    checkBusyHigh("PON");
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    delay(30);
    spiTransmit(DRF, DRF_V, sizeof(DRF_V));
    checkBusyHigh("DRF");
    setPinCsAll(HIGH);

    setPinCsAll(LOW);
    spiTransmit(POF, POF_V, sizeof(POF_V));
    checkBusyHigh("POF");
    setPinCsAll(HIGH);
}

// Fills the whole panel with one ink (placeholder status screens -- no text
// rendering pipeline built for this raw driver yet, unlike Seeed's GFXFF
// fonts). Left/right half (600 of 1200 columns, full 1600-row height) per
// CS -- see writePanelBuffer's comment for why.
void fillColor(uint8_t nibble) {
    constexpr int kHalfBytesPerRow = 300;  // 600px / 2px-per-byte
    constexpr int kHeight = 1600;
    uint8_t rowBuf[kHalfBytesPerRow];
    memset(rowBuf, (nibble << 4) | nibble, sizeof(rowBuf));

    for (int csx = 0; csx < 2; csx++) {
        setPinCs(csx, LOW);
        spiTransmitCommand(DTM);
        for (int row = 0; row < kHeight; row++) {
            spiTransmitData(rowBuf, sizeof(rowBuf));
        }
        setPinCs(csx, HIGH);
    }
    epdDisplay();
}

// InkJoy .bin hi byte -> GDEP133C02 nibble code. Same six values joyous-hub's
// dither.go hiBytes[] uses (see panel_epaper.cpp's hiByteToColor for the
// TFT_eSPI-side equivalent).
uint8_t hiByteToNibble(uint8_t hi) {
    switch (hi) {
        case 0x01: return NIBBLE_BLACK;
        case 0x02: return NIBBLE_WHITE;
        case 0x03: return NIBBLE_YELLOW;
        case 0x04: return NIBBLE_RED;
        case 0x06: return NIBBLE_BLUE;
        case 0x07: return NIBBLE_GREEN;
        default:   return NIBBLE_WHITE;
    }
}

// Streams a full-panel 4bpp buffer (1200x1600, 600 bytes/row, 960,000 bytes
// total -- panelBuf[row*600 + col/2], high nibble = even col) to the panel.
//
// CS0/CS1 (M/S) split left/right by column, 600px (300 bytes) each, full
// 1600-row height (reverted back 2026-08-07, second correction): a top/bottom
// split (guessed from reading the datasheet's "G800" scan-diagram label as
// "half of 1600 rows") was tried and tested wrong on real hardware --
// rulerTest() (a two-color tick-mark ruler burned directly into the raw
// buffer, read from an actual photo rather than back-calculated) showed a
// ruler drawn along buffer columns 0-1039 cutting off cleanly around column
// 600 and reappearing shifted down-and-left, which only makes sense if each
// driver IC's real addressable row width is 600px, not 1200. That reframes
// TRES's HRES=1200 as the *combined* panel width (sent identically to both
// ICs via setPinCsAll), not each IC's own resolution -- and reframes
// S2400/G800 as a dual-source/dual-gate addressing detail on a column-split
// panel, not evidence of a row split. Back to left/right, matching the
// original pre-session driver shape.
void writePanelBuffer(const uint8_t *panelBuf) {
    constexpr int kRowBytes = 600;
    constexpr int kHalfBytes = 300;
    constexpr int kHeight = 1600;

    for (int csx = 0; csx < 2; csx++) {
        setPinCs(csx, LOW);
        spiTransmitCommand(DTM);
        for (int row = 0; row < kHeight; row++) {
            const uint8_t *rowStart = panelBuf + row * kRowBytes + csx * kHalfBytes;
            spiTransmitData(rowStart, kHalfBytes);
        }
        setPinCs(csx, HIGH);
    }
    epdDisplay();
}

// Sets one pixel (0-5 palette nibble) into a full-panel 4bpp buffer, mapping
// from the InkJoy .bin's landscape (col, imageY) space into this panel's
// portrait (1200x1600) physical space.
//
// The two prior branches here (px=imageY/py=1599-col, and
// px=1199-imageY/py=col) were each a MIRRORED hybrid -- one axis's formula
// from a CW rotation, the other axis's from CCW -- not a pure rotation
// either way, confirmed by a real render coming out "reflected around the
// diagonal" (2026-08-07). Converting to row-down source coords
// (srcRow=1199-imageY, srcCol=col) and working the two true 90-degree
// rotations: CW is (px=imageY, py=col), CCW is (px=1199-imageY,
// py=1599-col). Neither prior branch was either of these.
// Of the 4 dimensionally-valid axis-mapping options, GOODISPLAY_ROTATE
// picks one:
//   0: px=imageY,          py=col            ("CW")   -> tested: landscape, normal
//   1: px=1199-imageY,     py=1599-col       ("CCW")  -> tested: landscape, upside down
//   2: px=1199-imageY,     py=col                     -> tested: reflected diagonally
//   3: px=imageY,          py=1599-col                -> untested, trying now
void setPanelPixel(uint8_t *panelBuf, int col, int imageY, uint8_t nibble) {
    constexpr int kRowBytes = 600;
#if GOODISPLAY_ROTATE == 0
    int px = imageY;
    int py = col;
#elif GOODISPLAY_ROTATE == 1
    int px = (INKJOY_BIN_HEIGHT - 1) - imageY;
    int py = (INKJOY_BIN_WIDTH - 1) - col;
#elif GOODISPLAY_ROTATE == 2
    int px = (INKJOY_BIN_HEIGHT - 1) - imageY;
    int py = col;
#else
    int px = imageY;
    int py = (INKJOY_BIN_WIDTH - 1) - col;
#endif
    int byteIdx = py * kRowBytes + px / 2;
    if (px % 2 == 0) {
        panelBuf[byteIdx] = (panelBuf[byteIdx] & 0x0F) | (nibble << 4);
    } else {
        panelBuf[byteIdx] = (panelBuf[byteIdx] & 0xF0) | nibble;
    }
}

// Direct physical-buffer pixel set -- px 0..1199 (panel width), py 0..1599
// (panel height, py<800 is CS0's half, py>=800 is CS1's), no landscape
// rotation involved. Used by cornerTest() to diagnose the raw buffer<->panel
// mapping independent of setPanelPixel's still-unverified rotation guess.
void setPhysicalPixel(uint8_t *panelBuf, int px, int py, uint8_t nibble) {
    constexpr int kRowBytes = 600;
    int byteIdx = py * kRowBytes + px / 2;
    if (px % 2 == 0) {
        panelBuf[byteIdx] = (panelBuf[byteIdx] & 0x0F) | (nibble << 4);
    } else {
        panelBuf[byteIdx] = (panelBuf[byteIdx] & 0xF0) | nibble;
    }
}

constexpr size_t kPanelBufBytes = 600UL * 1600UL;  // 960,000 bytes

// ---- Minimal 5x7 bitmap font -- just "F", deliberately asymmetric under
// every rotation/mirror (a checkerboard or solid color looks identical
// either way flipped; "F" doesn't), so a real render is legible proof the
// driver produces correct oriented output, not just "some ink moved."
struct Glyph { uint8_t rows[7]; };  // each row a 5-bit value, bit4 = leftmost column
const Glyph kGlyphF = {{0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10}};

// Draws the glyph PRE-ROTATED in landscape-bin space (row, the glyph's tall
// 7-unit dimension, drives "col"; bit, the glyph's narrow 5-unit dimension,
// drives "imageY") -- the opposite of what you'd naively draw. Necessary
// because setPanelPixel applies a real 90-degree rotation converting
// landscape-bin space into the portrait panel; drawing an already-upright
// glyph directly in landscape coords (the original version of this
// function) just gets rotated sideways again after that transform, which is
// exactly what 4 different rotation-formula attempts all showed empirically
// on real hardware (2026-08-07) -- every one came out landscape-oriented,
// because the bug was here, not in setPanelPixel's formula.
void drawGlyph(uint8_t *panelBuf, const Glyph &g, int col0, int imageY0, int scale, uint8_t nibble) {
    for (int row = 0; row < 7; row++) {
        for (int bit = 0; bit < 5; bit++) {
            if (!(g.rows[row] & (0x10 >> bit))) continue;
            for (int sy = 0; sy < scale; sy++) {
                for (int sx = 0; sx < scale; sx++) {
                    int col = col0 + row * scale + sy;
                    int imageY = imageY0 + bit * scale + sx;
                    if (col >= 0 && col < INKJOY_BIN_WIDTH && imageY >= 0 && imageY < INKJOY_BIN_HEIGHT) {
                        setPanelPixel(panelBuf, col, imageY, nibble);
                    }
                }
            }
        }
    }
}

}  // namespace

namespace Panel {

void begin() {
    initialGpio();
    initialSpi();
    setGpioLevel(LOAD_SW, HIGH);
    epdHardwareReset();
    setPinCsAll(HIGH);
    initEPD();
    checkDriverICStatus();  // diagnostic: confirms SPI is actually reaching the panel ICs
}

void sleep() {
    // GDEP133C02 has no separate low-power command beyond POF (already sent
    // at the end of every epdDisplay()) -- nothing extra needed here yet.
}

// Bring-up diagnostic (2026-08-07): draws one black "F" on a white
// background into a single in-memory buffer, then does exactly ONE real
// panel refresh -- not a fill-then-redraw sequence, one buffer built
// completely before the only writePanelBuffer()/epdDisplay() call. Same
// risk profile as showProvisioning()'s normal one-refresh behavior, not
// the earlier multi-color cycling that very likely brownout-looped the
// board. Legible text is a far more unambiguous result than a solid color:
// readable and right-side-up means the driver is genuinely correct, not
// just "some ink moved," and "F" specifically reveals any
// rotation/mirroring immediately since it has no symmetry to hide behind.
void letterFTest() {
    uint8_t *panelBuf = (uint8_t *)heap_caps_malloc(kPanelBufBytes, MALLOC_CAP_SPIRAM);
    if (!panelBuf) {
        Serial.println("panel: letterFTest: alloc failed");
        return;
    }
    memset(panelBuf, (NIBBLE_WHITE << 4) | NIBBLE_WHITE, kPanelBufBytes);
    constexpr int kScale = 80;  // ~560x400px in landscape-bin space -- large enough to read from across a room
    drawGlyph(panelBuf, kGlyphF, (INKJOY_BIN_WIDTH - 7 * kScale) / 2,
              (INKJOY_BIN_HEIGHT - 5 * kScale) / 2, kScale, NIBBLE_BLACK);
    Serial.println("panel: letterFTest -> single refresh");
    writePanelBuffer(panelBuf);
    free(panelBuf);
    Serial.println("panel: letterFTest done");
}

// Bring-up diagnostic (2026-08-07, v2): the four-square version produced an
// unreadable, overlapping mess (rectangles instead of squares, overlapping
// blocks) -- too much simultaneous ambiguity to interpret verbally. Replaced
// with a single L-shaped marker, both arms anchored at buffer (px=0,py=0),
// unequal lengths so it can't be confused under any rotation/mirror:
//   - long arm (300px) runs along +px, 50px thick
//   - short arm (200px) runs along +py, 50px thick
// The corner where the two arms meet is buffer (0,0). Whichever physical
// arm is longer tells you which buffer axis (px or py) that physical
// direction is; the meeting corner tells you where buffer (0,0) physically
// is. One refresh, one unambiguous shape, no landscape rotation involved
// (setPhysicalPixel, not setPanelPixel).
void cornerTest() {
    uint8_t *panelBuf = (uint8_t *)heap_caps_malloc(kPanelBufBytes, MALLOC_CAP_SPIRAM);
    if (!panelBuf) {
        Serial.println("panel: cornerTest: alloc failed");
        return;
    }
    memset(panelBuf, (NIBBLE_WHITE << 4) | NIBBLE_WHITE, kPanelBufBytes);

    auto fillRect = [&](int px0, int py0, int w, int h) {
        for (int y = 0; y < h; y++) {
            for (int x = 0; x < w; x++) {
                setPhysicalPixel(panelBuf, px0 + x, py0 + y, NIBBLE_BLACK);
            }
        }
    };
    fillRect(0, 0, 300, 50);  // long arm along +px
    fillRect(0, 0, 50, 200);  // short arm along +py

    Serial.println("panel: cornerTest -> single refresh");
    writePanelBuffer(panelBuf);
    free(panelBuf);
    Serial.println("panel: cornerTest done");
}

// Bring-up diagnostic (2026-08-07): probes whether each CS half's real
// addressable row width is wider than the 1200px (600 bytes) we've assumed
// from TRES (HRES=0x04B0=1200). The datasheet's panel-scan diagram (p.9)
// labels the source-line axis "S2400" -- exactly 2x 1200 -- which could
// mean 2 source drives per logical column (our assumption, giving 1200 real
// addressable columns per half) OR could mean each half is actually
// addressable to 2400 raw columns some other way. Directly tests it: CS0's
// row 0 gets the normal 600 bytes (1200px, all white) PLUS 50 extra black
// bytes (100px) immediately after -- past where column 1200 would be if our
// assumption is right. A second, ordinary black square is placed at
// px 300-349/py 400-449 purely as a canary: if the 50 extra bytes throw off
// the driver's internal row addressing (i.e. real per-row stride does match
// our 600-byte assumption and the extra bytes bleed into what we think is
// row 1), this square will appear shifted -- if it appears exactly where
// placed, the extra bytes were absorbed without disturbing later rows.
void wideRowProbe() {
    constexpr int kRowBytes = 600;
    constexpr int kHalfHeight = 800;
    uint8_t whiteRow[kRowBytes];
    memset(whiteRow, (NIBBLE_WHITE << 4) | NIBBLE_WHITE, sizeof(whiteRow));
    uint8_t extra[50];
    memset(extra, (NIBBLE_BLACK << 4) | NIBBLE_BLACK, sizeof(extra));

    uint8_t *panelBuf = (uint8_t *)heap_caps_malloc(kPanelBufBytes, MALLOC_CAP_SPIRAM);
    if (!panelBuf) {
        Serial.println("panel: wideRowProbe: alloc failed");
        return;
    }
    memset(panelBuf, (NIBBLE_WHITE << 4) | NIBBLE_WHITE, kPanelBufBytes);
    for (int y = 400; y < 450; y++) {
        for (int x = 300; x < 350; x++) {
            setPhysicalPixel(panelBuf, x, y, NIBBLE_BLACK);
        }
    }

    setPinCs(0, LOW);
    spiTransmitCommand(DTM);
    spiTransmitData(whiteRow, sizeof(whiteRow));
    spiTransmitData(extra, sizeof(extra));  // the probe: 100px worth of black right past column 1200
    for (int row = 1; row < kHalfHeight; row++) {
        spiTransmitData(panelBuf + row * kRowBytes, kRowBytes);
    }
    setPinCs(0, HIGH);

    setPinCs(1, LOW);
    spiTransmitCommand(DTM);
    for (int row = 0; row < kHalfHeight; row++) {
        spiTransmitData(panelBuf + (kHalfHeight + row) * kRowBytes, kRowBytes);
    }
    setPinCs(1, HIGH);

    Serial.println("panel: wideRowProbe -> single refresh");
    epdDisplay();
    free(panelBuf);
    Serial.println("panel: wideRowProbe done");
}

// Bring-up diagnostic (2026-08-07): wideRowProbe()'s single small canary
// mark landed somewhere that didn't cleanly fit either the identity-mapping
// or the widened-row hypothesis when measured from a photo -- back-
// calculating exact pixel percentages from a hand-held photo turned out too
// noisy to trust. This replaces precise measurement with something readable
// by eye alone: two colored "rulers" using setPhysicalPixel (raw buffer
// coords, no rotation logic), each made of ticks that grow in the direction
// of increasing coordinate:
//   - BLACK ticks along the buffer's top edge (py near 0), one every 200
//     px, height growing 20/40/60/80/100/120 as px increases 0->1000.
//     Whichever physical direction these ticks get taller reveals which way
//     is +px, and the corner where the shortest tick sits is buffer px=0.
//   - RED ticks along the buffer's left edge (px near 0), one every 200 py,
//     width growing 20/40/...160 as py increases 0->1400 -- spanning BOTH
//     CS0 (py<800) and CS1 (py>=800) halves in one continuous run, so any
//     gap/jump/misalignment where it crosses py=800 directly shows whether
//     the top/bottom CS split is correctly stitched together.
// No pixel-counting needed -- just report which way each ruler grows and
// whether the red one looks continuous or broken in the middle.
void rulerTest() {
    uint8_t *panelBuf = (uint8_t *)heap_caps_malloc(kPanelBufBytes, MALLOC_CAP_SPIRAM);
    if (!panelBuf) {
        Serial.println("panel: rulerTest: alloc failed");
        return;
    }
    memset(panelBuf, (NIBBLE_WHITE << 4) | NIBBLE_WHITE, kPanelBufBytes);

    for (int k = 0; k < 6; k++) {
        int px0 = k * 200;
        int height = 20 * (k + 1);
        for (int y = 0; y < height; y++) {
            for (int x = 0; x < 40; x++) {
                setPhysicalPixel(panelBuf, px0 + x, y, NIBBLE_BLACK);
            }
        }
    }
    for (int k = 0; k < 8; k++) {
        int py0 = k * 200;
        int width = 20 * (k + 1);
        for (int y = 0; y < 40; y++) {
            for (int x = 0; x < width; x++) {
                setPhysicalPixel(panelBuf, x, py0 + y, NIBBLE_RED);
            }
        }
    }

    Serial.println("panel: rulerTest -> single refresh");
    writePanelBuffer(panelBuf);
    free(panelBuf);
    Serial.println("panel: rulerTest done");
}

// No text-rendering pipeline built for this raw driver yet (unlike Seeed's
// Seeed_GFX/GFXFF fonts) -- these render solid-color placeholder screens so
// at least *something* distinguishable shows up during provisioning/setup,
// logged to Serial for the real detail. Real status text is future work.
void showProvisioning(const String &clientId) {
    Serial.printf("panel: provisioning as IJ_%s (no-text placeholder: yellow)\n", clientId.c_str());
    fillColor(NIBBLE_YELLOW);
}
void showOrientation(bool portrait) {
    Serial.printf("panel: orientation=%s (no button hardware on this board yet)\n", portrait ? "portrait" : "landscape");
}
void showFactoryReset() {
    Serial.println("panel: factory reset (no-text placeholder: blue)");
    fillColor(NIBBLE_BLUE);
}
void showConnected() {
    Serial.println("panel: connected (no-text placeholder: green)");
    fillColor(NIBBLE_GREEN);
}
void showLowBattery() {
    Serial.println("panel: low battery (no-text placeholder: red)");
    fillColor(NIBBLE_RED);
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

    uint8_t *panelBuf = (uint8_t *)heap_caps_malloc(kPanelBufBytes, MALLOC_CAP_SPIRAM);
    if (!panelBuf) {
        http.end();
        return false;
    }

    int nextPct = 20;
    for (int fileRow = 0; fileRow < H; fileRow++) {
        int got = 0;
        while (got < W * 2) {
            int n = stream->readBytes(rowBuf + got, W * 2 - got);
            if (n <= 0) {
                http.end();
                free(panelBuf);
                return false;
            }
            got += n;
        }
        int imageY = H - 1 - fileRow;  // bin rows are stored bottom-to-top
        for (int col = 0; col < W; col++) {
            setPanelPixel(panelBuf, col, imageY, hiByteToNibble(rowBuf[col * 2]));
        }
        if (nextPct <= 80 && fileRow >= (H * nextPct) / 100) {
            if (onProgress) onProgress(nextPct);
            nextPct += 20;
        }
    }
    http.end();

    writePanelBuffer(panelBuf);
    free(panelBuf);
    return true;
}

bool renderBinFromFile(const String &path) {
#if ENABLE_SD_CACHE
    File f = SD_MMC.open(path, FILE_READ);
    if (!f) return false;

    const int W = INKJOY_BIN_WIDTH;
    const int H = INKJOY_BIN_HEIGHT;
    static uint8_t rowBuf[INKJOY_BIN_WIDTH * 2];

    uint8_t *panelBuf = (uint8_t *)heap_caps_malloc(kPanelBufBytes, MALLOC_CAP_SPIRAM);
    if (!panelBuf) {
        f.close();
        return false;
    }

    for (int fileRow = 0; fileRow < H; fileRow++) {
        int got = 0;
        while (got < W * 2) {
            int n = f.read(rowBuf + got, W * 2 - got);
            if (n <= 0) {
                f.close();
                free(panelBuf);
                return false;
            }
            got += n;
        }
        int imageY = H - 1 - fileRow;
        for (int col = 0; col < W; col++) {
            setPanelPixel(panelBuf, col, imageY, hiByteToNibble(rowBuf[col * 2]));
        }
    }
    f.close();

    writePanelBuffer(panelBuf);
    free(panelBuf);
    return true;
#else
    (void)path;
    return false;  // no SD support on this board yet
#endif
}

}  // namespace Panel
#endif  // BOARD_GOODISPLAY
