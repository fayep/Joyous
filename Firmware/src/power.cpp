#include "power.h"
#include "config.h"
#include "secrets.h"
#include "panel.h"
#include <WiFi.h>
#include <time.h>
#include <sys/time.h>
#include <esp_sleep.h>
#include <esp_wifi.h>
#include <driver/gpio.h>

namespace Power {

namespace {

// Rough resting Li-ion SoC from pack millivolts (same shape as typical
// 1S discharge curves used on the EE0x boards).
int percentFromMv(int mv) {
    struct Point {
        int mv;
        int pct;
    };
    static const Point kCurve[] = {
        {4200, 100}, {4100, 94}, {4000, 85}, {3900, 74}, {3800, 62},
        {3700, 48},  {3600, 29}, {3500, 13}, {3400, 6},  {3300, 3},
        {3200, 1},   {3000, 0},
    };
    constexpr int n = (int)(sizeof(kCurve) / sizeof(kCurve[0]));
    if (mv >= kCurve[0].mv) return 100;
    if (mv <= kCurve[n - 1].mv) return 0;
    for (int i = 1; i < n; i++) {
        if (mv >= kCurve[i].mv) {
            return kCurve[i].pct +
                   (mv - kCurve[i].mv) * (kCurve[i - 1].pct - kCurve[i].pct) /
                       (kCurve[i - 1].mv - kCurve[i].mv);
        }
    }
    return 0;
}

bool buttonHeld() {
#if BTN_ORIENT_PIN >= 0 && BTN_RESET_PIN >= 0
    // Polarity is per-board -- see main.cpp's pollOrientationButton().
    const int active = BTN_ACTIVE_HIGH ? HIGH : LOW;
    return digitalRead(BTN_ORIENT_PIN) == active || digitalRead(BTN_RESET_PIN) == active;
#else
    return false;  // no button GPIOs known for this board yet
#endif
}

}  // namespace

void begin() {
    gpio_deep_sleep_hold_dis();
#if PANEL_ENABLE_PIN >= 0
    gpio_hold_dis((gpio_num_t)PANEL_ENABLE_PIN);
#endif
#if BATTERY_ADC_EN_PIN >= 0
    gpio_hold_dis((gpio_num_t)BATTERY_ADC_EN_PIN);
#endif
    setHighPerformance(false);
}

void beginTimeEnv() {
    setenv("TZ", NTP_TZ_INFO, 1);
    tzset();
}

void syncClockFromRoundTrip(uint32_t publishMillis, uint64_t ackServerEpochMs, uint32_t ackReceivedMillis) {
    // Unsigned subtraction wraps correctly even across a millis() rollover.
    uint32_t rttMs = ackReceivedMillis - publishMillis;
    constexpr uint64_t kSaneEpochMs = 1700000000000ULL; // sane post-2023 ms epoch
    constexpr uint32_t kMaxTrustedRttMs = 30000UL;       // longer than this, don't trust the estimate
    if (ackServerEpochMs < kSaneEpochMs || rttMs > kMaxTrustedRttMs) {
        Serial.printf("clock: skipped sync (server_ms=%llu rtt=%ums)\n",
                      (unsigned long long)ackServerEpochMs, rttMs);
        return;
    }
    uint64_t estimatedNowMs = ackServerEpochMs + rttMs / 2;
    struct timeval tv;
    tv.tv_sec = (time_t)(estimatedNowMs / 1000ULL);
    tv.tv_usec = (suseconds_t)((estimatedNowMs % 1000ULL) * 1000ULL);
    settimeofday(&tv, nullptr);
    Serial.printf("clock: synced via round trip rtt=%ums nowMin=%d\n", rttMs, nowMinutesSinceMidnight());
}

int nowMinutesSinceMidnight() {
    time_t now;
    time(&now);
    if (now < 1700000000) return -1; // not synced
    struct tm t;
    localtime_r(&now, &t);
    return t.tm_hour * 60 + t.tm_min;
}

int parseHHMM(const String &s) {
    int colon = s.indexOf(':');
    if (colon < 0) return -1;
    int h = s.substring(0, colon).toInt();
    int m = s.substring(colon + 1).toInt();
    if (h < 0 || h > 23 || m < 0 || m > 59) return -1;
    return h * 60 + m;
}

bool withinWindow(int nowMin, int beginMin, int endMin) {
    if (beginMin < 0 || endMin < 0 || nowMin < 0) return false;
    if (beginMin == endMin) return false; // zero-length window == disabled
    if (beginMin < endMin) return nowMin >= beginMin && nowMin < endMin;
    return nowMin >= beginMin || nowMin < endMin; // overnight wrap
}

long secondsUntil(int nowMin, int endMin) {
    int deltaMin = endMin - nowMin;
    if (deltaMin <= 0) deltaMin += 24 * 60;
    return (long)deltaMin * 60L;
}

void setHighPerformance(bool on) {
    setCpuFrequencyMhz(on ? CPU_MHZ_PERF : CPU_MHZ_IDLE);
}

void applyRadioPowerSave() {
    WiFi.setSleep(true);
    esp_wifi_set_ps(WIFI_PS_MAX_MODEM);

    // esp_wifi_set_max_tx_power units are 0.25 dBm (8..84).
    const int rssi = WiFi.RSSI();
    int8_t qdbm = 78;  // ~19.5 dBm default
    if (rssi > -50) qdbm = 44;       // ~11 dBm — strong AP
    else if (rssi > -65) qdbm = 60;  // ~15 dBm
    esp_err_t err = esp_wifi_set_max_tx_power(qdbm);
    Serial.printf("power: radio MAX_MODEM tx=%.1fdBm rssi=%d err=%d cpu=%uMHz\n",
                  qdbm / 4.0f, rssi, (int)err, getCpuFrequencyMhz());
}

void lightSleepMs(uint32_t ms) {
    if (ms == 0) return;
    if (buttonHeld()) {
        delay(ms > 50 ? 50 : ms);
        return;
    }

    esp_sleep_disable_wakeup_source(ESP_SLEEP_WAKEUP_ALL);
    esp_sleep_enable_timer_wakeup((uint64_t)ms * 1000ULL);
#if BTN_ORIENT_PIN >= 0 && BTN_RESET_PIN >= 0
    // Polarity is per-board -- see main.cpp's pollOrientationButton().
    esp_sleep_enable_ext1_wakeup(BTN_WAKE_MASK, BTN_ACTIVE_HIGH ? ESP_EXT1_WAKEUP_ANY_HIGH : ESP_EXT1_WAKEUP_ANY_LOW);
#endif
    // Keep MQTT reachable: WiFi DTIM / RX can wake the CPU from light sleep.
    esp_sleep_enable_wifi_wakeup();
    esp_light_sleep_start();
}

namespace {

// Shared panel/rail shutdown for both deep-sleep variants below. Arms
// KEY1/KEY3 wake unconditionally (deepSleepSeconds also wants it as an early
// exit; deepSleepUntilButton relies on it being the *only* wake source).
void prepareForDeepSleep() {
    Panel::sleep();

#if PANEL_ENABLE_PIN >= 0
    pinMode(PANEL_ENABLE_PIN, OUTPUT);
    digitalWrite(PANEL_ENABLE_PIN, LOW);
    gpio_hold_en((gpio_num_t)PANEL_ENABLE_PIN);
#endif
#if BATTERY_ADC_EN_PIN >= 0
    pinMode(BATTERY_ADC_EN_PIN, OUTPUT);
    digitalWrite(BATTERY_ADC_EN_PIN, LOW);
    gpio_hold_en((gpio_num_t)BATTERY_ADC_EN_PIN);
#endif
    gpio_deep_sleep_hold_en();

    esp_sleep_disable_wakeup_source(ESP_SLEEP_WAKEUP_ALL);
    // KEY1/KEY3 can also cut the overnight window (or a low-battery recheck
    // sleep) short if needed.
#if BTN_ORIENT_PIN >= 0 && BTN_RESET_PIN >= 0
    // Polarity is per-board -- see main.cpp's pollOrientationButton().
    esp_sleep_enable_ext1_wakeup(BTN_WAKE_MASK, BTN_ACTIVE_HIGH ? ESP_EXT1_WAKEUP_ANY_HIGH : ESP_EXT1_WAKEUP_ANY_LOW);
#endif
}

} // namespace

void deepSleepSeconds(uint64_t seconds) {
    Serial.printf("power: deep sleep %llu s (panel rail + batt sense held off)\n",
                  (unsigned long long)seconds);
    Serial.flush();
    prepareForDeepSleep();
    esp_sleep_enable_timer_wakeup(seconds * 1000000ULL);
    esp_deep_sleep_start();
    while (true) {} // unreachable
}

void deepSleepUntilButton() {
    Serial.println("power: deep sleep until button/power-cycle (battery critical)");
    Serial.flush();
    prepareForDeepSleep();
    esp_deep_sleep_start();
    while (true) {} // unreachable
}

int batteryMillivolts() {
#if BATTERY_ADC_PIN < 0
    return -1;
#else
    // Full-pack ÷2 is ~2.1 V at A0. Default attenuation on some cores tops
    // out near ~2.0 V, which looks like a hard ~78% SoC ceiling (~3.94 V).
    analogReadResolution(12);
    analogSetPinAttenuation(BATTERY_ADC_PIN, ADC_11db);
    pinMode(BATTERY_ADC_PIN, INPUT);
    // BATTERY_ADC_EN_PIN is optional: GooDisplay's divider (unlike Seeed's)
    // has no gating FET at all -- confirmed on real hardware (2026-08-14):
    // a real voltage shows up on BATTERY_ADC_PIN purely from the battery
    // being connected, with nothing actively enabling anything. Boards
    // with a real gate pin (Seeed) still get the enable/settle/disable
    // sequence around the read; boards without one (-1 sentinel) just
    // read directly.
#if BATTERY_ADC_EN_PIN >= 0
    pinMode(BATTERY_ADC_EN_PIN, OUTPUT);
    digitalWrite(BATTERY_ADC_EN_PIN, HIGH);
    delay(20);  // divider RC settle
#endif
    (void)analogReadMilliVolts(BATTERY_ADC_PIN);  // discard first sample
    uint32_t sumMv = 0;
    constexpr int kSamples = 16;
    for (int i = 0; i < kSamples; i++) {
        sumMv += analogReadMilliVolts(BATTERY_ADC_PIN);
        delay(2);
    }
#if BATTERY_ADC_EN_PIN >= 0
    digitalWrite(BATTERY_ADC_EN_PIN, LOW);
#endif
    return (int)(sumMv / kSamples) * BATTERY_DIVIDER_RATIO;
#endif
}

int batteryPercentFromMillivolts(int mv) {
    if (mv < 0) return 100;  // sensing unavailable — keep prior placeholder
    return percentFromMv(mv);
}

int batteryPercent() { return batteryPercentFromMillivolts(batteryMillivolts()); }

} // namespace Power
