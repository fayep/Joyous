#pragma once
#include <Arduino.h>

// wifi_sleep window handling — the hub can push a daily "sleep between HH:MM
// and HH:MM" window (see research/firmware-notes.md, `wifi_sleep` action).
// Real frames go into deep sleep for that window to save battery; we do the
// same via ESP32 timer-wakeup deep sleep, which fully resets the MCU (RTC
// memory survives, everything else doesn't — main.cpp re-runs setup() and
// re-logs-in on wake, matching the real frame's reboot-style sleep cycle).
namespace Power {

// Blocks briefly for NTP sync (needed for msgid timestamps and window
// checks). Safe to call after WiFi is connected; falls through after a
// bounded number of retries if NTP is unreachable.
void beginTimeSync();

// -1 if local time isn't synced yet.
int nowMinutesSinceMidnight();

// Parses "H:MM" (no zero-pad, per protocol notes) into minutes since
// midnight. Returns -1 on a malformed string.
int parseHHMM(const String &s);

// True if `nowMin` falls within [beginMin, endMin), handling an overnight
// window where beginMin > endMin (e.g. 23:00-06:00).
bool withinWindow(int nowMin, int beginMin, int endMin);

// Seconds from nowMin until endMin, handling the same overnight wrap.
long secondsUntil(int nowMin, int endMin);

// Deep-sleeps for `seconds` (timer wakeup). Does not return.
[[noreturn]] void deepSleepSeconds(uint64_t seconds);

} // namespace Power
