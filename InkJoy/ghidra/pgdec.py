#!/usr/bin/env python3
# PyGhidra decompile-by-address. Usage: pgv/bin/python pgdec.py <hexaddr> [<hexaddr>...]
import os, sys, pyghidra
pyghidra.start()
from ghidra.base.project import GhidraProject
from ghidra.app.decompiler import DecompInterface
from ghidra.util.task import TaskMonitor
PROJ_DIR  = os.environ.get("PROJ_DIR",  "/Volumes/CaseSensitive/PhotoFrames/InkJoy/ghidra/proj2")
PROJ_NAME = os.environ.get("PROJ_NAME", "ijepd2")
PROG      = os.environ.get("PROG",      "firmware-3844BE396C90.bin")
proj = GhidraProject.openProject(PROJ_DIR, PROJ_NAME, False)
prog = proj.openProgram("/", PROG, False)
fm = prog.getFunctionManager()
dec = DecompInterface(); dec.openProgram(prog)
sp = prog.getAddressFactory().getDefaultAddressSpace()
for a in sys.argv[1:]:
    f = fm.getFunctionContaining(sp.getAddress(int(a,16)))
    if f is None: print("no func @", a); continue
    res = dec.decompileFunction(f, 120, TaskMonitor.DUMMY)
    print("\n===== %s @ %s =====" % (f.getName(), f.getEntryPoint()))
    print(res.getDecompiledFunction().getC() if res and res.decompileCompleted() else "<fail>")
