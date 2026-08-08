#include <Arduino.h>
#include <WiFi.h>
#include <ArduinoJson.h>
#include <TFT_eSPI.h>
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
#include "sd_card.h"
#include "playlist.h"

// EPaper is Seeed_Arduino_LCD's e-paper sprite/driver class (Extensions/EPaper.cpp);
// panel.cpp owns init/render, this file just needs the instance to exist.
EPaper epaper;

static String clientId;    // MAC without colons — also the frame's clientID/stamac
static String reportTopic; // /device/report/{MAC} — we publish here
static String subTopic;    // /inkjoyap/{MAC} — we subscribe here

// Sleep window from the hub's wifi_sleep action, in minutes-since-midnight.
// RTC memory survives our own deep-sleep cycles (not full power loss) so a
// window configured once keeps applying across nightly sleeps.
RTC_DATA_ATTR int savedSleepBeginMin = -1;
RTC_DATA_ATTR int savedSleepEndMin = -1;

static unsigned long lastHeartMs = 0;
static unsigned long lastPlaylistRenderMs = 0;
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
    publish(InkJoy::buildLogin(clientId, minToHHMM(savedSleepBeginMin), minToHHMM(savedSleepEndMin)));
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
    Panel::showLowBattery();
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
    handleBatteryLevel(sendHeart());
    lastHeartMs = millis();
}

static void pollOrientationButton() {
    // KEY1 is active-low with internal pull-up.
    bool down = digitalRead(BTN_ORIENT_PIN) == LOW;
    uint32_t now = millis();
    if (down && !g_orientBtnDown && (now - g_orientBtnAt) > 40) {
        g_orientBtnDown = true;
        g_orientBtnAt = now;
        toggleOrientation();
    } else if (!down && g_orientBtnDown && (now - g_orientBtnAt) > 40) {
        g_orientBtnDown = false;
        g_orientBtnAt = now;
    }
}

static void factoryReset() {
    Serial.println("nvs: KEY3 long-press — clearing ijcfg, rebooting to BLE adopt");
    clearConfig();
    MqttHub::stop();
    Panel::showFactoryReset();
    delay(500);
    ESP.restart();
}

static void pollResetButton() {
    // KEY3 long-press (active-low) clears adoption NVS and reboots to setup.
    bool down = digitalRead(BTN_RESET_PIN) == LOW;
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
        g_resetFired = false;
    }
}

static void onPlayProgress(int pct) {
    int result = InkJoy::progressResult(pct);
    if (result < 0) return;
    publish(InkJoy::buildAck("play_ack", clientId, g_currentPlayAckMsgId, result));
}

static void handlePlay(JsonDocument &doc) {
    InkJoy::PlayMsg play;
    if (!InkJoy::parsePlay(doc, play)) return;

    g_currentPlayAckMsgId = play.msgId;
    publish(InkJoy::buildAck("play_ack", clientId, play.msgId, InkJoy::RESULT_ACCEPTED));

    Power::setHighPerformance(true);
    bool ok = Panel::renderBinFromHttp(play.host, play.port, play.img.imgUrl, onPlayProgress);
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

    pinMode(BTN_ORIENT_PIN, INPUT_PULLUP);
    pinMode(BTN_RESET_PIN, INPUT_PULLUP);
    loadOrientation();

    Serial.println("panel: begin");
    Panel::begin();
    Serial.println("panel: ready");

    SdCard::begin();  // logs its own mount result; absence is non-fatal
    Playlist::begin();  // logs its own result; no card/playlist.json is non-fatal

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

    if (!connectWiFi()) {
        Serial.println("wifi: giving up; restarting in 5s");
        delay(5000);
        ESP.restart();
    }
    {
        int mv = Power::batteryMillivolts();
        Serial.printf("battery: %d%% (%dmV)\n",
                      Power::batteryPercentFromMillivolts(mv), mv);
    }
    Power::beginTimeEnv();

    MqttHub::setReportTopic(reportTopic.c_str());
    MqttHub::setConnectedHandler([]() {
        Serial.println("mqtt: login");
        sendLogin();
    });
    if (!MqttHub::begin(g_cfg.hubHost.c_str(), g_cfg.hubPort, clientId.c_str(),
                        subTopic.c_str(), g_cfg.mqttUser.c_str(),
                        g_cfg.mqttPass.c_str())) {
        Serial.println("mqtt: begin failed");
    }

    if (pendingShowReady()) {
        for (int i = 0; i < 80 && !MqttHub::connected(); i++) {
            MqttHub::loopWait(100, onMqttMessage);
        }
        if (MqttHub::connected()) {
            Serial.println("panel: Connected (post-adopt)");
            Panel::showConnected();
            clearShowReady();
        } else {
            Serial.println("panel: Connected deferred (mqtt not up yet)");
        }
    }

    lastHeartMs = millis();
}

void loop() {
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
    pollOrientationButton();
    pollResetButton();

    if (pendingShowReady() && MqttHub::connected()) {
        Serial.println("panel: Connected (post-adopt)");
        Panel::showConnected();
        clearShowReady();
    }

    if (millis() - lastHeartMs >= HEART_INTERVAL_MS) {
        handleBatteryLevel(sendHeart());  // checks/reconnects WiFi only when a heart is due
        lastHeartMs = millis();
    }

    // SD-card playlist.json cycling — independent of MQTT play, matching
    // the real InkJoy firmware's own local slideshow behavior when no
    // cloud content is pushed. Renders the first frame immediately on
    // boot (lastPlaylistRenderMs starts at 0), then respects
    // schedule.interval_min thereafter.
    if (Playlist::loaded() &&
        millis() - lastPlaylistRenderMs >= Playlist::intervalMs()) {
        String path = Playlist::currentFramePath();
        Serial.printf("playlist: rendering %s\n", path.c_str());
        if (Panel::renderBinFromFile(path)) {
            Playlist::advance();
        } else {
            Serial.println("playlist: render failed, will retry next interval");
        }
        lastPlaylistRenderMs = millis();
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
