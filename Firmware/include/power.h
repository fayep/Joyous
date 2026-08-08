#pragma once
#include <Arduino.h>

// wifi_sleep window handling — the hub can push a daily "sleep between HH:MM
// and HH:MM" window (see research/firmware-notes.md, `wifi_sleep` action).
// Real frames go into deep sleep for that window to save battery; we do the
// same via ESP32 timer-wakeup deep sleep, which fully resets the MCU (RTC
// memory survives, everything else doesn't — main.cpp re-runs setup() and
// re-logs-in on wake, matching the real frame's reboot-style sleep cycle).
namespace Power {

// Call once at boot before other GPIO use — clears deep-sleep pad holds.
void begin();

// Sets the TZ env (see NTP_TZ_INFO in secrets.h — must stay "UTC0", see
// README) once at boot. Does NOT set the wall-clock value itself: this
// firmware has no NTP client. Instead the clock is set for free from the
// hub — every login_ack/heart_ack carries the hub's own true UTC msgid, and
// pairing that with our own millis() readings at publish/receive gives a
// round-trip clock estimate (see syncClockFromRoundTrip) without ever
// talking to a public time server.
void beginTimeEnv();

// Sets the system clock from one login_ack/heart_ack round trip.
// publishMillis/ackReceivedMillis are our own millis() readings at publish
// and ack-receipt; ackServerEpochMs is the ack envelope's own top-level
// "msgid" (the hub's true UTC ms at the moment it built the ack). Assumes
// symmetric latency (half the round trip each way). No-op if the round trip
// looks bogus (implausible server timestamp, or a round trip long enough
// that the symmetric-latency assumption isn't trustworthy).
void syncClockFromRoundTrip(uint32_t publishMillis, uint64_t ackServerEpochMs, uint32_t ackReceivedMillis);

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

// Daytime idle: 80 MHz. Perf: 240 MHz for HTTP/panel work.
void setHighPerformance(bool on);

// After WiFi is up: MAX_MODEM sleep + TX power scaled to RSSI.
void applyRadioPowerSave();

// Light-sleep helper (timer + KEY1/KEY3 + WiFi wakeup). Not used on the
// idle path — full light sleep freezes lwIP so MQTT/TCP cannot ACK.
// Kept for experiments / future tickless idle.
void lightSleepMs(uint32_t ms);

// Panel sleep + rail/divider GPIO hold, then deep sleep for `seconds`.
[[noreturn]] void deepSleepSeconds(uint64_t seconds);

// Same panel/rail shutdown as deepSleepSeconds, but with no timer wake at
// all — only KEY1/KEY3 (or a power cycle) brings it back. Used for the
// sub-BATTERY_CRITICAL_PCT cutoff, where self-waking hourly to recheck a
// pack that's very unlikely to have recovered isn't worth the drain.
[[noreturn]] void deepSleepUntilButton();

// Pack millivolts from the EE02 ÷2 ADC path, or -1 if sensing isn't configured.
int batteryMillivolts();

// Map pack millivolts → 0–100 for InkJoy heart/sleep `battery`. mv < 0
// returns the old placeholder 100. Voltage-curve estimate, not a fuel gauge;
// with USB and no pack the sense node can read ~full.
int batteryPercentFromMillivolts(int mv);
int batteryPercent();  // sample + map

} // namespace Power
