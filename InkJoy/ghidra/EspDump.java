//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.listing.Function;
public class EspDump extends GhidraScript {
    @Override public void run() throws Exception {
        String[] args = getScriptArgs();
        DecompInterface dec = new DecompInterface();
        dec.openProgram(currentProgram);
        for (String s : args) {
            long addr = Long.parseLong(s, 16);
            Function f = getFunctionContaining(toAddr(addr));
            if (f == null) { println("no function @ "+s); continue; }
            DecompileResults res = dec.decompileFunction(f, 60, monitor);
            println("\n----- " + f.getName() + " @ " + f.getEntryPoint() + " -----");
            println(res!=null && res.decompileCompleted() ? res.getDecompiledFunction().getC() : "  <decompile failed>");
        }
    }
}
