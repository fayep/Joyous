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
    if (now > 1700000000) { // sane post-2023 epoch => clock has synced
        uint64_t ms = (uint64_t)now * 1000ULL + (millis() % 1000UL);
        char buf[24];
        snprintf(buf, sizeof(buf), "%llu", (unsigned long long)ms);
        return String(buf);
    }
    return String(millis());
}

uint64_t parseMsgIdMs(JsonDocument &doc) {
    const char *s = doc["msgid"] | "";
    if (!s || !*s) return 0;
    return strtoull(s, nullptr, 10);
}

String buildLogin(const String &clientId, const String &sleepBegin, const String &sleepEnd,
                   const P2Ink p2cal[6]) {
    JsonDocument doc;
    doc["action"] = "login";
    doc["clientid"] = clientId;
    doc["stamac"] = clientId;
    doc["msgid"] = nowMsgId();
    JsonObject data = doc["data"].to<JsonObject>();
    data["inkjoy"] = true;
    char ver[32];
    snprintf(ver, sizeof(ver), "M H:%d T:%d F:%s", HW_MODEL, FW_PROTOCOL_T, FW_VERSION);
    data["ver"] = ver;
    data["statype"] = 3;
    data["sleep_mode"] = 2;
    data["sleep_begin_time"] = sleepBegin;
    data["sleep_end_time"] = sleepEnd;
    JsonArray p2 = data["p2"].to<JsonArray>();
    for (int i = 0; i < 6; i++) {
        JsonArray ink = p2.add<JsonArray>();
        ink.add(p2cal[i].r);
        ink.add(p2cal[i].g);
        ink.add(p2cal[i].b);
    }
    String out;
    serializeJson(doc, out);
    return out;
}

String buildHeart(const String &clientId, int batteryPct, int rssi,
                  const String &wifiName, int orientation) {
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
    data["orientation"] = orientation;
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
    // NOT img0["hash"].as<String>() -- ArduinoJson v7's String converter
    // serializes an absent/null variant as the literal text "null" rather
    // than "" (see its convertFromJson: falls back to serializeJson() when
    // the value isn't a JsonString), so that would make an absent hash
    // read as the non-empty string "null" and wrongly route every legacy
    // play (real InkJoy wire msgs, and any hub predating ContentCache)
    // through ContentCache with a bogus cache key instead of falling
    // through to the plain HTTP render below. `| ""` sidesteps the
    // converter entirely for the missing-key case -- confirmed the hard
    // way on real hardware (2026-08-11): this caused every play to fail
    // with RESULT_FAILED on a device with no SD card at all.
    out.img.hash = img0["hash"] | "";
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

static void parseOverlayLine(JsonObject obj, OverlayLine &out) {
    // `| ""` not `.as<String>()` for text/font -- see parsePlay's hash
    // comment for why an absent key must not read back as the literal
    // string "null".
    out.text = obj["text"] | "";
    out.font = obj["font"] | "";
    out.x = obj["x"] | 0;
    out.y = obj["y"] | 0;
}

bool parseOverlay(JsonDocument &doc, OverlayMsg &out) {
    JsonObject data = doc["data"];
    if (data.isNull()) return false;
    out.msgId = doc["msgid"].as<String>();

    JsonArray lines = data["lines"];
    out.lineCount = 0;
    for (JsonObject ln : lines) {
        if (out.lineCount >= (int)(sizeof(out.lines) / sizeof(out.lines[0]))) break;
        parseOverlayLine(ln, out.lines[out.lineCount]);
        out.lineCount++;
    }

    JsonObject caption = data["caption"];
    out.hasCaption = !caption.isNull();
    if (out.hasCaption) {
        parseOverlayLine(caption, out.caption);
    }
    return true;
}

} // namespace InkJoy
