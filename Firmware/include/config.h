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

// Shared clone-firmware protocol/feature version, reported as "T:%d" in
// login's ver string (see inkjoy_protocol.cpp's buildLogin) alongside the
// existing per-board "H:%d" hardware model. Real InkJoy hardware never
// sends this field at all -- joyous-hub treats an absent T as 0. Bump this
// whenever a protocol-visible firmware capability changes, and update the
// hub-side gate (joyous-hub's InkJoyPNGProtocolT) in lockstep -- they must
// never drift apart. T=9: first build with ContentCache/renderPngBytes
// (indexed-PNG play support) -- see content_cache.h.
#define FW_PROTOCOL_T 9
