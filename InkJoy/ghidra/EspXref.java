//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.mem.Memory;
import ghidra.program.model.symbol.Reference;
import java.nio.charset.StandardCharsets;
import java.util.*;
public class EspXref extends GhidraScript {
    @Override public void run() throws Exception {
        String[] needles = getScriptArgs();
        Memory mem = currentProgram.getMemory();
        DecompInterface dec = new DecompInterface(); dec.openProgram(currentProgram);
        Set<Function> funcs = new LinkedHashSet<>();
        for (String n : needles) {
            byte[] pat = n.getBytes(StandardCharsets.US_ASCII);
            Address found = mem.findBytes(currentProgram.getMinAddress(), pat, null, true, monitor);
            if (found == null) { println("NOT FOUND: \""+n+"\""); continue; }
            // walk back to byte after previous NUL => string start
            Address start = found;
            for (int i=0;i<200;i++){ Address prev=start.subtract(1); if((mem.getByte(prev)&0xff)==0) break; start=prev; }
            println("string \""+n+"\" body@"+found+" start@"+start);
            int total=0;
            for (Reference r : getReferencesTo(start)) { Function f=getFunctionContaining(r.getFromAddress()); if(f!=null){funcs.add(f);println("  <- "+f.getName()+" @ "+f.getEntryPoint()+" (from "+r.getFromAddress()+")");total++;} }
            if (total==0) println("  (no refs to start; RISCV analyzer may not have resolved the lui/addi)");
        }
        for (Function f : funcs) {
            DecompileResults res = dec.decompileFunction(f, 60, monitor);
            println("\n----- "+f.getName()+" @ "+f.getEntryPoint()+" -----");
            println(res!=null&&res.decompileCompleted()?res.getDecompiledFunction().getC():"<fail>");
        }
    }
}
