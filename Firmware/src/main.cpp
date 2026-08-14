#include <Arduino.h>
#include <WiFi.h>
#include <ArduinoJson.h>
#include <Preferences.h>
#include <esp_wifi.h>
#include <string.h>

#include "secrets.h"
#include "config.h"
#include "inkjoy_protocol.h"
#include "panel.h"
#include "power.h"
#include "ble_provision.h"
#include "mqtt_hub.h"
#if ENABLE_SD_CACHE
#include "sd_card.h"
#include "playlist.h"
#include "content_cache.h"
#endif

#if defined(BOARD_SEEED) || defined(BOARD_GOODISPLAY)
#include <TFT_eSPI.h>
// EPaper is Seeed_Arduino_LCD's e-paper sprite/driver class (Extensions/EPaper.cpp);
// panel_tftespi.cpp owns init/render for both boards, this file just needs
// the instance to exist (Seeed's EE02 and GooDisplay's ESP32-133C02 are the
// same underlying T133A01/GDEP133C02 driver silicon -- see
// panel_tftespi.cpp).
EPaper epaper;
#endif

static String clientId;    // MAC without colons — also the frame's clientID/stamac
static String reportTopic; // /device/report/{MAC} — we publish here
static String subTopic;    // /inkjoyap/{MAC} — we subscribe here

// Sleep window from the hub's wifi_sleep action, in minutes-since-midnight.
// RTC memory survives our own deep-sleep cycles (not full power loss) so a
// window configured once keeps applying across nightly sleeps.
RTC_DATA_ATTR int savedSleepBeginMin = -1;
RTC_DATA_ATTR int savedSleepEndMin = -1;

static unsigned long lastHeartMs = 0;
#if ENABLE_SD_CACHE
static unsigned long lastPlaylistRenderMs = 0;

#if ENABLE_SD_CACHE
// Forces the next offline-cycling check in loop() (playlist.json or
// content-cache fallback, whichever applies) to fire immediately instead
// of waiting out a full interval. Leaving lastPlaylistRenderMs at its
// default of 0 only achieves that when millis() is already large -- true
// when toggling into offline mode mid-session on a device that's been up
// a while, but NOT on a fresh boot that persists straight into offline
// mode (loadPlaybackMode() reads "offline" from NVS), where millis() is
// itself only a few seconds in at that point. Subtracting a day's worth
// of ms guarantees millis()-lastPlaylistRenderMs already exceeds any
// realistic interval (schedule.interval_min or kCacheCycleIntervalMs)
// regardless of how little real uptime has elapsed -- relies on the same
// unsigned-wraparound arithmetic the interval check itself already uses.
static void armOfflineCycleNow() {
    lastPlaylistRenderMs = millis() - 24UL * 60UL * 60UL * 1000UL;
}
#endif
// Cycle position for offline-mode cache-cycling (see loop()) -- only used
// when no playlist.json exists; separate from Playlist's own g_currentFrame
// since the two cycling sources are mutually exclusive per boot but this
// still needs its own persistent index across loop() iterations.
static size_t g_cacheFrameIndex = 0;
// No schedule.interval_min to read when cycling the content cache instead
// of a real playlist.json -- 10 minutes is a reasonable default slideshow
// pace, matching typical InkJoy playlist authoring.
constexpr unsigned long kCacheCycleIntervalMs = 10UL * 60UL * 1000UL;
#endif
static String g_currentPlayAckMsgId;

// millis() readings at the moment login/heart were published — paired with
// the ack's own server-timestamped msgid for round-trip clock sync (see
// Power::syncClockFromRoundTrip, called from onMqttMessage below).
static uint32_t g_loginSentMs = 0;
static uint32_t g_heartSentMs = 0;

// millis() deadline before which loop() won't act on the sleep-window check —
// set on every login_ack so a hub-pushed corrective wifi_sleep (see
// LOGIN_SETTLE_MS in config.h) has time to arrive and update
// savedSleepBeginMin/EndMin via the normal per-iteration loopWait() below,
// before we'd otherwise re-enter deep sleep under a stale schedule.
static uint32_t g_loginSettleUntilMs = 0;

static bool connectWiFi();  // defined below; hearts force this when radio is down
static bool goOnline();     // defined below; brings WiFi/MQTT up (boot or KEY2 toggle)
static void goOffline();    // defined below; tears WiFi/MQTT down for offline mode
static void onMqttMessage(const char *payload, int len);

struct RuntimeConfig {
    String ssid;
    String wifiPass;
    String hubHost;
    int hubPort = 0;
    String mqttUser;
    String mqttPass;
};
static RuntimeConfig g_cfg;

// Hub portrait preference (heart.orientation 0 vs 90). Default landscape.
static bool g_portrait = false;
static bool g_orientBtnDown = false;
static uint32_t g_orientBtnAt = 0;
static bool g_resetBtnDown = false;
static uint32_t g_resetBtnAt = 0;
static bool g_resetFired = false;
// SW3/KEY2 (BTN_MODE_PIN) toggle: false = online (MQTT-driven play,
// existing default), true = offline (SD playlist cycling, ignore play).
static bool g_offlineMode = false;
static bool g_modeBtnDown = false;
static uint32_t g_modeBtnAt = 0;

