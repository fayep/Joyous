#!/usr/bin/env python3
# Hunt the analyzed ij_epd image for an eFuse *program* (burn) path.
# Read path is benign (boot reads MAC/calibration). Burn requires:
#   - write opcode 0x5A5A to EFUSE_CONF_REG (0x600B49CC)   [read uses 0x5AA5]
#   - writes to EFUSE_PGM_DATA0..7 / PGM_CHECK (0x600B4800..0x600B4828)
#   - EFUSE_PGM_CMD = bit1 of EFUSE_CMD_REG (0x600B49D8)
# Strategy: find every function that touches the EFUSE peripheral region, decompile
# only those, and report which burn-specific markers appear.
import os, pyghidra
pyghidra.start()
from ghidra.base.project import GhidraProject
from ghidra.app.decompiler import DecompInterface
from ghidra.util.task import TaskMonitor

PROJ_DIR  = os.environ.get("PROJ_DIR",  "/Volumes/CaseSensitive/PhotoFrames/InkJoy/ghidra/proj_ota")
PROJ_NAME = os.environ.get("PROJ_NAME", "ijepd_ota")
PROG      = os.environ.get("PROG",      "ota_D0CF13EF4080.bin")

EFUSE_BASE = 0x600B4800
EFUSE_END  = 0x600B4A00           # covers CONF/CMD/STATUS at +0x1cc/+0x1d8/+0x1d4
PGM_DATA_LO, PGM_DATA_HI = 0x600B4800, 0x600B4828   # PGM_DATA0..7 + CHECK0..2 (burn-only)
CONF = 0x600B49CC; CMD = 0x600B49D8

proj = GhidraProject.openProject(PROJ_DIR, PROJ_NAME, False)
prog = proj.openProgram("/", PROG, False)
fm = prog.getFunctionManager()
listing = prog.getListing()
dec = DecompInterface(); dec.openProgram(prog)

def touches_efuse(scalar):
    if scalar is None: return False
    v = scalar & 0xFFFFFFFF
    if EFUSE_BASE - 0x800 <= v < EFUSE_END + 0x100: return True   # direct address
    if (v << 12) in (0x600B4000, 0x600B5000): return True         # lui immediate (raw imm)
    if (v & 0xFFFFF000) in (0x600B4000, 0x600B5000): return True  # shifted lui value
    return False

# 1) find candidate functions by scanning instruction scalars + from-references
cands = {}
fns = fm.getFunctions(True)
for f in fns:
    ep = f.getEntryPoint()
    hit = False
    it = listing.getInstructions(f.getBody(), True)
    while it.hasNext():
        insn = it.next()
        for i in range(insn.getNumOperands()):
            s = insn.getScalar(i)
            if s is not None and touches_efuse(s.getUnsignedValue()):
                hit = True; break
        if hit: break
        for r in insn.getReferencesFrom():
            ta = r.getToAddress()
            if ta is not None and ta.isMemoryAddress():
                off = ta.getOffset()
                if EFUSE_BASE - 0x800 <= off < EFUSE_END + 0x100:
                    hit = True; break
        if hit: break
    if hit:
        cands[ep] = f

print("EFUSE-touching functions:", len(cands))
burn_hits = []
for ep, f in cands.items():
    res = dec.decompileFunction(f, 120, TaskMonitor.DUMMY)
    c = res.getDecompiledFunction().getC() if (res and res.decompileCompleted()) else ""
    cl = c.lower()
    markers = []
    if "0x5a5a" in cl: markers.append("PROG_OPCODE_0x5A5A")
    for a in range(PGM_DATA_LO, PGM_DATA_HI+1, 4):
        if ("0x%08x" % a) in cl or ("0x%x" % a) in cl: markers.append("PGM_DATA@0x%x" % a)
    if ("0x%x" % CONF) in cl: markers.append("CONF_REG")
    if ("0x%x" % CMD) in cl: markers.append("CMD_REG")
    has_read = "0x5aa5" in cl
    print("  %-10s %-28s read_opcode=%s burn_markers=%s"
          % (f.getEntryPoint(), f.getName(), has_read, markers or "none"))
    if any(m.startswith("PROG_OPCODE") or m.startswith("PGM_DATA") for m in markers):
        burn_hits.append((f, markers, c))

print("\n==== VERDICT ====")
if not burn_hits:
    print("NO eFuse PROGRAM/BURN path found (no 0x5A5A write opcode, no PGM_DATA writes).")
    print("Any EFUSE access above is READ-only (0x5AA5 / RDATA) => image cannot burn a fuse.")
else:
    print("POTENTIAL BURN PATH(S) FOUND -- inspect:")
    for f, markers, c in burn_hits:
        print("\n----- %s @ %s  markers=%s -----" % (f.getName(), f.getEntryPoint(), markers))
        print(c)
