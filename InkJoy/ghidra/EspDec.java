//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.listing.Function;
public class EspDec extends GhidraScript {
    @Override public void run() throws Exception {
        String[] args = getScriptArgs();
        DecompInterface dec = new DecompInterface();
        DecompileOptions opts = new DecompileOptions();
        dec.setOptions(opts);
        dec.toggleCCode(true);
        dec.toggleSyntaxTree(true);
        dec.setSimplificationStyle("decompile");
        boolean ok = dec.openProgram(currentProgram);
        println("openProgram -> " + ok + "  lastError=" + dec.getLastMessage());
        for (String s : args) {
            Function f = getFunctionContaining(toAddr(Long.parseLong(s, 16)));
            if (f == null) { println("no function @ " + s); continue; }
            DecompileResults res = dec.decompileFunction(f, 120, monitor);
            println("\n----- " + f.getName() + " @ " + f.getEntryPoint()
                + "  completed=" + (res!=null && res.decompileCompleted())
                + "  msg=" + (res!=null? res.getErrorMessage():"null") + " -----");
            if (res != null && res.getDecompiledFunction() != null)
                println(res.getDecompiledFunction().getC());
        }
    }
}