static bool loadConfig() {
    Preferences prefs;
    prefs.begin("ijcfg", true);
    g_cfg.ssid = prefs.getString("ssid", "");
    g_cfg.wifiPass = prefs.getString("pass", "");
    g_cfg.hubHost = prefs.getString("host", "");
    g_cfg.hubPort = prefs.getInt("port", 0);
    g_cfg.mqttUser = prefs.isKey("mqtt_usr") ? prefs.getString("mqtt_usr", "") : "";
    g_cfg.mqttPass = prefs.isKey("mqtt_pwd") ? prefs.getString("mqtt_pwd", "") : "";
    prefs.end();

    if (g_cfg.ssid.length() > 0 && g_cfg.hubHost.length() > 0 && g_cfg.hubPort > 0) {
        return true;
    }

    g_cfg.ssid = WIFI_SSID;
    g_cfg.wifiPass = WIFI_PASSWORD;
    g_cfg.hubHost = HUB_HOST;
    g_cfg.hubPort = HUB_MQTT_PORT;
    g_cfg.mqttUser = "";
    g_cfg.mqttPass = "";
    return false;
}

static void saveConfig(const BleProvision::Config &adopted) {
    Preferences prefs;
    prefs.begin("ijcfg", false);
    prefs.putString("ssid", adopted.ssid);
    prefs.putString("pass", adopted.wifiPass);
    prefs.putString("host", adopted.hubHost);
    prefs.putInt("port", adopted.hubPort);
    prefs.putString("mqtt_usr", adopted.mqttUser);
    prefs.putString("mqtt_pwd", adopted.mqttPass);
    // One-shot: show Connected screen after the post-adopt Wi‑Fi/MQTT bring-up.
    prefs.putBool("show_ready", true);
    prefs.end();

    g_cfg.ssid = adopted.ssid;
    g_cfg.wifiPass = adopted.wifiPass;
    g_cfg.hubHost = adopted.hubHost;
    g_cfg.hubPort = adopted.hubPort;
    g_cfg.mqttUser = adopted.mqttUser;
    g_cfg.mqttPass = adopted.mqttPass;
}

static bool pendingShowReady() {
    Preferences prefs;
    prefs.begin("ijcfg", true);
    bool v = prefs.isKey("show_ready") && prefs.getBool("show_ready", false);
    prefs.end();
    return v;
}

static void clearShowReady() {
    Preferences prefs;
    prefs.begin("ijcfg", false);
    prefs.putBool("show_ready", false);
    prefs.end();
}

static void clearConfig() {
    Preferences prefs;
    prefs.begin("ijcfg", false);
    prefs.clear();
    prefs.end();
    g_cfg = RuntimeConfig{};
    g_portrait = false;
    g_offlineMode = false;
}

static void loadOrientation() {
    Preferences prefs;
    prefs.begin("ijcfg", true);
    g_portrait = prefs.isKey("portrait") ? prefs.getBool("portrait", false) : false;
    prefs.end();
}

static void saveOrientation() {
    Preferences prefs;
    prefs.begin("ijcfg", false);
    prefs.putBool("portrait", g_portrait);
    prefs.end();
}

static void loadPlaybackMode() {
    Preferences prefs;
    prefs.begin("ijcfg", true);
    g_offlineMode = prefs.isKey("offline") ? prefs.getBool("offline", false) : false;
    prefs.end();
}

static void savePlaybackMode() {
    Preferences prefs;
    prefs.begin("ijcfg", false);
    prefs.putBool("offline", g_offlineMode);
    prefs.end();
}

static int heartOrientation() {
    return g_portrait ? ORIENT_PORTRAIT : ORIENT_LANDSCAPE;
}

static String minToHHMM(int minutes) {
    if (minutes < 0) return "0:00";
    char buf[8];
    snprintf(buf, sizeof(buf), "%d:%02d", minutes / 60, minutes % 60);
    return String(buf);
}

static void publish(const String &payload) { MqttHub::publish(payload); }

static void sendLogin() {
    g_loginSentMs = millis();
    P2Ink p2cal[6];
    loadP2Calibration(p2cal);
    publish(InkJoy::buildLogin(clientId, minToHHMM(savedSleepBeginMin), minToHHMM(savedSleepEndMin), p2cal));
}

// Returns the battery percent sent with this heart, or -1 if the heart
// couldn't be sent (wifi/mqtt down) — battery state is unknown in that case,
// so callers should treat -1 as "don't act on it" rather than "critical".
static int sendHeart() {
    // Modem-sleep / TX power save can drop the association; never publish a
    // heart (or skip one) without first verifying WiFi is actually up.
    if (WiFi.status() != WL_CONNECTED) {
        Serial.println("heart: wifi down — reconnecting");
        if (!connectWiFi()) {
            Serial.println("heart: skipped — wifi reconnect failed");
            return -1;
        }
    }
    if (!MqttHub::started()) {
        Serial.println("heart: mqtt stopped — restarting");
        if (!MqttHub::begin(g_cfg.hubHost.c_str(), g_cfg.hubPort, clientId.c_str(),
                            subTopic.c_str(), g_cfg.mqttUser.c_str(),
                            g_cfg.mqttPass.c_str())) {
            Serial.println("heart: skipped — mqtt restart failed");
            return -1;
        }
    }
    if (!MqttHub::connected()) {
        // WiFi just came back — give esp-mqtt a few seconds to rejoin.
        // Don't drain the message queue here: login_ack → sendHeart would re-enter.
        for (int i = 0; i < 40 && !MqttHub::connected(); i++) {
            MqttHub::loopWait(100, nullptr);
        }
    }
    if (!MqttHub::connected()) {
        Serial.println("heart: skipped — mqtt not connected");
        return -1;
    }

    int mv = Power::batteryMillivolts();
    int pct = Power::batteryPercentFromMillivolts(mv);
    int orient = heartOrientation();
    Serial.printf("heart: battery=%d%% (%dmV) rssi=%d orient=%d\n",
                  pct, mv, WiFi.RSSI(), orient);
    g_heartSentMs = millis();
    publish(InkJoy::buildHeart(clientId, pct, WiFi.RSSI(), WiFi.SSID(), orient));
    return pct;
}

