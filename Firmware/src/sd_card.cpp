#include "config.h"
#if ENABLE_SD_CACHE
#include "sd_card.h"
#include <SD_MMC.h>

namespace SdCard {

// Native SDMMC, not SPI-mode SD -- switched 2026-08-12 after finding
// GooDisplay's own "Burn firmware to SD card" utility (extracted from
// their product page's downloads, sd.bin) uses the native SDMMC
// peripheral (SDMMC_CLK/CMD/D0-D3 strings in its binary), not SPI, to
// access the same physical card slot on this same board. SPI mode had
// been persistently, intermittently unreliable (mount failures on fresh
// boots with nothing else running, not explained by any of the software
// bugs found and fixed alongside this) -- consistent with the board's SD
// slot electrically/pull-up-wise being designed around SDMMC, which the
// vendor's own firmware confirms actually works, rather than SPI, which
// was never vendor-verified on this hardware. SD_SPI_SCLK_PIN/
// SD_SPI_MOSI_PIN/SD_SPI_MISO_PIN/SD_CS_PIN (the board spec's SD_SCK/
// SD_MOSI/SD_MISO/SD_CS) map directly onto SDMMC's CLK/CMD/D0/D3 -- these
// are the same physical pins under SD's dual-protocol pin definition, not
// a different set.
//
// Full 4-bit mode (CLK/CMD/D0/D1/D2/D3) on boards where SD_D1_PIN/
// SD_D2_PIN are known (GooDisplay: found 2026-08-12 by physically tracing
// the microSD socket's DAT1/DAT2 pads to the ESP32-S3-WROOM-1 module,
// since the vendor's own spec table only documents the 4 SPI-named pins
// and doesn't cover D1/D2 at all). Falls back to 1-bit mode (CLK/CMD/D0
// only, matching setPins()'s 3-arg overload, D3/CS left undriven) on
// boards where they aren't -- e.g. Seeed's SD addon, never traced for
// D1/D2 and not known to be wired for native 4-bit mode at all.
static bool g_present = false;

bool begin() {
    // Boost CPU to full speed for the mount handshake, restoring whatever
    // frequency was active before on the way out. Power::begin() -- the
    // very first thing setup() does, before Panel::begin() or this
    // function ever run -- already drops the CPU to CPU_MHZ_IDLE (80MHz),
    // so every mount attempt including the boot-time one has been
    // happening at reduced clock. Confirmed the hard way (2026-08-12):
    // plain 1-bit mode (CLK/CMD/D0 only, no D1/D2 involved at all) started
    // intermittently failing with a pull-up-shaped ACMD41 error
    // ("sdmmc_init_ocr: send_op_cond returned 0x107") -- the same failure
    // shape whether or not D1/D2 were wired in, ruling those out. ESP32
    // can source a given CPU frequency from either its PLL or XTAL
    // directly depending on power-management state, and some peripheral
    // clock dividers (SDMMC's timing-sensitive init handshake very
    // plausibly included) don't tolerate that ambiguity -- a known class
    // of bug on real InkJoy hardware too, per prior hardware experience.
    uint32_t prevMhz = getCpuFrequencyMhz();
    setCpuFrequencyMhz(CPU_MHZ_PERF);

    // end() first: mirrors the old SPI-mode code's reasoning -- a fresh,
    // real re-probe every call rather than trusting stale internal state,
    // so a card pulled and reinserted mid-session is picked up correctly.
    SD_MMC.end();
#ifdef SD_D1_PIN
    bool pinsOk = SD_MMC.setPins(SD_SPI_SCLK_PIN, SD_SPI_MOSI_PIN, SD_SPI_MISO_PIN,
                                  SD_D1_PIN, SD_D2_PIN, SD_CS_PIN);
    bool mode1bit = false;
#else
    bool pinsOk = SD_MMC.setPins(SD_SPI_SCLK_PIN, SD_SPI_MOSI_PIN, SD_SPI_MISO_PIN);
    bool mode1bit = true;
#endif
    if (!pinsOk) {
        Serial.println("sd: setPins failed");
        g_present = false;
    } else {
        g_present = SD_MMC.begin("/sd", mode1bit);
        if (!g_present) {
            Serial.println("sd: not present (no card, or no addon board)");
        } else {
            Serial.printf("sd: mounted type=%d size=%lluMB (%s-bit)\n",
                          (int)SD_MMC.cardType(), SD_MMC.cardSize() / (1024ULL * 1024ULL),
                          mode1bit ? "1" : "4");
        }
    }

    setCpuFrequencyMhz(prevMhz);
    return g_present;
}

bool present() { return g_present; }
uint64_t totalBytes() { return g_present ? SD_MMC.totalBytes() : 0; }
uint64_t usedBytes() { return g_present ? SD_MMC.usedBytes() : 0; }

void end() {
    SD_MMC.end();
    g_present = false;
}

} // namespace SdCard
#endif // ENABLE_SD_CACHE
