#pragma once

// Force-included (see platformio.ini's goodisplay_c02 env: -DUSER_SETUP_LOADED=1
// -include include/tft_setup_goodisplay.h) instead of adding a new
// BOARD_SCREEN_COMBO number to Seeed_GFX's own Dynamic_Setup.h -- that file
// lives in a third-party library PlatformIO re-downloads into .pio/libdeps/,
// so editing it directly wouldn't survive a clean rebuild or a fresh clone.
// TFT_eSPI.h skips its whole User_Setup_Select.h dispatch when
// USER_SETUP_LOADED is already defined, so this file supplies every macro
// that dispatch would otherwise have provided, without touching the library.
//
// T133A01_DRIVER: GooDisplay's GDEP133C02 and Seeed's "T133A01" are the same
// underlying driver silicon under two different vendor/panel-integrator
// names -- confirmed by comparing register maps directly (both use e.g.
// PSR=0x00, PWR=0x01, POF=0x02, PON=0x04, DTM=0x10, DRF=0x12, CDI=0x50,
// TRES=0x61, and both are EPD_WIDTH=1600/EPD_HEIGHT=1200,
// USE_COLORFULL_EPAPER, 4bpp). Pin values are GooDisplay's own confirmed
// mapping (GooDisplay/docs/vendor_code_example_usb_variant/pindefine.h).
#define USER_SETUP_ID 9999  // no real Setup5xx file backs this; not dispatched via Dynamic_Setup.h

#define T133A01_DRIVER
#define EPAPER_ENABLE

#define TFT_WIDTH 1200
#define TFT_HEIGHT 1600
#define EPD_WIDTH TFT_WIDTH
#define EPD_HEIGHT TFT_HEIGHT

#define TFT_SCLK 9   // SPI_CLK
#define TFT_MISO 40  // SPI_Data1 (comm.c's data1_io_num -- standard MISO despite the vendor's "dual data line" naming; no transaction ever sets a real 2-bit DIO transfer flag, so electrically this is plain 4-wire SPI)
#define TFT_MOSI 41  // SPI_Data0
#define TFT_CS   18  // EPD_CS0 / vendor's SPI_CS0 -- drives the panel's "M" (main) half
#define TFT_CS1  17  // EPD_CS1 / vendor's SPI_CS1 -- drives the panel's "S" (sub) half
// TFT_DC: the board actually has a real, dedicated DC/X breakout on the
// panel connector (H1) -- traced by hand (2026-08-08) from H1's DC/X pad
// through a via near U6, to the back of the board, across to the opposite
// side of the ESP32-S3-WROOM-1 module, landing on the 3rd pin down the
// right-hand column from the antenna/keepout end. Cross-referenced against
// Espressif's real WROOM-1 datasheet (Figure 3-1, Table 3-1): that column,
// top to bottom, is pin40/GND, pin39/IO1, pin38/IO2, pin37/TXD0(GPIO43),
// pin36/RXD0(GPIO44) -- and confirmed against which of those positions
// actually have a routed pad on this board (2nd and 3rd do, 4th and 5th
// don't, matching TXD0/RXD0 being used internally for the onboard
// USB-serial bridge instead of broken out separately) -- so DC/X is GPIO2.
// A real traced pin beats both prior guesses (TFT_RST, which was the
// original DC/RST aliasing bug, and SPI_Data3, a real panel pin but reused
// on unverified "should be a no-op" reasoning rather than confirmed as the
// board's actual DC/X route).
#define TFT_DC   2
#define TFT_BUSY 7   // EPD_BUSY
#define TFT_RST  6   // EPD_RST
#define TFT_ENABLE 45  // LOAD_SW

#define LOAD_GLCD
#define LOAD_FONT2
#define LOAD_FONT4
#define LOAD_FONT6
#define LOAD_FONT7
#define LOAD_FONT8
#define LOAD_GFXFF
#define SMOOTH_FONT

// Matches GDEP133C02's own comm.c (SPI_MASTER_FREQ_10M) exactly -- not
// using XIAO_SPI_Frequency.h's ARDUINO_XIAO_ESP32S3 branch since this board
// isn't a XIAO (generic esp32-s3-devkitc-1 target), and this is board-
// specific timing anyway, not board-family-specific.
#define SPI_FREQUENCY 10000000
#define SPI_READ_FREQUENCY 4000000