// Called with the battery percent from the heart we just sent. Below
// BATTERY_LOW_PCT: warn on-panel, tell the hub why, deep-sleep to recheck in
// an hour. At/below BATTERY_CRITICAL_PCT: same warning/notice, but sleep
// with no timer wake at all — only KEY1/KEY3 or a power cycle brings it back
// (see BATTERY_CRITICAL_PCT's comment in config.h for why).
static void handleBatteryLevel(int pct) {
    if (pct < 0 || pct > BATTERY_LOW_PCT) return;
    Serial.printf("battery: %d%% <= %d%% — warning + sleep\n", pct, BATTERY_LOW_PCT);
    if (MqttHub::connected()) {
        publish(InkJoy::buildSleep(clientId, pct, InkJoy::SLEEP_REASON_LOW_BATTERY));
        delay(200);  // let the publish flush before the radio drops
    }
    Panel::showLowBattery(g_portrait);
    MqttHub::stop();
    if (pct <= BATTERY_CRITICAL_PCT) {
        Power::deepSleepUntilButton();
    } else {
        Power::deepSleepSeconds(BATTERY_LOW_RECHECK_SECONDS);
    }
}

static void toggleOrientation() {
    g_portrait = !g_portrait;
    saveOrientation();
    Serial.printf("orient: %s (%d)\n",
                  g_portrait ? "portrait" : "landscape", heartOrientation());
    Panel::showOrientation(g_portrait);
    // sendLogin() as well as sendHeart(): the hub only mirrors heart's
    // orientation field into its own Portrait state once it knows this
    // device's HardwareModel (joyous-hub's devices.go UpdateHeart), and
    // HardwareModel is only ever learned from a login's H: field, not from
    // heart -- a bridge restart resets that knowledge until the device's
    // next natural reconnect. A fresh login here forces the hub to
    // (re-)learn HardwareModel immediately, so an orientation button press
    // always reaches the hub's Portrait state right away instead of
    // silently no-op'ing until some later unrelated reconnect.
    sendLogin();
    handleBatteryLevel(sendHeart());
    lastHeartMs = millis();
}

static void pollOrientationButton() {
#if BTN_ORIENT_PIN >= 0
    // Polarity is per-board: Seeed's KEY1 is active-low with internal
    // pull-up (BTN_ACTIVE_HIGH=0); GooDisplay's SW2 is active-high per its
    // own vendor spec ("All three buttons are triggered by a high level"),
    // confirmed the hard way -- using the wrong polarity here (internal
    // pull-up fighting a circuit built for active-high) left the pin in an
    // underdetermined, noise-susceptible idle state that fired at random.
    bool down = digitalRead(BTN_ORIENT_PIN) == (BTN_ACTIVE_HIGH ? HIGH : LOW);
    uint32_t now = millis();
    if (down && !g_orientBtnDown && (now - g_orientBtnAt) > 40) {
        g_orientBtnDown = true;
        g_orientBtnAt = now;
        toggleOrientation();
    } else if (!down && g_orientBtnDown && (now - g_orientBtnAt) > 40) {
        g_orientBtnDown = false;
        g_orientBtnAt = now;
    }
#endif
}

static void toggleOfflineMode() {
    g_offlineMode = !g_offlineMode;
    savePlaybackMode();
    Serial.printf("mode: %s\n", g_offlineMode ? "offline (SD playlist)" : "online (MQTT)");
    Panel::showMode(g_offlineMode, g_portrait);
    if (g_offlineMode) {
        goOffline();
#if ENABLE_SD_CACHE
        armOfflineCycleNow();
#endif
    } else if (!goOnline()) {
        // Not fatal: loop()'s own sendHeart() already retries connectWiFi()/
        // MqttHub::begin() on its next interval once we're back in the
        // online branch of loop(), same as any other transient wifi drop.
        Serial.println("mode: online requested but wifi/mqtt bring-up failed — will keep retrying");
    } else {
        lastHeartMs = millis();
    }
}

static void pollModeButton() {
#if BTN_MODE_PIN >= 0
    // Polarity is per-board -- see pollOrientationButton()'s comment.
    bool down = digitalRead(BTN_MODE_PIN) == (BTN_ACTIVE_HIGH ? HIGH : LOW);
    uint32_t now = millis();
    if (down && !g_modeBtnDown && (now - g_modeBtnAt) > 40) {
        g_modeBtnDown = true;
        g_modeBtnAt = now;
        toggleOfflineMode();
    } else if (!down && g_modeBtnDown && (now - g_modeBtnAt) > 40) {
        g_modeBtnDown = false;
        g_modeBtnAt = now;
    }
#endif
}

static void factoryReset() {
    Serial.println("nvs: KEY3 long-press — clearing ijcfg, rebooting to BLE adopt");
    clearConfig();
    MqttHub::stop();
    Panel::showFactoryReset(g_portrait);
    delay(500);
    ESP.restart();
}

static void softReboot() {
    // Plain reboot, no NVS/config change -- picks up anything only checked
    // once at boot (e.g. SD card presence; ENABLE_SD_CACHE boards don't
    // re-poll for a card every loop) without needing a full power cycle.
    Serial.println("reboot: KEY3 short-press — soft reboot");
    MqttHub::stop();
    delay(200);
    ESP.restart();
}

static void pollResetButton() {
#if BTN_RESET_PIN >= 0
    // Polarity is per-board -- see pollOrientationButton()'s comment.
    // Held >= BTN_RESET_HOLD_MS clears adoption NVS and reboots to setup;
    // released before that (a short press) just soft-reboots instead.
    bool down = digitalRead(BTN_RESET_PIN) == (BTN_ACTIVE_HIGH ? HIGH : LOW);
    uint32_t now = millis();
    if (down) {
        if (!g_resetBtnDown) {
            g_resetBtnDown = true;
            g_resetBtnAt = now;
            g_resetFired = false;
        } else if (!g_resetFired && (now - g_resetBtnAt) >= BTN_RESET_HOLD_MS) {
            g_resetFired = true;
            factoryReset();
        }
    } else if (g_resetBtnDown) {
        g_resetBtnDown = false;
        if (!g_resetFired && (now - g_resetBtnAt) > 40) {
            softReboot();
        }
        g_resetFired = false;
    }
#endif
}

