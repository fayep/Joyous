#pragma once
#include <Arduino.h>

// BLE (BluFi-style) provisioning peer for the board. Speaks the exact wire
// format joyous-hub's existing AdoptBLEFrame already pushes to real InkJoy
// frames (joyous-hub/ble.go): advertises as "IJ_<MAC>" with GATT service
// 0000ffff / write characteristic 0000ff01, and accepts the same
// [type, 0x00, seq, len, ...data] packet sequence (op mode, SSID, password,
// end-of-wifi, then a JSON mqtt_config blob). No InkJoy app or cloud
// involved — this exists so the hub's own "scan for IJ_ frames" / "adopt"
// buttons (POST /api/inkjoy/ble/scan, /api/inkjoy/ble/adopt) work against
// this board the same way they already do against a real frame.
namespace BleProvision {

struct Config {
    String ssid;
    String wifiPass;
    String hubHost;
    int hubPort = 0;
    String mqttUser;
    String mqttPass;
};

// Starts advertising as "IJ_<clientId>" and blocks until a full config
// (WiFi credentials + mqtt_config JSON) has been received over BLE, then
// tears down the BLE stack and returns it.
Config waitForAdopt(const String &clientId);

} // namespace BleProvision
