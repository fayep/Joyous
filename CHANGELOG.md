# Changelog

Notable changes to **Joyous Hub** and related tools in this repository.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Samsung battery history** — pre-sleep readings during normal push cycles are appended to `samsung_battery_history.json` (up to 365 samples per frame). The UI shows level, delta since last push, and recent readings without wake-only polls.

### Fixed

- **UI polling** — removed global Samsung MDC poll and 5s device refresh on every tab; Album does not query frames. Registry refresh is tab-scoped; Send from Album loads the device list once on click.
- **Samsung send when remote wake fails** — after WoL/magic timeout, hub polls MDC every 5s for up to 5 minutes so a manual power-button wake can complete the push; UI prompts “Press power button on frame…” after 20s.
- **Samsung battery wake** — wake polling now matches the E-Paper app more closely (3s initial delay, 45s timeout, 3s MDC probes, WoL/magic resend every 10s). Fixes false wake failures on battery when the first TCP attempt consumed the entire 15s window.

## [2026-06-21]

Samsung e-paper frames should see a large reduction in idle power draw after this release: the hub no longer leaves the display awake after a push, and battery is sampled on the same MDC session used to send sleep — not via a separate connect cycle.

### Added

- **Samsung wake / sleep control** — WoL plus Samsung magic UDP (`SHA256(MAC:E-Paper)` → port 10194), then MDC TLS on `:1515`. Moon (wake) and power (sleep) buttons in the Samsung settings UI.
- **Wake → push → sleep pipeline** — optional auto-sleep after image send (configurable delay; default 15s). Frame is woken only for delivery, then put back to network standby.
- **Pre-sleep battery read** — `0x1B` / `0x73` on the same MDC session as sleep-now (`0x11` / `0x00`), with connect retries while the panel finishes refreshing. No second dial for battery vs sleep.
- **WiFi MAC field** — stored for wake-on-send and manual wake (from Samsung E-Paper app device info).
- **InkJoy synthetic acks** — hub emits frame-shaped progress/result MQTT acks so the UI reflects push state when the real device acks are missing.
- **Display preview** — last pushed image thumbnail on InkJoy and Samsung device rows.
- **Version-sealed builds** — `linkmeta` embeds build metadata; `make build-release` with `JOYOUS_SEAL`.
- **`wipe_petals`** transition mask for InkJoy wipes.

### Changed

- **Samsung “active” status** — requires recent proof the frame was awake (MDC session, frame-originated `content.json` / `.png`, etc.). SSDP discover, hub UI preview loads, and post-sleep state no longer flip the badge to active.
- Hub frame list sorting, Samsung editor layout, and outbound/access logging improvements.

### Fixed

- False **active** reading after hub restart or opening Samsung settings (preview PNG was updating `LastSeen` as if the frame had contacted the hub).
- Separate MDC sessions for battery vs sleep failing when the frame was still busy after e-ink refresh.

### Samsung power & battery notes

| Action | Wakes frame? | Battery read? |
|--------|--------------|---------------|
| Send image (auto-sleep on) | Yes (once) | Yes, on sleep session |
| Manual sleep button | Only if already reachable | Yes, if MDC connects |
| Manual wake button | Yes | No |
| `/api/samsung/poll` (UI reachability poll) | Yes | Yes — avoid frequent use on battery |

**Recommended:** leave auto-sleep enabled and rely on pre-sleep readings during pushes to learn battery level. Periodic poll is for reachability checks, not everyday telemetry.

Battery percentage and power source (AC / USB / wireless) are stored on the device record in `devices.json`. A rolling history lives in `samsung_battery_history.json`; push-cycle **pre_sleep** samples drive the “since last push” delta in the UI.
