#include "ble_provision.h"
#include <NimBLEDevice.h>
#include <ArduinoJson.h>

// Wire format reverse-engineered from joyous-hub/ble.go (AdoptBLEFrame),
// already proven against a real InkJoy frame:
//   packet := [type, 0x00, seq, len, ...payload]
//   type 8  op mode        payload=[1] (STA) — informational, no action needed
//   type 9  WiFi SSID      payload=ssid bytes
//   type 13 WiFi password  payload=password bytes
//   type 12 end of wifi    payload=empty — SSID+password are now complete
//   type 77 mqtt_config    payload=JSON {"data":{"host","port","usr","pwd"}}
namespace BleProvision {

namespace {

constexpr const char *SERVICE_UUID = "0000ffff-0000-1000-8000-00805f9b34fb";
constexpr const char *CHAR_UUID = "0000ff01-0000-1000-8000-00805f9b34fb";

constexpr uint8_t TYPE_OP_MODE = 8;
constexpr uint8_t TYPE_WIFI_SSID = 9;
constexpr uint8_t TYPE_WIFI_END = 12;
constexpr uint8_t TYPE_WIFI_PASSWORD = 13;
constexpr uint8_t TYPE_MQTT_CONFIG = 77;

volatile bool g_wifiDone = false;
volatile bool g_mqttDone = false;
String g_ssid;
String g_wifiPass;
Config g_result;

class WriteCallbacks : public NimBLECharacteristicCallbacks {
    void onWrite(NimBLECharacteristic *chr) override {
        NimBLEAttValue raw = chr->getValue();
        if (raw.length() < 4) return;

        const uint8_t *data = raw.data();
        uint8_t type = data[0];
        uint8_t len = data[3];
        if (raw.length() < (size_t)(4 + len)) return;
        String payload;
        payload.concat((const char *)(data + 4), len);

        switch (type) {
            case TYPE_OP_MODE:
                break; // no action needed — we're always STA
            case TYPE_WIFI_SSID:
                g_ssid = payload;
                break;
            case TYPE_WIFI_PASSWORD:
                g_wifiPass = payload;
                break;
            case TYPE_WIFI_END:
                g_result.ssid = g_ssid;
                g_result.wifiPass = g_wifiPass;
                g_wifiDone = true;
                break;
            case TYPE_MQTT_CONFIG: {
                JsonDocument doc;
                if (deserializeJson(doc, payload)) break;
                JsonObject data = doc["data"];
                if (data.isNull()) break;
                g_result.hubHost = data["host"] | "";
                g_result.hubPort = data["port"] | 0;
                g_result.mqttUser = data["usr"] | "";
                g_result.mqttPass = data["pwd"] | "";
                g_mqttDone = true;
                break;
            }
            default:
                break;
        }
    }
};

WriteCallbacks g_callbacks;

} // namespace

Config waitForAdopt(const String &clientId) {
    g_wifiDone = false;
    g_mqttDone = false;
    g_ssid = "";
    g_wifiPass = "";
    g_result = Config{};

    String name = "IJ_" + clientId;
    NimBLEDevice::init(name.c_str());

    NimBLEServer *server = NimBLEDevice::createServer();
    NimBLEService *service = server->createService(SERVICE_UUID);
    NimBLECharacteristic *chr = service->createCharacteristic(
        CHAR_UUID, NIMBLE_PROPERTY::WRITE | NIMBLE_PROPERTY::WRITE_NR);
    chr->setCallbacks(&g_callbacks);
    service->start();

    NimBLEAdvertising *adv = NimBLEDevice::getAdvertising();
    adv->addServiceUUID(SERVICE_UUID);
    adv->start();

    while (!(g_wifiDone && g_mqttDone)) {
        delay(100);
    }

    adv->stop();
    NimBLEDevice::deinit(true);

    return g_result;
}

} // namespace BleProvision