static void onPlayProgress(int pct) {
    int result = InkJoy::progressResult(pct);
    if (result < 0) return;
    publish(InkJoy::buildAck("play_ack", clientId, g_currentPlayAckMsgId, result));
}

// Plain network fetch+render, no SD cache involved -- the "per usual"
// fallback for both an empty hash (legacy hub, real InkJoy wire msgs) and
// a genuine SD cache failure (no card physically present, write error --
// see handlePlay; a normal cache miss on a healthy card instead goes
// through ContentCache::fetchAndRender, which caches AND renders). Dispatches
// on imgUrl's extension since a hub new enough to send PNG could point
// either format at this same fallback.
static bool renderPlayImage(const InkJoy::PlayMsg &play, const Panel::OverlayLine *lines,
                             int lineCount, const Panel::OverlayLine *caption) {
    if (play.img.imgUrl.endsWith(".png")) {
        return Panel::renderPngFromHttp(play.host, play.port, play.img.imgUrl, lines, lineCount, caption);
    }
    return Panel::renderBinFromHttp(play.host, play.port, play.img.imgUrl, onPlayProgress,
                                     lines, lineCount, caption);
}

static void handlePlay(JsonDocument &doc) {
    InkJoy::PlayMsg play;
    if (!InkJoy::parsePlay(doc, play)) return;

    g_currentPlayAckMsgId = play.msgId;

    if (g_offlineMode) {
        // Should be unreachable in practice -- WiFi is off in offline mode
        // (see goOffline()), so loop() never calls MqttHub::loopWait()/
        // onMqttMessage() to begin with. Kept as a defensive belt-and-
        // braces check (and to document the invariant) in case that ever
        // changes, e.g. a message arriving mid-transition before the radio
        // actually powers down.
        Serial.println("play: declined (offline mode)");
        publish(InkJoy::buildAck("play_ack", clientId, play.msgId, InkJoy::RESULT_FAILED));
        return;
    }

    publish(InkJoy::buildAck("play_ack", clientId, play.msgId, InkJoy::RESULT_ACCEPTED));

    // Translated up front (not after rendering) -- every render path below
    // takes this directly and draws it into the same buffer before its own
    // one epaper.update() call. absent/empty (offline mode, real InkJoy,
    // Samsung, or a clone-board play with the hub's overlay feature
    // inactive) is a no-op there, not a separate step here -- see
    // panel.h's own note above the render*() declarations for why: two
    // renders (image, then overlay) meant two full e-paper waveform
    // refreshes back to back, confirmed on real hardware as genuinely,
    // unacceptably slow (~1 minute per play).
    Panel::OverlayLine lines[6];
    for (int i = 0; i < play.lineCount; i++) {
        lines[i].text = play.lines[i].text;
        lines[i].font = play.lines[i].font;
        lines[i].x = play.lines[i].x;
        lines[i].y = play.lines[i].y;
    }
    Panel::OverlayLine caption;
    if (play.hasCaption) {
        caption.text = play.caption.text;
        caption.font = play.caption.font;
        caption.x = play.caption.x;
        caption.y = play.caption.y;
    }
    const Panel::OverlayLine *captionPtr = play.hasCaption ? &caption : nullptr;

#if ENABLE_SD_CACHE
    // Re-mount fresh here, while the system is still at idle CPU/power --
    // i.e. BEFORE Power::setHighPerformance(true) below ramps the CPU, and
    // before any WiFi download or panel refresh current draw starts. The
    // SD bus appears to be sensitive to that concurrent high-current load
    // (all three of CPU boost, active WiFi TX, and an e-paper refresh land
    // in this same window on every play, and GooDisplay has no dedicated
    // power-enable/load-switch for the SD slot -- it's on the shared,
    // unsequenced 3.3V rail). An earlier attempt at remounting mid-flow
    // (after the CPU/power ramp had already started) made a previously-
    // working card fail, which pointed at "never remount" -- but doing it
    // here instead, while the board is still quiet, is a different bet:
    // that remounting itself isn't the problem, timing it during heavy
    // concurrent load was.
    bool sdReady = !play.img.hash.isEmpty() && SdCard::begin();
#endif

    Power::setHighPerformance(true);
#if ENABLE_SD_CACHE
    // play.img.hash is only populated once the hub's PNG encoder exists
    // (see content_cache.h) -- until then this is always empty and we go
    // straight to renderPlayImage(), same as before ContentCache existed.
    int orientation = heartOrientation();
    bool ok;
    if (sdReady) {
        if (ContentCache::has(play.img.hash, orientation)) {
            Serial.printf("play: cache hit %s\n", play.img.hash.c_str());
            ok = ContentCache::render(play.img.hash, orientation, lines, play.lineCount, captionPtr);
        } else {
            Serial.printf("play: cache miss %s, fetching+caching\n", play.img.hash.c_str());
            // No onProgress plumbing through ContentCache yet -- a cached
            // fetch skips the 20/40/60/80 progress acks the direct path
            // sends, going straight from accepted to done/failed.
            ok = ContentCache::fetchAndRender(play.host, play.port, play.img.imgUrl,
                                               play.img.hash, orientation,
                                               lines, play.lineCount, captionPtr);
            if (ok) {
                ContentCache::evictIfNeeded(orientation);
            } else {
                // Only reached on a genuine, unrecoverable SD problem (no
                // card physically present despite ENABLE_SD_CACHE, a
                // write error even after the remount retry, etc.) --
                // fall back to a plain network render rather than failing
                // the play outright. On a healthy card this branch never
                // runs; the miss above is written to cache normally.
                Serial.println("play: SD cache fetch failed, falling back to direct network render");
                ok = renderPlayImage(play, lines, play.lineCount, captionPtr);
            }
        }
    } else {
        ok = renderPlayImage(play, lines, play.lineCount, captionPtr);
    }
    if (sdReady) SdCard::end();  // see SdCard::end()'s comment -- don't leave it powered idle
#else
    bool ok = renderPlayImage(play, lines, play.lineCount, captionPtr);
#endif
    Power::setHighPerformance(false);

    publish(InkJoy::buildAck("play_ack", clientId, play.msgId,
                              ok ? InkJoy::RESULT_DONE : InkJoy::RESULT_FAILED));
}

