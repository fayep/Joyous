# Samsung EM32DX

Tools and reference material for the Samsung E-Paper display (MDC push, protocol RE).

## Android app (reference APK)

Used to reverse-engineer MDC commands (battery, content push, settings).

| Field | Value |
|-------|-------|
| App | Samsung E-Paper |
| Package | `com.samsung.android.ePaperApp` |
| Version | **1.3.2_5073_0429** (version code **5073**) |

Place the base APK here (gitignored):

```
Samsung/com.samsung.android.ePaperApp.apk
```

The Play Store bundle is a split XAPK (`manifest.json` at repo root lists `config.arm64_v8a`, `config.en`, `config.xxxhdpi`). The base APK alone is enough for jadx decompilation of MDC/Kotlin code; native libs live in the arm64 split if you need those.

Decompile locally:

```bash
jadx -d Samsung/epaper-decompiled --no-res Samsung/com.samsung.android.ePaperApp.apk
```

Key classes for battery: `com.samsung.android.ePaper.data.mdc.MDCBatteryCommand`, `MDCConstant.MDC_COMMAND_BATTERY` (cmd `0x1B`, subcmd `0x73`).

## Local push script

`samsung_serve.py` — SSDP discovery, HTTP serve, and MDC content download (same TLS/PIN flow as the app).
