# GooDisplay ESP32-133C02: DC/X pin confirmation via firmware disassembly

Confirms `TFT_DC = GPIO2` in `Firmware/include/tft_setup_goodisplay.h` — first
found by hand-tracing the board's real DC/X breakout pad on connector H1
back to the ESP32-S3-WROOM-1 module, then independently confirmed here by
disassembling the vendor's own production firmware (dumped from this exact
unit before it was reflashed — see `GooDisplay/dumps/app0.bin`).

## Tools

- **Capstone 6.0.0a10** (PyPI pre-release) — the first Capstone line with
  Xtensa support (`CS_ARCH_XTENSA`; stable 5.x does not have it). Used for
  quick literal-pool scans (`l32r` target resolution) and single-address
  spot checks. Its linear-sweep mode is not reliable end-to-end: Xtensa
  interleaves literal pools directly in `.text`, and a blind sweep can walk
  into pool data and decode it as if it were code, producing garbage
  (confirmed empirically — several early leads turned out to be misaligned
  decodes, not real instructions).
- **`xtensa-esp32s3-elf-objdump`** (real ESP-IDF toolchain, already present
  locally at `~/.platformio/packages/toolchain-xtensa-esp32s3/bin/`) — the
  authoritative disassembler for resolving call/branch targets. Its raw
  binary mode (`-b binary -m xtensa`) segfaults on this build
  (`crosstool-NG esp-2021r2-patch5`, binutils 2.35.1) regardless of flags.
  Workaround: wrap each raw segment in a minimal ELF via
  `xtensa-esp32s3-elf-objcopy -I binary -O elf32-xtensa-le --adjust-vma=<load_addr>`,
  then run normal `objdump -d` on that — this path is solid and fast
  (~715KB IROM segment disassembles in well under a second).
- Binary Ninja headless was evaluated first but this container has no
  Xtensa architecture plugin installed; Unicorn (and anything built on it,
  incl. Qiling) has no Xtensa support at all as of this session, ruling out
  an emulation-based approach (e.g. porting `gerald`, a RISC-V-specific
  firmware-mapping tool built on Unicorn, to this target) without a much
  larger investment (porting QEMU's existing `target/xtensa` into Unicorn).

## Segment layout (`app0.bin`, parsed from the ESP image header)

| # | load_addr | len | contents |
|---|---|---|---|
| 0 | `0x3c0b0020` | 0x128b8c | DROM (flash-mapped rodata) |
| 1 | `0x3fc97e00` | 0x5484 | DRAM (.data init) |
| 2 | `0x40374000` | 0x1fd8 | IRAM (early boot) |
| 3 | `0x42000020` | 0xae8a4 | **IROM (main app .text)** |
| 4 | `0x40375fd8` | 0x11d54 | IRAM (perf-critical code) |
| 5 | `0x600fe000` | 0x20 | RTC memory |

## Methodology

Rather than a blind call-graph walk from the reset vector (which would
spend most of its time in unrelated WiFi/BT/TLS code before ever reaching
the display driver), searched for known, highly-specific constants first:

1. **Panel register init table.** `Firmware/src/panel_goodisplay.cpp`
   already has GooDisplay's own register value tables, taken directly from
   their `GDEP133C02.c` reference (e.g. `AN_TM_V = {0xC0,0x1E,0x1E,0xCE,
   0xCE,0xCE,0x15,0x15,0x55}`, `TRES_V = {0x04,0xB0,0x03,0x20}`,
   `PWR_V = {0x0F,0x00,0x28,0x2C,0x28,0x38}`, etc). Searched `app0.bin` for
   these exact byte sequences — found all of them clustered within ~40
   bytes of each other in DROM (`0x3c0cb6f6`-`0x3c0cb71d`), confirming this
   is genuinely the same register table, verbatim, in the real production
   firmware.
2. Found the literal pointer table referencing these values (right at the
   start of IROM, `0x42000020`+), then found the code that walks that
   table one entry at a time — the real `initEPD()`-equivalent.
3. **GPIO peripheral base.** Searched for `0x60004000` (ESP32-S3's GPIO
   register base) as a raw literal. Found a function that computes
   `GPIO_ENABLE_W1TS_REG` (`base+0x24`) writes from a runtime pin argument
   — generic `gpio_set_direction()`-style code, confirmed by shape but not
   pin-specific by itself.
4. **SPI peripheral base.** Searched for `0x60024000`/`0x60025000`
   (GPSPI2/GPSPI3 bases) — found the internal `spi_host_device_t` ->
   peripheral-base resolver (SPI3_HOST case literally loads
   `0x60025000`, matching our own driver's `SPI3_HOST` selection).

## Findings

- **CS0 = GPIO18, CS1 = GPIO17, confirmed.** Found a `setPinCsAll()`-
  equivalent (`0x42002dec`): calls a `gpio_set_level(pin, value)`-style
  helper (`0x420102dc`) twice, once with `pin=18` once with `pin=17`, both
  with the same value — exactly matching `EPD_CS0`/`EPD_CS1` and our own
  driver's `setPinCsAll()`.
- **DC/X = GPIO2, confirmed.** Found a `writecommand()`-equivalent
  (`0x42002e04`): unconditionally calls `gpio_set_level(pin=2, value=0)`
  (DC forced LOW), then a single-byte SPI write helper (`0x42002dbc`,
  which extracts one byte from its argument and calls an SPI write
  function). Called from the panel-init sequence in exactly the expected
  position: after `setPinCsAll(0)`, before the register-table transmit
  call. This independently confirms the hand-traced hardware finding
  (H1's DC/X pad -> a via near U6 -> back of board -> ESP32-S3-WROOM-1
  module pin 38, which Espressif's own WROOM-1 datasheet Table 3-1 lists
  as `IO2`/GPIO2).

## Open question: does this mean the panel needs DC after all?

The vendor firmware toggling a real DC pin looks at first like evidence
the panel is strapped to the datasheet's "4-wire SPI" mode (Table 6-1,
BS1=L/BS0=H — the mode that actually reads D/C#), contradicting the
"Standard 4-wire SPI" (BS1=H/BS0=H, D/C# tied to GND, ignored) mode our
own driver and Seeed_GFX both rely on successfully.

Weighed against that: our own driver has never toggled GPIO2 (or any
DC-equivalent) at all, using only ESP-IDF's positional command-phase SPI
framing (`spi_transaction_t.cmd`) -- and it has produced correct,
repeatable results (ruler test, oriented letter test, and real InkJoy
image sends through joyous-hub). If the panel genuinely needed DC to
distinguish command from data, a driver that never drives DC at all
should fail unpredictably, not work consistently.

Best current read: the vendor's firmware is generic/shared across
multiple GooDisplay board variants with potentially different BS0/BS1
strapping, and defensively toggles DC on all of them -- harmless on boards
(like this one) where the panel is actually in Standard 4-wire SPI mode
and doesn't read that pin, load-bearing on variants where it is. Not
conclusively proven (would need to physically probe BS0/BS1 or the real
D/C# pin's strap to fully close this out), but consistent with all
evidence gathered so far.