static void handleWifiSleep(JsonDocument &doc) {
    InkJoy::SleepWindow win;
    if (!InkJoy::parseSleepWindow(doc, win)) return;

    int beginMin = Power::parseHHMM(win.beginTime);
    int endMin = Power::parseHHMM(win.endTime);
    if (beginMin >= 0 && endMin >= 0) {
        savedSleepBeginMin = beginMin;
        savedSleepEndMin = endMin;
    }
    publish(InkJoy::buildAck("wifi_sleep_ack", clientId, win.ackMsgId, InkJoy::RESULT_ACCEPTED));
}

// Not a real InkJoy action -- our own addition (see joyous-hub's
// CmdSendOverlay) for comparing on-device overlay rendering against the
// hub's baked-in version. Single ack, no progress -- unlike handlePlay this
// is a small, synchronous draw with no HTTP download to report progress on.
static void handleOverlay(JsonDocument &doc) {
    InkJoy::OverlayMsg ov;
    if (!InkJoy::parseOverlay(doc, ov)) return;

    Panel::OverlayLine lines[6];
    for (int i = 0; i < ov.lineCount; i++) {
        lines[i].text = ov.lines[i].text;
        lines[i].font = ov.lines[i].font;
        lines[i].x = ov.lines[i].x;
        lines[i].y = ov.lines[i].y;
    }
    Panel::OverlayLine caption;
    if (ov.hasCaption) {
        caption.text = ov.caption.text;
        caption.font = ov.caption.font;
        caption.x = ov.caption.x;
        caption.y = ov.caption.y;
    }

    // Panel work (epaper.update()) -- same CPU boost handlePlay uses around
    // its own panel refresh, see Power::setHighPerformance's own comment
    // ("Perf: 240 MHz for HTTP/panel work").
    Power::setHighPerformance(true);
    bool ok = Panel::drawOverlayLines(lines, ov.lineCount, ov.hasCaption ? &caption : nullptr);
    Power::setHighPerformance(false);

    publish(InkJoy::buildAck("overlay_ack", clientId, ov.msgId,
                              ok ? InkJoy::RESULT_DONE : InkJoy::RESULT_FAILED));
}

static void onMqttMessage(const char *payload, int len) {
    JsonDocument doc;
    if (deserializeJson(doc, payload, len)) return;

    const char *action = doc["action"] | "";
    if (!strcmp(action, "login_ack")) {
        Power::syncClockFromRoundTrip(g_loginSentMs, InkJoy::parseMsgIdMs(doc), millis());
        // Real frames: login → login_ack → first heart (battery/rssi/orient).
        Serial.println("mqtt: login_ack → heart");
        handleBatteryLevel(sendHeart());
        lastHeartMs = millis();
        g_loginSettleUntilMs = millis() + LOGIN_SETTLE_MS;
    } else if (!strcmp(action, "heart_ack")) {
        Power::syncClockFromRoundTrip(g_heartSentMs, InkJoy::parseMsgIdMs(doc), millis());
    } else if (!strcmp(action, "play")) {
        handlePlay(doc);
    } else if (!strcmp(action, "wifi_sleep")) {
        handleWifiSleep(doc);
    } else if (!strcmp(action, "overlay")) {
        handleOverlay(doc);
    }
}

static volatile uint8_t g_lastWifiReason = 0;

static void onWiFiEvent(WiFiEvent_t event, WiFiEventInfo_t info) {
    if (event == ARDUINO_EVENT_WIFI_STA_DISCONNECTED) {
        g_lastWifiReason = info.wifi_sta_disconnected.reason;
        Serial.printf("wifi: disconnect reason=%u\n",
                      (unsigned)info.wifi_sta_disconnected.reason);
    } else if (event == ARDUINO_EVENT_WIFI_STA_GOT_IP) {
        Serial.printf("wifi: got ip %s\n",
                      IPAddress(info.got_ip.ip_info.ip.addr).toString().c_str());
    }
}

struct ApPick {
    bool found = false;
    int32_t rssi = -127;
    uint8_t channel = 0;
    uint8_t bssid[6] = {};
    wifi_auth_mode_t enc = WIFI_AUTH_OPEN;
};

