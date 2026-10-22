# xtroadmap

Gerald-shaped mapmaking for ESP32-S3 firmware, without an emulator.

Unicorn has no Xtensa target. Capstone 6 does. This walks one function at
a time from an `entry` prologue, follows every direct `j` / `b*` destination
(both arms), queues `call8`/`call4`/`call12` callees, and names things in a
persistent registry. `l32r` literal-pool loads are resolved so strings show
up on the listing the way live `a0..a3` did in Gerald.

What it will not do (needs an emulator or a later pass):

- resolve `callx8` / `jx` (register-indirect)
- force a branch with realistic register state — both arms are queued as
  syntax, not as a concrete run
- recover heap/stack object shapes

## Setup

```bash
cd GooDisplay/xtroadmap
uv sync
```

Capstone **6.0.0a10** (Xtensa) is pinned. Stable 5.x cannot decode this ISA.

## Usage

```bash
# Default image: ../dumps/wifi_firmware_app0_segments
uv run xtroadmap roadmap 0x42002dec --listing --max-functions 8

# Progress-only (registry is the durable product)
uv run xtroadmap roadmap 0x40375e78 --max-functions 40

uv run xtroadmap disas 0x420119d8 -n 40
uv run xtroadmap strings --grep ADC
uv run xtroadmap xref 0x3c0ba002
```

`--image` accepts either an extracted `seg*_0xADDR.bin` directory or a raw
`app0.bin`. Registry defaults to `./xtroadmap_registry.json`.
