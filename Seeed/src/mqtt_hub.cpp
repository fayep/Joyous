#include "mqtt_hub.h"

#include <freertos/FreeRTOS.h>
#include <freertos/event_groups.h>
#include <freertos/queue.h>
#include <mqtt_client.h>
#include <string.h>

namespace MqttHub {
namespace {

constexpr int kQueueDepth = 4;
constexpr int kMaxPayload = 2048;

struct QueuedMsg {
    int len;
    char data[kMaxPayload];
};

esp_mqtt_client_handle_t g_client = nullptr;
EventGroupHandle_t g_events = nullptr;
QueueHandle_t g_queue = nullptr;
volatile bool g_connected = false;

String g_uri;
String g_clientId;
String g_subTopic;
String g_reportTopic;
ConnectedHandler g_onConnected;

char g_accum[kMaxPayload];
int g_accumLen = 0;
int g_accumTotal = 0;

constexpr EventBits_t kBitWork = BIT0;
constexpr EventBits_t kBitConnected = BIT1;

static void enqueuePayload(const char *data, int len) {
    if (!g_queue || len <= 0 || len >= kMaxPayload) return;
    QueuedMsg msg;
    msg.len = len;
    memcpy(msg.data, data, len);
    msg.data[len] = '\0';
    if (xQueueSend(g_queue, &msg, 0) == pdTRUE) {
        xEventGroupSetBits(g_events, kBitWork);
    }
}

static void handleEvent(void * /*arg*/, esp_event_base_t /*base*/, int32_t event_id, void *event_data) {
    auto *event = static_cast<esp_mqtt_event_handle_t>(event_data);
    switch (static_cast<esp_mqtt_event_id_t>(event_id)) {
        case MQTT_EVENT_CONNECTED:
            g_connected = true;
            if (g_subTopic.length()) {
                esp_mqtt_client_subscribe(g_client, g_subTopic.c_str(), 0);
            }
            xEventGroupSetBits(g_events, kBitConnected);
            break;
        case MQTT_EVENT_DISCONNECTED:
            g_connected = false;
            break;
        case MQTT_EVENT_DATA: {
            if (event->current_data_offset == 0) {
                g_accumLen = 0;
                g_accumTotal = event->total_data_len;
            }
            if (event->data_len > 0 && g_accumLen + event->data_len < kMaxPayload) {
                memcpy(g_accum + g_accumLen, event->data, event->data_len);
                g_accumLen += event->data_len;
            }
            if (g_accumTotal > 0 && g_accumLen >= g_accumTotal) {
                enqueuePayload(g_accum, g_accumLen);
                g_accumLen = 0;
                g_accumTotal = 0;
            }
            break;
        }
        default:
            break;
    }
}

}  // namespace

bool begin(const char *host, int port, const char *clientId, const char *subTopic) {
    if (!g_events) g_events = xEventGroupCreate();
    if (!g_queue) g_queue = xQueueCreate(kQueueDepth, sizeof(QueuedMsg));
    if (!g_events || !g_queue) return false;

    g_clientId = clientId;
    g_subTopic = subTopic;
    g_uri = String("mqtt://") + host + ":" + String(port);

    if (g_client) {
        esp_mqtt_client_stop(g_client);
        esp_mqtt_client_destroy(g_client);
        g_client = nullptr;
    }
    g_connected = false;

    // Flat esp_mqtt_client_config_t (Arduino-ESP32 / IDF 4-style in the
    // PlatformIO framework-arduinoespressif32 package on this machine).
    esp_mqtt_client_config_t cfg = {};
    cfg.uri = g_uri.c_str();
    cfg.client_id = g_clientId.c_str();
    cfg.disable_clean_session = true;
    cfg.keepalive = 120;
    cfg.buffer_size = kMaxPayload;
    cfg.out_buffer_size = kMaxPayload;

    g_client = esp_mqtt_client_init(&cfg);
    if (!g_client) return false;

    esp_mqtt_client_register_event(g_client, MQTT_EVENT_ANY, handleEvent, nullptr);
    return esp_mqtt_client_start(g_client) == ESP_OK;
}

void stop() {
    if (g_client) {
        esp_mqtt_client_stop(g_client);
        esp_mqtt_client_destroy(g_client);
        g_client = nullptr;
    }
    g_connected = false;
}

void setReportTopic(const char *topic) { g_reportTopic = topic; }

void setConnectedHandler(ConnectedHandler handler) { g_onConnected = std::move(handler); }

bool publish(const char *payload, int len) {
    if (!g_client || !g_connected || g_reportTopic.isEmpty()) return false;
    return esp_mqtt_client_publish(g_client, g_reportTopic.c_str(), payload, len, 0, 0) >= 0;
}

bool publish(const String &payload) { return publish(payload.c_str(), (int)payload.length()); }

bool connected() { return g_connected; }

void loopWait(uint32_t timeoutMs, const MessageHandler &handler) {
    if (!g_events || !g_queue) {
        vTaskDelay(pdMS_TO_TICKS(timeoutMs ? timeoutMs : 50));
        return;
    }

    EventBits_t bits = xEventGroupWaitBits(
        g_events, kBitWork | kBitConnected, pdTRUE, pdFALSE,
        timeoutMs == 0 ? 0 : pdMS_TO_TICKS(timeoutMs));

    if ((bits & kBitConnected) && g_onConnected) {
        g_onConnected();
    }

    QueuedMsg msg;
    while (xQueueReceive(g_queue, &msg, 0) == pdTRUE) {
        if (handler) handler(msg.data, msg.len);
    }
}

}  // namespace MqttHub
