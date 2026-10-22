import os, re, pyghidra
pyghidra.start()
from ghidra.base.project import GhidraProject
from ghidra.app.decompiler import DecompInterface
from ghidra.util.task import TaskMonitor
proj=GhidraProject.openProject("/Volumes/CaseSensitive/PhotoFrames/InkJoy/ghidra/proj_ota","ijepd_ota",False)
prog=proj.openProgram("/","ota_D0CF13EF4080.bin",False)
fm=prog.getFunctionManager(); listing=prog.getListing(); dec=DecompInterface(); dec.openProgram(prog)
EFUSE_BASE=0x600B4800
def touches(v):
    v&=0xFFFFFFFF
    if 0x600B4000 <= v < 0x600B4B00: return True
    if (v<<12) in (0x600B4000,0x600B5000): return True
    if (v&0xFFFFF000) in (0x600B4000,0x600B5000): return True
    return False
cands=[]
f=fm.getFunctions(True)
for fn in f:
    hit=False; it=listing.getInstructions(fn.getBody(),True)
    while it.hasNext():
        insn=it.next()
        for i in range(insn.getNumOperands()):
            s=insn.getScalar(i)
            if s and touches(s.getUnsignedValue()): hit=True;break
        if hit:break
        for r in insn.getReferencesFrom():
            ta=r.getToAddress()
            if ta and ta.isMemoryAddress() and 0x600B4000<=ta.getOffset()<0x600B4B00: hit=True;break
        if hit:break
    if hit: cands.append(fn)
# burn-staging registers as decompiler symbols
pgm_addrs=[0x600B4800+o for o in range(0,0x2C,4)]  # PGM_DATA0..7 + CHECK0..2
pgm_syms=[("_dat_ram_%08x"%a) for a in pgm_addrs]   # lowercased
CONF="_dat_ram_600b49cc"; CMD="_dat_ram_600b49d8"
burn=[]
for fn in cands:
    res=dec.decompileFunction(fn,120,TaskMonitor.DUMMY)
    c=res.getDecompiledFunction().getC() if (res and res.decompileCompleted()) else ""
    cl=c.lower()
    pgm_writes=[s for s in pgm_syms if re.search(re.escape(s)+r"\s*=[^=]", cl)]
    conf_write=re.search(re.escape(CONF)+r"\s*=[^=]", cl)
    cmd_write =re.search(re.escape(CMD)+r"\s*=[^=]", cl)
    prog_op = "0x5a5a" in cl
    read_op = "0x5aa5" in cl
    conf_touch = CONF in cl; cmd_touch = CMD in cl
    flags=[]
    if prog_op: flags.append("PROG_OPCODE_0x5A5A")
    if pgm_writes: flags.append("PGM_DATA_WRITE("+",".join(x[-4:] for x in pgm_writes)+")")
    if conf_write: flags.append("CONF_WRITE")
    if cmd_write: flags.append("CMD_WRITE")
    note=[]
    if read_op: note.append("read_opcode_0x5AA5")
    if conf_touch: note.append("CONF")
    if cmd_touch: note.append("CMD")
    if flags or note:
        print("%-10s %-24s BURN=%s  other=%s"%(fn.getEntryPoint(),fn.getName(),flags or "none",note or "-"))
    if flags: burn.append((fn,flags,c))
print("\n==== VERDICT (corrected, symbol-aware) ====")
if not burn:
    print("NO burn-staging detected: no 0x5A5A program opcode, no writes to EFUSE_PGM_DATA/CHECK,")
    print("no write to EFUSE_CONF/CMD carrying a program op across any EFUSE-touching function.")
else:
    print("BURN-STAGING FOUND -> dumping:")
    for fn,flags,c in burn:
        print("\n----- %s @ %s  %s -----\n%s"%(fn.getName(),fn.getEntryPoint(),flags,c))
