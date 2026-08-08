#pragma once
#include <Arduino.h>

// microSD support for the snap-on SPI1 addon board (see SD_* pins in
// config.h for the pin rationale). No card-detect line is available on
// this connector, so presence is inferred from whether the card actually
// mounts -- an absent addon board and an addon board with no card inserted
// both just fail to mount.
namespace SdCard {

// Mounts the card over its own dedicated SPI host (never shared with the
// panel's SPI). Safe to call with no addon board attached -- SD.begin() on
// an unpopulated CS/SPI1 bus just fails to see a card and returns false.
bool begin();

// Last known mount state (true after a successful begin(), false if begin()
// hasn't been called or failed).
bool present();

uint64_t totalBytes();
uint64_t usedBytes();

} // namespace SdCard
