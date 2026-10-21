#pragma once
#include <Arduino.h>

// microSD support, native SDMMC peripheral (see SD_* pins in each board's
// config header for the pin rationale -- SD_D1_PIN/SD_D2_PIN, where known,
// enable full 4-bit mode; see sd_card.cpp's begin() comment). No
// card-detect line is available on Seeed's addon connector, so presence
// is inferred from whether the card actually mounts -- an absent addon
// board and an addon board with no card inserted both just fail to mount.
namespace SdCard {

// Mounts the card over the SDMMC peripheral (never shared with the
// panel's own SPI bus). Safe to call with no addon board attached --
// SD_MMC.begin() on an unpopulated bus just fails to see a card and
// returns false.
//
// Always does a real fresh mount (SD.end() then SD.begin()) rather than a
// cheap "already mounted?" check, so it DOES pick up a card that's been
// pulled and reinserted since the last call -- but don't call it routinely
// (e.g. before every play). Confirmed the hard way (2026-08-12): calling
// this unconditionally on every cache-eligible play caused real mount
// failures ("Card Failed! cmd: 0x00") on a card that had been sitting
// untouched and working fine since boot -- tearing down and reinitializing
// the SD peripheral is itself not reliable to do gratuitously. Call it only
// as a recovery step after an SD operation has already failed once (see
// main.cpp's handlePlay), not proactively.
bool begin();

// Last known mount state (true after a successful begin(), false if begin()
// hasn't been called or failed).
bool present();

// Unmounts and powers down the SDMMC peripheral. Call once boot-time SD
// setup (Playlist::begin()/ContentCache::begin()) is done, and again after
// each play's SD cache access finishes -- otherwise the peripheral (and
// the physical card, kept in active standby) stays powered/clocked for
// the entire uptime between deep-sleep cycles for no reason, a real if
// modest continuous battery drain. begin() already does a fresh
// SD.end()+SD.begin() cycle every time regardless, so this doesn't change
// what begin() itself does -- it just stops leaving the peripheral live
// during idle time between calls. See begin()'s own comment for the risk
// this reintroduces (routine remounting was unreliable before the CPU-
// boost-around-mount and cold-boot-settle-delay fixes went in).
void end();

uint64_t totalBytes();
uint64_t usedBytes();

} // namespace SdCard
