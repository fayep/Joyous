#include "inkjoy_protocol.h"
#include "config.h"
#include <time.h>

namespace InkJoy {

int progressResult(int percent) {
    switch (percent) {
        case 20: return 182;
        case 40: return 184;
        case 60: return 186;
        case 80: return 188;
        default: return -1;
    }
}

String nowMsgId() {
    time_t now;
    time(&now);
    if (now > 1700000000) { // sane post-2023 epoch => NTP has synced
        uint64_t ms = (uint64_t)now * 1000ULL + (millis() % 1000UL);
        char buf[24];
        snprintf(buf, sizeof(buf), "%llu", (unsigned long long)ms);
        return String(buf);
    }
    return String(millis());
}

String buildLogin(const String &clientId, const String &sleepBegin, const String &sleepEnd) {
    JsonDocument doc;
    doc["action"] = "login";
    doc["clientid"] = clientId;
    doc["stamac"] = clientId;
    doc["msgid"] = nowMsgId();
    JsonObject data = doc["data"].to<JsonObject>();
    data["inkjoy"] = true;
    char ver[24];
    snprintf(ver, sizeof(ver), "M H:%d F:%s", HW_MODEL, FW_VERSION);
    data["ver"] = ver;
    data["statype"] = 3;
    data["sleep_mode"] = 2;
    data["sleep_begin_time"] = sleepBegin;
    data["sleep_end_time"] = sleepEnd;
    String out;
    serializeJson(doc, out);
    return out;
}

String buildHeart(const String &clientId, int batteryPct, int rssi, const String &wifiName) {
    JsonDocument doc;
    doc["action"] = "heart";
    doc["clientid"] = clientId;
    doc["stamac"] = clientId;
    doc["msgid"] = nowMsgId();
    JsonObject data = doc["data"].to<JsonObject>();
    data["type"] = 3;
    data["ack"] = 1;
    data["wifi"] = "on";
    data["wifi_name"] = wifiName;
    data["ble"] = "off";
    data["tf"] = "absent";
    data["tfsize"] = 0;
    data["tfused"] = 0;
    data["orientation"] = 0;
    data["battery"] = batteryPct;
    data["wifi_listen_iv"] = 50;
    data["wifi_rssi"] = rssi;
    data["wifi_ch"] = 0;
    data["ble_rssi"] = 0;
    data["version"] = FW_VERSION;
    String out;
    serializeJson(doc, out);
    return out;
}

String buildAck(const char *action, const String &clientId, const String &ackMsgId, int result) {
    JsonDocument doc;
    doc["action"] = action;
    doc["clientid"] = clientId;
    doc["stamac"] = clientId;
    doc["msgid"] = nowMsgId();
    JsonObject data = doc["data"].to<JsonObject>();
    data["ack_msgid"] = ackMsgId;
    data["result"] = result;
    String out;
    serializeJson(doc, out);
    return out;
}

String buildSleep(const String &clientId, int batteryPct, int reason) {
    JsonDocument doc;
    doc["action"] = "sleep";
    doc["clientid"] = clientId;
    doc["stamac"] = clientId;
    doc["msgid"] = nowMsgId();
    JsonObject data = doc["data"].to<JsonObject>();
    data["ack"] = 0;
    data["battery"] = batteryPct;
    data["reason"] = reason;
    String out;
    serializeJson(doc, out);
    return out;
}

bool parsePlay(JsonDocument &doc, PlayMsg &out) {
    JsonObject data = doc["data"];
    if (data.isNull()) return false;
    JsonArray imgs = data["imgs"];
    if (imgs.isNull() || imgs.size() == 0) return false;
    out.msgId = doc["msgid"].as<String>();
    out.host = data["host"].as<String>();
    out.port = data["port"] | 80;
    JsonObject img0 = imgs[0];
    out.img.imgId = img0["imgid"].as<String>();
    out.img.imgUrl = img0["imgurl"].as<String>();
    return true;
}

bool parseSleepWindow(JsonDocument &doc, SleepWindow &out) {
    JsonObject data = doc["data"];
    if (data.isNull()) return false;
    out.beginTime = data["beginTime"].as<String>();
    out.endTime = data["endTime"].as<String>();
    out.ackMsgId = doc["msgid"].as<String>();
    return true;
}

} // namespace InkJoy
