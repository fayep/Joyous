//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.symbol.Reference;
import java.util.*;
public class EspCallers extends GhidraScript {
    @Override public void run() throws Exception {
        long[] targets = {0x420101a0L, 0x42019d68L}; // url builder, mqtt_config handler
        DecompInterface dec = new DecompInterface();
        dec.openProgram(currentProgram);
        Set<Function> toShow = new LinkedHashSet<>();
        for (long t : targets) {
            Address a = toAddr(t);
            Function tf = getFunctionAt(a);
            println("=== callers of " + (tf!=null?tf.getName():("0x"+Long.toHexString(t))) + " ===");
            for (Reference r : getReferencesTo(a)) {
                Function cf = getFunctionContaining(r.getFromAddress());
                if (cf != null) { println("  <- " + cf.getName() + " @ " + cf.getEntryPoint() + " (call @ "+r.getFromAddress()+")"); toShow.add(cf); }
            }
            if (tf != null) toShow.add(tf);
        }
        for (Function f : toShow) {
            DecompileResults res = dec.decompileFunction(f, 60, monitor);
            println("\n----- " + f.getName() + " @ " + f.getEntryPoint() + " -----");
            println(res!=null && res.decompileCompleted() ? res.getDecompiledFunction().getC() : "  <decompile failed>");
        }
    }
}
