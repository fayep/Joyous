#include "config.h"
#if ENABLE_SD_CACHE
#include "sd_card.h"
#include <SPI.h>
#include <SD.h>

namespace SdCard {

// FSPI (host 1) -- deliberately a different hardware SPI peripheral than
// Seeed_GFX's TFT_eSPI, which Setup510 configures onto HSPI (host 2) for
// ESP32-S3 (USE_HSPI_PORT). Keeping the SD card on its own host means a
// panel refresh and an SD transaction never contend for the same
// controller, even though they're independent electrical buses anyway.
static SPIClass g_spi(FSPI);
static bool g_present = false;

bool begin() {
    g_spi.begin(SD_SPI_SCLK_PIN, SD_SPI_MISO_PIN, SD_SPI_MOSI_PIN, SD_CS_PIN);
    g_present = SD.begin(SD_CS_PIN, g_spi);
    if (!g_present) {
        Serial.println("sd: not present (no card, or no addon board)");
        return false;
    }
    Serial.printf("sd: mounted type=%d size=%lluMB\n",
                  (int)SD.cardType(), SD.cardSize() / (1024ULL * 1024ULL));
    return true;
}

bool present() { return g_present; }
uint64_t totalBytes() { return g_present ? SD.totalBytes() : 0; }
uint64_t usedBytes() { return g_present ? SD.usedBytes() : 0; }

} // namespace SdCard
#endif // ENABLE_SD_CACHE