// Pick the strongest BSS for this SSID. Still force WPA2 authmode later so
// WPA2/WPA3 transition APs don't try SAE (ESP32 often AUTH_FAILs on that).
static ApPick pickAp(const String &ssid) {
    Serial.println("wifi: scanning...");
    int n = WiFi.scanNetworks(/*async=*/false, /*hidden=*/true);
    ApPick bestAny;
    for (int i = 0; i < n; i++) {
        if (WiFi.SSID(i) != ssid) continue;
        wifi_auth_mode_t enc = WiFi.encryptionType(i);
        int32_t rssi = WiFi.RSSI(i);
        Serial.printf("wifi: saw %s ch=%d rssi=%d enc=%d bssid=%s\n",
                      ssid.c_str(), WiFi.channel(i), rssi, (int)enc,
                      WiFi.BSSIDstr(i).c_str());

        if (rssi > bestAny.rssi) {
            bestAny.found = true;
            bestAny.rssi = rssi;
            bestAny.channel = (uint8_t)WiFi.channel(i);
            bestAny.enc = enc;
            memcpy(bestAny.bssid, WiFi.BSSID(i), 6);
        }
    }
    WiFi.scanDelete();
    if (bestAny.found) {
        Serial.printf("wifi: picking strongest BSS rssi=%d enc=%d\n",
                      bestAny.rssi, (int)bestAny.enc);
        return bestAny;
    }
    Serial.printf("wifi: ssid %s not seen (%d APs total)\n", ssid.c_str(), n);
    return {};
}

static void logPassFingerprint(const String &pass) {
    unsigned upper = 0, lower = 0, digit = 0, other = 0;
    for (unsigned i = 0; i < pass.length(); i++) {
        char c = pass[i];
        if (c >= 'A' && c <= 'Z') upper++;
        else if (c >= 'a' && c <= 'z') lower++;
        else if (c >= '0' && c <= '9') digit++;
        else other++;
    }
    // Fingerprint only — never print the password itself.
    Serial.printf("wifi: pass fingerprint len=%u upper=%u lower=%u digit=%u other=%u\n",
                  (unsigned)pass.length(), upper, lower, digit, other);
}

static bool connectWiFi() {
    WiFi.onEvent(onWiFiEvent);
    logPassFingerprint(g_cfg.wifiPass);
    Serial.printf("wifi: connecting to %s\n", g_cfg.ssid.c_str());
    WiFi.mode(WIFI_STA);
    // Disable modem-sleep only while associating — re-enable below once up.
    WiFi.setSleep(false);

    ApPick ap = pickAp(g_cfg.ssid);
    if (ap.found) {
        // Lock to WPA2 so we don't attempt WPA3 SAE against transition BSSes.
        WiFi.begin(g_cfg.ssid.c_str(), g_cfg.wifiPass.c_str(), ap.channel,
                   ap.bssid, /*connect=*/false);
        wifi_config_t conf = {};
        if (esp_wifi_get_config(WIFI_IF_STA, &conf) == ESP_OK) {
            conf.sta.threshold.authmode = WIFI_AUTH_WPA2_PSK;
            conf.sta.pmf_cfg.capable = true;
            conf.sta.pmf_cfg.required = false;
            esp_wifi_set_config(WIFI_IF_STA, &conf);
        }
        WiFi.reconnect();
    } else {
        WiFi.begin(g_cfg.ssid.c_str(), g_cfg.wifiPass.c_str());
    }

    const uint32_t deadline = millis() + 45000;
    wl_status_t st = WiFi.status();
    while (st != WL_CONNECTED && millis() < deadline) {
        delay(1000);
        st = WiFi.status();
        Serial.printf("wifi: status=%d last_reason=%u\n",
                      (int)st, (unsigned)g_lastWifiReason);
    }
    if (st != WL_CONNECTED) {
        Serial.printf("wifi: failed status=%d last_reason=%u\n",
                      (int)st, (unsigned)g_lastWifiReason);
        return false;
    }
    Serial.printf("wifi: ok ip=%s rssi=%d\n",
                  WiFi.localIP().toString().c_str(), WiFi.RSSI());
    // Daytime "low power": MAX_MODEM + TX scaled to RSSI. Real deep sleep
    // only happens in the hub's wifi_sleep window (see loop()).
    Power::applyRadioPowerSave();
    return true;
}

// Brings WiFi + MQTT up: boot's online path and KEY2's offline→online
// transition both go through this. sendLogin() itself fires from
// MqttHub's connected-handler (set once in setup(), persists across
// stop()/begin() cycles), not from here.
static bool goOnline() {
    if (!connectWiFi()) return false;
    if (!MqttHub::begin(g_cfg.hubHost.c_str(), g_cfg.hubPort, clientId.c_str(),
                        subTopic.c_str(), g_cfg.mqttUser.c_str(),
                        g_cfg.mqttPass.c_str())) {
        Serial.println("mqtt: begin failed");
        return false;
    }
    return true;
}

// Tears WiFi + MQTT down for offline mode -- WIFI_OFF actually powers the
// radio down (not just disassociates), which is the point: offline mode is
// meant to extend battery life, not just stop attempting network I/O while
// leaving the radio powered and idle.
static void goOffline() {
    MqttHub::stop();
    // disconnect(true) tears down the association; mode(WIFI_OFF) powers
    // the radio down fully (not just idle-associated) -- eraseap avoided
    // deliberately, since connectWiFi() always WiFi.begin()s explicitly
    // from g_cfg anyway, so there's nothing to gain from also wearing NVS
    // by erasing WiFi's own separately-stored AP config on every toggle.
    WiFi.disconnect(true);
    WiFi.mode(WIFI_OFF);
    Serial.println("wifi: off (offline mode)");
}

