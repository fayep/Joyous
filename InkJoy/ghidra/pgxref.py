#!/usr/bin/env python3
# PyGhidra string-xref + decompile via byte-search (robust; mirrors old EspXref).
import os, sys, pyghidra
pyghidra.start()
from ghidra.base.project import GhidraProject
from ghidra.app.decompiler import DecompInterface
from ghidra.util.task import TaskMonitor
PROJ_DIR  = os.environ.get("PROJ_DIR",  "/Volumes/CaseSensitive/PhotoFrames/InkJoy/ghidra/proj2")
PROJ_NAME = os.environ.get("PROJ_NAME", "ijepd2")
PROG      = os.environ.get("PROG",      "firmware-3844BE396C90.bin")
needles = sys.argv[1:]
proj = GhidraProject.openProject(PROJ_DIR, PROJ_NAME, False)
prog = proj.openProgram("/", PROG, False)
fm  = prog.getFunctionManager()
rm  = prog.getReferenceManager()
mem = prog.getMemory()
dec = DecompInterface(); dec.openProgram(prog)
seen = set(); hits = []
for n in needles:
    pat = bytes(n, "ascii")
    found = mem.findBytes(prog.getMinAddress(), pat, None, True, TaskMonitor.DUMMY)
    if found is None:
        print("NOT FOUND:", n); continue
    start = found
    for _ in range(300):
        prev = start.subtract(1)
        if (mem.getByte(prev) & 0xff) == 0: break
        start = prev
    refs = rm.getReferencesTo(start)
    cnt = 0
    for r in refs:
        f = fm.getFunctionContaining(r.getFromAddress())
        if f and f.getEntryPoint() not in seen:
            seen.add(f.getEntryPoint()); hits.append((n, f)); cnt += 1
    print("needle %r body@%s start@%s refs->funcs=%d" % (n, found, start, cnt))
for n, f in hits:
    res = dec.decompileFunction(f, 120, TaskMonitor.DUMMY)
    print("\n===== [%s] %s @ %s =====" % (n, f.getName(), f.getEntryPoint()))
    print(res.getDecompiledFunction().getC() if res and res.decompileCompleted() else "<fail>")
