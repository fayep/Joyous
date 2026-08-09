"""The actual fix: NOP out the branch that skips SD testing/mounting.

Found by comparing H2 and H3: both firmwares contain byte-for-byte
identical logic gating "STA power debug: skip sd_test" —

    lw a0, -0x798(gp)     ; a0 = current run_mode (global)
    c.addi a0, -2
    seqz a0, a0            ; a0 = (run_mode == 2)
    c.jr ra
    ...
    jal <that function>
    bnez a0, <skip_sd_test>   ; if run_mode == 2, skip the real SD test/mount

Every single boot log collected this session prints "run_mode: 2" — this
isn't a coincidence, it's the literal condition being checked. The SD
test/mount is deliberately skipped during normal STA-mode operation,
regardless of whether a card is present. H3 has the exact same gate, so
this alone doesn't explain why H3 can read SD content and H2 apparently
can't — but it does mean H2 never even attempts the real mount path in
normal operation, which is a prerequisite for anything else to work.

Fix: patch only the single 4-byte `bnez a0, ...` branch at 0x420230a0 to
a NOP. This is deliberately NOT touching the shared "is run_mode == 2"
utility function itself (0x42022afe), since that's almost certainly
called from other places too or unrelated purposes — only this one
specific decision point, so nothing else in the firmware is affected.

After this: whatever code normally runs when run_mode != 2 (the real
sd_test/mount path) should run unconditionally instead.
"""
from fw_patch_tool import Firmware, enc_addi

NOP = enc_addi(0, 0, 0).to_bytes(4, "little")  # addi zero, zero, 0

BRANCH_VA = 0x420230a0

fw = Firmware.load("firmware.bin")

before = fw.read_irom(BRANCH_VA, 4)
fw.write_irom(BRANCH_VA, NOP, name="nop_skip_sd_test_gate")
after = fw.read_irom(BRANCH_VA, 4)

print(f"before: {before.hex()}  (bnez a0, skip_sd_test)")
print(f"after:  {after.hex()}  (nop)")

manifest = fw.save("firmware-nop-skip-gate.bin")
print("saved firmware-nop-skip-gate.bin, manifest at", manifest)