void setup() {
    Serial.begin(115200);
    delay(300);
    Serial.println();
    Serial.println("seeed inkjoy: boot");
    Power::begin();

    // XIAO user LED (GPIO21, active-low) — force off. The charge LED next to
    // USB is hardware-only and blinks whenever a LiPo is charging; that is
    // not under firmware control and is not a power-mode indicator.
#ifdef LED_BUILTIN
    pinMode(LED_BUILTIN, OUTPUT);
    digitalWrite(LED_BUILTIN, HIGH);
#endif

#if BTN_ORIENT_PIN >= 0
    pinMode(BTN_ORIENT_PIN, BTN_ACTIVE_HIGH ? INPUT_PULLDOWN : INPUT_PULLUP);
#endif
#if BTN_RESET_PIN >= 0
    pinMode(BTN_RESET_PIN, BTN_ACTIVE_HIGH ? INPUT_PULLDOWN : INPUT_PULLUP);
#endif
#if BTN_MODE_PIN >= 0
    pinMode(BTN_MODE_PIN, BTN_ACTIVE_HIGH ? INPUT_PULLDOWN : INPUT_PULLUP);
#endif
    loadOrientation();
    loadPlaybackMode();
#if ENABLE_SD_CACHE
    if (g_offlineMode) {
        armOfflineCycleNow();
    }
#endif

    Serial.println("panel: begin");
    Panel::begin();
    Serial.println("panel: ready");

// No test-function auto-run here on purpose. An earlier test
// (colorCycleTest, cycling through 6 colors) was wired into setup() for one
// bring-up session (2026-08-07) and very likely caused a brownout-reset
// loop on real hardware -- refreshing two real EPD driver ICs draws real
// current, and running it unconditionally on every boot meant ANY reset
// re-triggered it immediately with no real gap. A SECOND real refresh loop
// was then also observed from a totally normal boot path (Panel::begin() +
// the one showProvisioning() refresh, no test function involved), so the
// trigger was never conclusively pinned to any one test specifically --
// treat any wired-in real-hardware refresh as carrying that same risk.
//
// GooDisplay's panel driver went through a full bring-up (2026-08-07/08):
// a from-scratch ESP-IDF driver (panel_goodisplay.cpp, kept in the tree but
// excluded from the build) confirmed the panel works over single-line SPI
// with a left/right CS0/CS1 column split, then Seeed_GFX (same underlying
// T133A01/GDEP133C02 silicon) was retried with its earlier TFT_DC/TFT_RST
// pin-aliasing bug fixed and became the real implementation -- see
// panel_tftespi.cpp and tft_setup_goodisplay.h.

#if ENABLE_SD_CACHE
    // Settle delay: the comment above documents Panel::begin() as an
    // already-known real-current-draw/brownout-reset risk on this exact
    // hardware (its EPD driver IC's own reset+charge-pump power-up
    // sequence). SD.begin() immediately after that, with no gap, means the
    // SD peripheral's own init handshake was landing right in that same
    // window -- and every SD mount failure caught so far (2026-08-12) has
    // been a low-level "sdcard_mount(): f_mount failed" / "Select Failed"
    // inside SD.begin() itself, before any of our own code runs, including
    // on a totally quiet fresh boot with nothing else yet running. Giving
    // the panel's power rails a moment to settle first is cheap and
    // directly targets that. Bumped 500ms -> 3000ms (2026-08-12) while
    // actively debugging a cold-boot-only mount failure ("send_op_cond
    // returned 0x107") that a warm/soft reboot doesn't reproduce -- soft
    // reboot skips the analog rail/charge-pump ramp a true cold power-on
    // goes through, so if a longer settle window fixes the cold-boot case
    // too, that confirms settling time (not CPU clock, already ruled out)
    // is the real cause. Also just makes the boot window easier to catch
    // on a serial monitor. Dial back down once this either confirms or
    // rules out the settling-time theory -- 3s adds real boot latency in
    // normal use.
    delay(3000);
    SdCard::begin();  // logs its own mount result; absence is non-fatal
    Playlist::begin();  // logs its own result; no card/playlist.json is non-fatal
    if (SdCard::present()) {
        ContentCache::begin();  // logs its own result; creates cache dirs up front
    }
    // Power the SDMMC peripheral back down now that boot-time setup is
    // done -- nothing needs it live again until the next cache-eligible
    // play (handlePlay() re-mounts on demand; see SdCard::end()'s comment
    // on why this matters).
    SdCard::end();
#endif

    WiFi.mode(WIFI_STA);
    clientId = WiFi.macAddress();
    clientId.replace(":", "");
    clientId.toUpperCase();
    reportTopic = "/device/report/" + clientId;
    subTopic = "/inkjoyap/" + clientId;
    Serial.printf("id: %s\n", clientId.c_str());
    Serial.printf("orient: %s (%d) — KEY1 toggles; KEY3 hold %ds = factory reset\n",
                  g_portrait ? "portrait" : "landscape", heartOrientation(),
                  BTN_RESET_HOLD_MS / 1000);
    Serial.printf("mode: %s — KEY2 toggles\n",
                  g_offlineMode ? "offline (SD playlist)" : "online (MQTT)");

    bool adopted = loadConfig();
    Serial.printf("cfg: adopted=%d ssid=%s hub=%s:%d\n",
                  adopted ? 1 : 0, g_cfg.ssid.c_str(),
                  g_cfg.hubHost.c_str(), g_cfg.hubPort);
    if (!adopted) {
        Serial.println("ble: waiting for adopt");
        Panel::showProvisioning(clientId);
        BleProvision::Config received = BleProvision::waitForAdopt(clientId);
        saveConfig(received);
        Serial.printf("ble: adopted hub=%s:%d — restarting\n",
                      received.hubHost.c_str(), received.hubPort);
        // Clean WiFi bring-up after NimBLE teardown.
        delay(200);
        ESP.restart();
    }

    {
        int mv = Power::batteryMillivolts();
        Serial.printf("battery: %d%% (%dmV)\n",
                      Power::batteryPercentFromMillivolts(mv), mv);
    }
    Power::beginTimeEnv();

    // MqttHub's report topic / connected-handler are cheap, no-I/O setup --
    // safe to configure even when booting straight into offline mode, so
    // goOnline() (KEY2 toggle, later) doesn't need to redo it.
    MqttHub::setReportTopic(reportTopic.c_str());
    MqttHub::setConnectedHandler([]() {
        Serial.println("mqtt: login");
        sendLogin();
    });

    if (g_offlineMode) {
        // Skip WiFi/MQTT entirely -- offline mode means the radio stays off
        // for battery life, not just "don't act on network messages" (see
        // goOffline()'s comment). WiFi.mode(WIFI_STA) above was only to read
        // the MAC for clientId; turn it back off now.
        WiFi.mode(WIFI_OFF);
        Serial.println("wifi: off (offline mode)");
    } else if (!goOnline()) {
        Serial.println("wifi: giving up; restarting in 5s");
        delay(5000);
        ESP.restart();
    }

    if (!g_offlineMode && pendingShowReady()) {
        for (int i = 0; i < 80 && !MqttHub::connected(); i++) {
            MqttHub::loopWait(100, onMqttMessage);
        }
        if (MqttHub::connected()) {
            Serial.println("panel: Connected (post-adopt)");
            Panel::showConnected(g_portrait);
            clearShowReady();
        } else {
            Serial.println("panel: Connected deferred (mqtt not up yet)");
        }
    }

    lastHeartMs = millis();
}

