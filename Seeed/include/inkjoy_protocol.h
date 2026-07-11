#pragma once
#include <Arduino.h>
#include <ArduinoJson.h>

// Message envelopes and action schema reverse-engineered from the real InkJoy
// frame (ESP32-C5, fw v0.5.6) — see research/firmware-notes.md at the repo
// root for the full protocol writeup. This implements only what the hub
// (joyous-hub, local broker on HUB_MQTT_PORT) actually needs: login, heart,
// play (+ack), wifi_sleep (+ack), shutdown.

namespace InkJoy {

// play_ack / wifi_sleep_ack result bitfield (low byte) — see firmware-notes.md
// "Ack result bitfield". 106=accepted, 104=failed/interrupted, 113=done,
// 182/184/186/188=20/40/60/80% download progress.
constexpr int RESULT_ACCEPTED = 106;
constexpr int RESULT_FAILED = 104;
constexpr int RESULT_DONE = 113;

// Maps percent (20/40/60/80) to its progress result code; returns -1 for any
// other input.
int progressResult(int percent);

// Unix-ms timestamp as a string, for the envelope "msgid" field. Requires
// NTP sync (see power.cpp); falls back to a millis()-based value (still
// unique within a boot, just not wall-clock-accurate) if unsynced.
String nowMsgId();

String buildLogin(const String &clientId, const String &sleepBegin, const String &sleepEnd);
String buildHeart(const String &clientId, int batteryPct, int rssi, const String &wifiName);
String buildAck(const char *action, const String &clientId, const String &ackMsgId, int result);

// Sent before power-off. Wire action is "sleep" (not "shutdown" — an
// earlier, uncaptured guess in these docs turned out wrong; see
// research/firmware-notes.md). `reason` enum values aren't fully mapped yet;
// 2 is confirmed for our own wifi_sleep-window trigger.
constexpr int SLEEP_REASON_SCHEDULED = 2;
String buildSleep(const String &clientId, int batteryPct, int reason);

struct PlayImage {
    String imgId;
    String imgUrl;
};

struct PlayMsg {
    String msgId;
    String host;
    int port = 0;
    PlayImage img;
};

struct SleepWindow {
    String beginTime; // "H:MM", no zero-pad
    String endTime;
    String ackMsgId;
};

// `doc` must already hold a deserialized action envelope (action == "play").
bool parsePlay(JsonDocument &doc, PlayMsg &out);

// `doc` must already hold a deserialized action envelope (action == "wifi_sleep").
bool parseSleepWindow(JsonDocument &doc, SleepWindow &out);

} // namespace InkJoy
