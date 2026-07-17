"""Isolation patch #4: same hook byte range as before (0x42022806, 8 bytes)
but with the RETURN_VA bug fixed.

Root cause found: RETURN_VA=0x4202280e (used by every prior patch in this
series) was not actually an instruction boundary. Verified by loading
firmware.bin through esptool's own parser (LoadFirmwareImage) and reading
segment.data directly — no manual file-offset arithmetic, which is what
produced the original wrong address back when this hook was first
identified. The real layout at the hook site:

  0x420227fe  auipc ra, 0x11b        <- untouched: the real KEY_STATE_PRESSED
  0x42022802  jalr  ra, ra, 0x258       printf call, executes normally before
                                        our hook even starts
  0x42022806  c.lui a0, 1             <- HOOK_VA: our 8-byte overwrite starts
  0x42022808  addi  a0, a0, -0x448      here (a0 = 3000, likely a delay arg)
  0x4202280c  auipc ra, 0xfe7f3       <- this 4-byte instruction gets split:
  0x42022810  jalr  ra, ra, 0x274        our old RETURN_VA (0x4202280e) landed
                                          on its SECOND HALF, i.e. mid-instruction

Every prior patch in this series (v1, v2, isolation #1/#2) jumped back to
that split point and executed garbage bytes as an "instruction" — this is
what actually caused every hang, not esp_log_write, opendir, or task
context/stack safety. v3 (bare jump, no calls) apparently survived by
luck: whatever garbage decoded to at that specific register state must
have been comparatively harmless.

Fix: RETURN_VA moves to 0x42022810 (a real boundary, right after the
auipc+jalr this hook otherwise skips), and the cave replicates that
skipped call (0x40815a80, called with a0=3000 — likely a delay function)
instead of dropping it, to preserve original behavior/timing.

This patch is intentionally the same bare/no-log-calls shape as isolation
patch #3, to test the boundary fix in isolation before reintroducing any
log calls.
"""
from fw_patch_tool import Firmware, Asm

DELAY_FN_VA = 0x40815a80  # a0=3000 at the call site; likely vTaskDelay-ish
HOOK_VA = 0x42022806
RETURN_VA = 0x42022810  # corrected: real instruction boundary

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()

asm = Asm(base_va=cave_va)
asm.li("a0", 3000)          # replicate: c.lui a0,1 / addi a0,a0,-0x448
asm.call(DELAY_FN_VA)       # replicate the call this hook otherwise skips
asm.call(RETURN_VA, link="zero")
code = asm.assemble()

cave_va_actual = fw.add_cave(code, name="isolation_probe4_fixed_return")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_isolation_probe4",
              note="corrected RETURN_VA (0x42022810, was wrongly 0x4202280e); "
                   "cave replicates a0=3000 + the delay call this hook skips, "
                   "no other calls yet — retesting the trampoline fix in isolation")

manifest = fw.save("firmware-isolation-patch4.bin")
print("saved firmware-isolation-patch4.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(code), "bytes")
