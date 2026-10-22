//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.Reference;
import java.util.*;
public class EspFindStr extends GhidraScript {
    @Override public void run() throws Exception {
        String[] needles = getScriptArgs();
        DecompInterface dec = new DecompInterface(); dec.openProgram(currentProgram);
        Set<Function> funcs = new LinkedHashSet<>();
        Iterator<Data> it = currentProgram.getListing().getDefinedData(true);
        while (it.hasNext()) {
            Data d = it.next(); Object v = d.getValue();
            if (!(v instanceof String)) continue;
            String sv = (String) v;
            for (String n : needles) if (sv.contains(n)) {
                for (Reference r : getReferencesTo(d.getAddress())) {
                    Function f = getFunctionContaining(r.getFromAddress());
                    if (f != null) { funcs.add(f); println("hit \""+n+"\" <- "+f.getName()+" @ "+f.getEntryPoint()); }
                }
            }
        }
        for (Function f : funcs) {
            DecompileResults res = dec.decompileFunction(f, 60, monitor);
            println("\n----- "+f.getName()+" @ "+f.getEntryPoint()+" -----");
            println(res!=null&&res.decompileCompleted()?res.getDecompiledFunction().getC():"<fail>");
        }
    }
}
