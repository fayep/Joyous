"""SD-probe patch v6: same corrected hook/return/opendir/readdir addressing
as v5, but logging goes straight to the ROM's uart_tx_one_char (0x40000054,
per esp-idf's esp32c5.rom.ld) one byte at a time, instead of any app-level
logging function.

v5 (corrected addresses, printf-style logger) ran with zero crashes but
zero output. Emulating the actual call chain under Unicorn (see
test_v5_cave.py) traced it into a real, deep dependency chain — a
default-stream pointer and mutex/lock state that only exist once the RTOS
has actually booted — before hitting library internals impractical to
fake. uart_tx_one_char is a ROM function with none of that: it's used by
the ROM bootloader itself before FreeRTOS even starts, so it can't depend
on any app-level runtime state. Verified the byte-transmit loop itself in
isolation under Unicorn (test_uart_stub.py) before building this.

Ground truth:
  opendir()   0x4200b4c8
  readdir()   0x4200b530
  delay-ish function  0x40815a80 (a0=3000, replicated — this hook skips it)
  uart_tx_one_char (ROM)  0x40000054
  dirent->d_name offset: +3
Hook site: 0x42022806, 8 bytes. Return: 0x42022810.
"""
from fw_patch_tool import Firmware, Asm

UART_TX_ONE_CHAR = 0x40000054
DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530

HOOK_VA = 0x42022806
RETURN_VA = 0x42022810

STR_SDCARD = b"/sdcard\x00"
STR_BEFORE = b"SDPATCH: probing /sdcard\r\n\x00"
STR_FAIL_OPENDIR = b"SDPATCH: opendir failed\r\n\x00"
STR_FAIL_READDIR = b"SDPATCH: readdir returned null\r\n\x00"
STR_PREFIX = b"SDPATCH: first file: \x00"
STR_NEWLINE = b"\r\n\x00"


def build_cave(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.li("a0", 3000)
    asm.call(DELAY_FN_VA)

    counter = [0]

    def put_str(str_va):
        counter[0] += 1
        loop_label = f"putc_loop_{counter[0]}"
        done_label = f"putc_done_{counter[0]}"
        asm.la("t0", str_va)
        asm.label(loop_label)
        asm.lbu("a0", 0, "t0")
        asm.beq("a0", "zero", done_label)
        asm.call(UART_TX_ONE_CHAR)
        asm.addi("t0", "t0", 1)
        asm.j(loop_label)
        asm.label(done_label)

    put_str(s["before"])

    asm.la("a0", s["sdcard"])
    asm.call(OPENDIR_VA)
    asm.mv("t1", "a0")
    asm.bne("t1", "zero", "opendir_ok")
    put_str(s["fail_opendir"])
    asm.j("done")

    asm.label("opendir_ok")
    asm.mv("a0", "t1")
    asm.call(READDIR_VA)
    asm.mv("t2", "a0")
    asm.bne("t2", "zero", "readdir_ok")
    put_str(s["fail_readdir"])
    asm.j("done")

    asm.label("readdir_ok")
    asm.addi("t3", "t2", 3)
    put_str(s["prefix"])
    # print the filename itself: t3 holds its address, reuse put_str's
    # loop body inline since put_str takes a fixed VA, not a register
    asm.mv("t0", "t3")
    asm.label("name_loop")
    asm.lbu("a0", 0, "t0")
    asm.beq("a0", "zero", "name_done")
    asm.call(UART_TX_ONE_CHAR)
    asm.addi("t0", "t0", 1)
    asm.j("name_loop")
    asm.label("name_done")
    put_str(s["newline"])

    asm.label("done")
    asm.call(RETURN_VA, link="zero")
    return asm


fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()

placeholder = {k: cave_va for k in ("sdcard", "before", "fail_opendir", "fail_readdir", "prefix", "newline")}
code_len = len(build_cave(cave_va, placeholder).assemble())

strings_va = cave_va + code_len
names_and_bytes = [
    ("sdcard", STR_SDCARD), ("before", STR_BEFORE), ("fail_opendir", STR_FAIL_OPENDIR),
    ("fail_readdir", STR_FAIL_READDIR), ("prefix", STR_PREFIX), ("newline", STR_NEWLINE),
]
string_vas = {}
offset = 0
for name, b in names_and_bytes:
    string_vas[name] = strings_va + offset
    offset += len(b)
data = b"".join(b for _, b in names_and_bytes)
data += b"\x00" * ((-len(data)) % 4)

code = build_cave(cave_va, string_vas).assemble()
assert len(code) == code_len, "instruction count changed between passes"

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file_on_keypress_v6")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_keypress_sd_probe_v6",
              note="v6: logging goes straight to ROM uart_tx_one_char "
                   "(0x40000054), bypassing the app-level logging stack "
                   "entirely (verified impractical to fake at runtime "
                   "in emulation — real mutex/heap dependency chain)")

manifest = fw.save("firmware-keypress-sd-patch-v6.bin")
print("saved firmware-keypress-sd-patch-v6.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
