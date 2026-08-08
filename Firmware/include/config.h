#pragma once

// Board selection comes from platformio.ini's build_flags (-DBOARD_SEEED /
// -DBOARD_GOODISPLAY on the corresponding [env:...]). Each board's own
// header defines its pin mapping, panel geometry, HW_MODEL, and whether
// ENABLE_SD_CACHE is on.
#if defined(BOARD_SEEED)
#include "config_seeed.h"
#elif defined(BOARD_GOODISPLAY)
#include "config_goodisplay.h"
#else
#error "No board selected -- define BOARD_SEEED or BOARD_GOODISPLAY in platformio.ini build_flags"
#endif