void loop() {
    pollOrientationButton();
    pollResetButton();
    pollModeButton();  // may flip g_offlineMode via goOnline()/goOffline() mid-call

#if ENABLE_SD_CACHE
    // Offline-mode cycling: a real playlist.json (manually authored,
    // matching the real InkJoy schema) takes priority when present;
    // otherwise fall back to cycling through whatever ContentCache has
    // organically accumulated from live plays (see content_cache.h) --
    // so a frame that's never had a playlist.json placed on it still
    // shows a real slideshow in offline mode instead of a static image.
    // Renders the first frame immediately on entering offline mode/boot
    // (armOfflineCycleNow() force-arms lastPlaylistRenderMs -- see its own
    // comment for why leaving it at the default 0 isn't enough on a fresh
    // boot), then respects schedule.interval_min (playlist.json) or
    // kCacheCycleIntervalMs (cache fallback, no schedule to read) thereafter.
    if (g_offlineMode && Playlist::loaded() &&
        millis() - lastPlaylistRenderMs >= Playlist::intervalMs()) {
        String path = Playlist::currentFramePath();
        Serial.printf("playlist: rendering %s\n", path.c_str());
        // SdCard::end() (setup(), and after each play) powers the SDMMC
        // peripheral down between uses -- renderBinFromFile() itself just
        // assumes an already-mounted card (it's shared with the play path,
        // where SdCard::begin() already ran first), so re-mount here too.
        if (SdCard::begin() && Panel::renderBinFromFile(path)) {
            Playlist::advance();
        } else {
            Serial.println("playlist: render failed, will retry next interval");
        }
        SdCard::end();
        lastPlaylistRenderMs = millis();
    } else if (g_offlineMode && !Playlist::loaded() &&
               millis() - lastPlaylistRenderMs >= kCacheCycleIntervalMs) {
        int orientation = heartOrientation();
        if (!SdCard::begin()) {
            Serial.println("content_cache: SD mount failed, skipping this cycle");
        } else {
            size_t count = ContentCache::frameCount(orientation);
            if (count == 0) {
                Serial.println("content_cache: nothing cached yet for offline cycling");
            } else {
                g_cacheFrameIndex %= count;  // orientation may have flipped since last cycle
                Serial.printf("content_cache: cycling frame %u/%u\n",
                              (unsigned)g_cacheFrameIndex, (unsigned)count);
                if (ContentCache::renderFrame(g_cacheFrameIndex, orientation)) {
                    g_cacheFrameIndex = (g_cacheFrameIndex + 1) % count;
                } else {
                    Serial.println("content_cache: cycle render failed, will retry next interval");
                }
            }
        }
        SdCard::end();
        lastPlaylistRenderMs = millis();
    }
#endif

    if (g_offlineMode) {
        // WiFi is off (see goOffline()) -- MQTT, heart, and the wifi_sleep
        // window below all require it, so none of that applies until KEY2
        // goes back online. Just pace the button/playlist polling above.
        delay(50);
        return;
    }

    uint32_t now = millis();
    uint32_t untilHeart = (now - lastHeartMs >= HEART_INTERVAL_MS)
                              ? 0
                              : (HEART_INTERVAL_MS - (now - lastHeartMs));
    // Do NOT light-sleep here: that freezes lwIP/MQTT so TCP never ACKs and
    // QoS0 play publishes expire on the broker. Modem-sleep (MAX_MODEM) still
    // naps the radio between DTIMs while the MQTT task stays runnable.
    // Cap wait for KEY1/KEY3 polling + wifi_sleep checks.
    uint32_t waitMs = untilHeart < 200UL ? untilHeart : 200UL;
    if (waitMs == 0) waitMs = 50;

    MqttHub::loopWait(waitMs, onMqttMessage);

    if (pendingShowReady() && MqttHub::connected()) {
        Serial.println("panel: Connected (post-adopt)");
        Panel::showConnected(g_portrait);
        clearShowReady();
    }

    if (millis() - lastHeartMs >= HEART_INTERVAL_MS) {
        handleBatteryLevel(sendHeart());  // checks/reconnects WiFi only when a heart is due
        lastHeartMs = millis();
    }

    int nowMin = Power::nowMinutesSinceMidnight();
    if (millis() >= g_loginSettleUntilMs &&
        Power::withinWindow(nowMin, savedSleepBeginMin, savedSleepEndMin)) {
        if (MqttHub::connected()) {
            publish(InkJoy::buildSleep(clientId, Power::batteryPercent(),
                                         InkJoy::SLEEP_REASON_SCHEDULED));
            delay(200);  // let the publish flush before the radio drops
        }
        MqttHub::stop();
        Power::deepSleepSeconds((uint64_t)Power::secondsUntil(nowMin, savedSleepEndMin));
    }
}
