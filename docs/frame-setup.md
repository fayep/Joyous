# Frame setup

Connect each frame once; after that, everything is in the Joyous web UI (`http://<host>:18080`). Install Joyous first (see the [README](../README.md)) and run the bridge for each kind of frame you have.

## InkJoy frames and the custom-firmware boards

`inkjoy-bridge` handles both InkJoy frames and the Seeed XIAO EE02 / GooDisplay ESP32-133C02 boards running [Joyous firmware](../Firmware/README.md). Joyous sets them up over Bluetooth, so `inkjoy-bridge` must run on a machine with Bluetooth. macOS asks for Bluetooth permission the first time.

1. Put the frame in setup mode:
   - An **InkJoy** frame that isn't set up yet advertises itself over Bluetooth as `IJ_<MAC>`.
   - A **custom-firmware board** does the same on first boot and shows that name on its screen. To flash one, follow [Firmware/README.md](../Firmware/README.md).
2. In the web UI, open the **InkJoy** tab and click **Find new frames**. Click **Adopt** next to `IJ_<MAC>`.
3. Enter the Wi-Fi network and password the frame should join, and confirm.

The frame saves the Wi-Fi details and the address of this host. It reconnects to `inkjoy-bridge` on port 11883 from then on, and appears under **Devices**.

**InkJoy's cloud.** By default the bridge relays an InkJoy frame's traffic to InkJoy's cloud, so the InkJoy app keeps working alongside Joyous. To keep frames entirely local, set `upstream: ""` in `inkjoy-config.yaml`. The custom-firmware boards are always local-only.

**After adopting:**
- **Orientation and sleep hours:** set them per frame on the **InkJoy** tab, which also has **Refresh display**.
- **Transition:** on the InkJoy 10" and 28.5", the **Transition** dropdown picks how the panel redraws. Luminance is the default and goes dark first, light last.
- **Color calibration:** optional; see [below](#colour-calibration).

## Samsung E-Paper (EM32DX)

`samsung-bridge` finds Samsung frames on your LAN, wakes them to deliver a photo, and puts them back to sleep. Battery level is read in the same session before sleep.

1. In Samsung's **E-Paper** app, connect the frame to your Wi-Fi and turn on **Network Standby** in its power settings. Without it the frame can't be woken remotely.
2. Note the frame's **Wi-Fi MAC** from the app's device info.
3. In the web UI, open the **Samsung** tab and click **Discover displays**.
   - Discovery uses SSDP on your LAN.
   - If nothing appears, set `discover_subnets` in `samsung-config.yaml` to your LAN (e.g. `192.168.1.0/24`) so the bridge searches it directly.
4. Open the frame and enter its **Wi-Fi MAC**, which is needed for wake-on-send. Leave **Sleep after send** on, with a delay long enough for the panel to finish refreshing (30–45 s is typical).

Seeing the frame as **Asleep** is normal: Joyous only wakes it to send. If a remote wake fails, press the frame's power button when the UI asks; Joyous keeps retrying for up to five minutes.

## Nixplay

`nixplay-bridge` signs in to your Nixplay account and uploads photos to your galleries. Nixplay's cloud then syncs them to your frames.

1. Set `keychain_account` to your Nixplay email in `nixplay-config.yaml`.
2. Store the password once:
   ```
   nixplay-bridge -set-password
   ```
   It's saved in the system credential store: the macOS Keychain, the Secret Service on Linux (GNOME Keyring or KWallet), or Windows Credential Manager.
3. Start the bridge. Your galleries appear under **Devices**, where you can send photos to them.

## Colour calibration

Joyous dithers each photo against the frame's actual inks. Each panel model starts with a measured standard palette, but individual panels vary. To calibrate one:

1. On the frame's card under **Devices**, open **Color calibration** and **Send calibration image**. The frame shows one band per ink.
2. Photograph the panel in daylight, square on.
   - Avoid reflections; your shadow over part of each band helps.
3. Enter each band's colour, read from the darkest, least-glare part of the photo, and **Save**.

The next photo sent to that frame uses the new values. **Reset to standard** returns the frame to its standard palette.
