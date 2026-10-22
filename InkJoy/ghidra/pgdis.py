#!/usr/bin/env python3
# PyGhidra disassemble-by-address. Usage: pgv/bin/python pgdis.py <hexaddr> [...]
import os, sys, pyghidra
pyghidra.start()
from ghidra.base.project import GhidraProject
PROJ_DIR  = os.environ.get("PROJ_DIR",  "/Volumes/CaseSensitive/PhotoFrames/InkJoy/ghidra/proj2")
PROJ_NAME = os.environ.get("PROJ_NAME", "ijepd2")
PROG      = os.environ.get("PROG",      "firmware-3844BE396C90.bin")
proj = GhidraProject.openProject(PROJ_DIR, PROJ_NAME, False)
prog = proj.openProgram("/", PROG, False)
fm = prog.getFunctionManager(); listing = prog.getListing()
sp = prog.getAddressFactory().getDefaultAddressSpace()
for a in sys.argv[1:]:
    f = fm.getFunctionContaining(sp.getAddress(int(a,16)))
    if f is None: print("no func @", a); continue
    print("\n===== %s @ %s .. %s =====" % (f.getName(), f.getEntryPoint(), f.getBody().getMaxAddress()))
    it = listing.getInstructions(f.getBody(), True)
    while it.hasNext():
        i = it.next()
        print("%s  %s" % (i.getAddress(), i))
