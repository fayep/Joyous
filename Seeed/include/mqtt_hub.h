#pragma once

#include <Arduino.h>
#include <functional>

// esp-mqtt wrapper: IDF client runs its own task. Inbound InkJoy payloads are
// queued so play/HTTP work runs on the Arduino loop, not the MQTT task.
// loopWait() blocks until a queued message or timeout (heart / sleep checks).

namespace MqttHub {

using MessageHandler = std::function<void(const char *payload, int len)>;
using ConnectedHandler = std::function<void()>;

bool begin(const char *host, int port, const char *clientId, const char *subTopic);
void stop();

void setReportTopic(const char *topic);
void setConnectedHandler(ConnectedHandler handler);

bool publish(const char *payload, int len);
bool publish(const String &payload);
bool connected();

void loopWait(uint32_t timeoutMs, const MessageHandler &handler);

}  // namespace MqttHub
