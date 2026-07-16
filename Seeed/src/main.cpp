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
static String g_currentPlayAckMsgId;

struct RuntimeConfig {
    String ssid;
    String wifiPass;
    String hubHost;
    int hubPort = 0;
};
static RuntimeConfig g_cfg;

static bool loadConfig() {
    Preferences prefs;
    prefs.begin("ijcfg", true);
    g_cfg.ssid = prefs.getString("ssid", "");
    g_cfg.wifiPass = prefs.getString("pass", "");
    g_cfg.hubHost = prefs.getString("host", "");
    g_cfg.hubPort = prefs.getInt("port", 0);
    prefs.end();

    if (g_cfg.ssid.length() > 0 && g_cfg.hubHost.length() > 0) return true;

    g_cfg.ssid = WIFI_SSID;
    g_cfg.wifiPass = WIFI_PASSWORD;
    g_cfg.hubHost = HUB_HOST;
    g_cfg.hubPort = HUB_MQTT_PORT;
    return false;
}

static void saveConfig(const BleProvision::Config &adopted) {
    Preferences prefs;
    prefs.begin("ijcfg", false);
    prefs.putString("ssid", adopted.ssid);
    prefs.putString("pass", adopted.wifiPass);
    prefs.putString("host", adopted.hubHost);
    prefs.putInt("port", adopted.hubPort);
    prefs.end();

    g_cfg.ssid = adopted.ssid;
    g_cfg.wifiPass = adopted.wifiPass;
    g_cfg.hubHost = adopted.hubHost;
    g_cfg.hubPort = adopted.hubPort;
}

static String minToHHMM(int minutes) {
    if (minutes < 0) return "0:00";
    char buf[8];
    snprintf(buf, sizeof(buf), "%d:%02d", minutes / 60, minutes % 60);
    return String(buf);
}

static void publish(const String &payload) { MqttHub::publish(payload); }

static void sendLogin() {
    publish(InkJoy::buildLogin(clientId, minToHHMM(savedSleepBeginMin), minToHHMM(savedSleepEndMin)));
}

static int currentBatteryPct() {
    int batteryPct = 100;
#if BATTERY_ADC_PIN >= 0
    batteryPct = map(analogRead(BATTERY_ADC_PIN), 0, 4095, 0, 100);
#endif
    return batteryPct;
}

static void sendHeart() {
    publish(InkJoy::buildHeart(clientId, currentBatteryPct(), WiFi.RSSI(), WiFi.SSID()));
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

    bool ok = Panel::renderBinFromHttp(play.host, play.port, play.img.imgUrl, onPlayProgress);

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
    if (!strcmp(action, "play")) {
        handlePlay(doc);
    } else if (!strcmp(action, "wifi_sleep")) {
        handleWifiSleep(doc);
    }
}

static void connectWiFi() {
    WiFi.mode(WIFI_STA);
    WiFi.begin(g_cfg.ssid.c_str(), g_cfg.wifiPass.c_str());
    while (WiFi.status() != WL_CONNECTED) {
        delay(250);
    }
    // Nap between AP DTIM beacons while staying associated + MQTT-reachable.
    esp_wifi_set_ps(WIFI_PS_MIN_MODEM);
}

void setup() {
    Serial.begin(115200);

    Panel::begin();

    WiFi.mode(WIFI_STA);
    clientId = WiFi.macAddress();
    clientId.replace(":", "");
    clientId.toUpperCase();
    reportTopic = "/device/report/" + clientId;
    subTopic = "/inkjoyap/" + clientId;

    bool adopted = loadConfig();
    if (!adopted) {
        Panel::showProvisioning(clientId);
        BleProvision::Config received = BleProvision::waitForAdopt(clientId);
        saveConfig(received);
    }

    connectWiFi();
    Power::beginTimeSync();

    MqttHub::setReportTopic(reportTopic.c_str());
    MqttHub::setConnectedHandler([]() { sendLogin(); });
    if (!MqttHub::begin(g_cfg.hubHost.c_str(), g_cfg.hubPort, clientId.c_str(), subTopic.c_str())) {
        Serial.println("mqtt begin failed");
    }

    lastHeartMs = millis();
}

void loop() {
    uint32_t now = millis();
    uint32_t untilHeart = (now - lastHeartMs >= HEART_INTERVAL_MS)
                              ? 0
                              : (HEART_INTERVAL_MS - (now - lastHeartMs));
    // Also wake periodically to evaluate the wifi_sleep window.
    uint32_t waitMs = untilHeart < 60000UL ? untilHeart : 60000UL;

    MqttHub::loopWait(waitMs, onMqttMessage);

    if (millis() - lastHeartMs >= HEART_INTERVAL_MS) {
        if (MqttHub::connected()) sendHeart();
        lastHeartMs = millis();
    }

    int nowMin = Power::nowMinutesSinceMidnight();
    if (Power::withinWindow(nowMin, savedSleepBeginMin, savedSleepEndMin)) {
        if (MqttHub::connected()) {
            publish(InkJoy::buildSleep(clientId, currentBatteryPct(), InkJoy::SLEEP_REASON_SCHEDULED));
            delay(200);  // let the publish flush before the radio drops
        }
        MqttHub::stop();
        Power::deepSleepSeconds((uint64_t)Power::secondsUntil(nowMin, savedSleepEndMin));
    }
}
