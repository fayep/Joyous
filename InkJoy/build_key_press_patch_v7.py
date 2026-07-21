"""SD-probe patch v7: same as v6, but targeting usb_serial_device_tx_one_char
(0x40000ac4) instead of the classic uart_tx_one_char (0x40000054).

v6 flashed and ran with zero crashes, but still produced no visible
output — this device's console is USB Serial/JTAG, not the traditional
UART0 peripheral (confirmed in project memory: "Frame has USB Serial/JTAG
(ESP32-C5 built-in, no external chip)"), so calling uart_tx_one_char was
silently writing to the wrong peripheral. esp32c5.rom.ld lists a separate
"usb_device_uart" function group for exactly this interface:
usb_serial_device_tx_one_char = 0x40000ac4.

Verified the byte-transmit loop against this new address in isolation
under Unicorn (test_uart_stub_v2.py) before rebuilding this.

Ground truth:
  opendir()   0x4200b4c8
  readdir()   0x4200b530
  delay-ish function  0x40815a80 (a0=3000, replicated — this hook skips it)
  usb_serial_device_tx_one_char (ROM)  0x40000ac4
  dirent->d_name offset: +3
Hook site: 0x42022806, 8 bytes. Return: 0x42022810.
"""
from fw_patch_tool import Firmware, Asm

UART_TX_ONE_CHAR = 0x40000ac4
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
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file_on_keypress_v7")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_keypress_sd_probe_v7",
              note="v7: logging via usb_serial_device_tx_one_char "
                   "(0x40000ac4) instead of uart_tx_one_char — this "
                   "board's console is USB Serial/JTAG, not classic UART0")

manifest = fw.save("firmware-keypress-sd-patch-v7.bin")
print("saved firmware-keypress-sd-patch-v7.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
