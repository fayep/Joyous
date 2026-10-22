import os, pyghidra
pyghidra.start()
from ghidra.base.project import GhidraProject
from ghidra.app.decompiler import DecompInterface
from ghidra.util.task import TaskMonitor
proj = GhidraProject.openProject("/Volumes/CaseSensitive/PhotoFrames/InkJoy/ghidra/proj_ota","ijepd_ota",False)
prog = proj.openProgram("/","ota_D0CF13EF4080.bin",False)
fm=prog.getFunctionManager(); dec=DecompInterface(); dec.openProgram(prog)
# validity: decompile a handful, show C length + whether EFUSE addrs resolve in text
targets=[0x40800442,0x4080fcac,0x420015fc,0x42003cec,0x420933c8]
from ghidra.program.model.address import AddressSet
af=prog.getAddressFactory().getDefaultAddressSpace()
tot_resolved=0
for a in targets:
    f=fm.getFunctionContaining(af.getAddress(a))
    if not f: print("no func @%x"%a); continue
    res=dec.decompileFunction(f,120,TaskMonitor.DUMMY)
    c=res.getDecompiledFunction().getC() if (res and res.decompileCompleted()) else ""
    has6=("600b4" in c.lower()); tot_resolved+=1 if has6 else 0
    print("== %s @ %x  Clen=%d  has_600b4=%s =="%(f.getName(),a,len(c),has6))
    print("\n".join(l for l in c.splitlines() if any(k in l.lower() for k in ("600b4","0x5a","efuse","0xb4800","volatile")))[:800])
    print("...")
# global: decompile ALL funcs? too slow. Instead confirm 0x5a5a absence via a wider net:
# count funcs whose C contains 0x5a5a or 0x5aa5 among EFUSE-touching set already done.
print("\nvalidity: %d/%d sampled funcs resolved the 0x600b4xxx EFUSE address in decompiled C"%(tot_resolved,len(targets)))
