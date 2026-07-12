#include "power.h"
#include "secrets.h"
#include <time.h>
#include <esp_sleep.h>

namespace Power {

void beginTimeSync() {
    configTzTime(NTP_TZ_INFO, "pool.ntp.org", "time.nist.gov");
    time_t now = 0;
    for (int i = 0; i < 40 && now < 1700000000; i++) {
        delay(250);
        time(&now);
    }
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

void deepSleepSeconds(uint64_t seconds) {
    esp_sleep_enable_timer_wakeup(seconds * 1000000ULL);
    esp_deep_sleep_start();
    while (true) {} // unreachable
}

} // namespace Power
