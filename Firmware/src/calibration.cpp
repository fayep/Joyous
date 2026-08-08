#include "calibration.h"
#include <Preferences.h>
#include <string.h>

const P2Ink kDefaultP2Calibration[6] = {
    {42, 33, 38},
    {206, 218, 213},
    {213, 206, 84},
    {115, 36, 26},
    {47, 98, 176},
    {97, 134, 113},
};

void loadP2Calibration(P2Ink out[6]) {
    Preferences prefs;
    prefs.begin("ijcfg", true);
    size_t got = 0;
    if (prefs.isKey("p2cal")) {
        got = prefs.getBytes("p2cal", out, sizeof(P2Ink) * 6);
    }
    prefs.end();
    if (got != sizeof(P2Ink) * 6) {
        memcpy(out, kDefaultP2Calibration, sizeof(kDefaultP2Calibration));
    }
}

void saveP2Calibration(const P2Ink in[6]) {
    Preferences prefs;
    prefs.begin("ijcfg", false);
    prefs.putBytes("p2cal", in, sizeof(P2Ink) * 6);
    prefs.end();
}
