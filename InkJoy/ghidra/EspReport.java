//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Data;
import ghidra.program.model.listing.Function;
import ghidra.program.model.mem.MemoryBlock;
import java.util.Iterator;

public class EspReport extends GhidraScript {
    @Override public void run() throws Exception {
        Address ep = toAddr(0x408002beL);
        int funcs = currentProgram.getFunctionManager().getFunctionCount();
        Function epf = getFunctionContaining(ep);
        println("functions total: " + funcs);
        println("entry 0x408002be in function: " + (epf != null ? epf.getName()+" @ "+epf.getEntryPoint() : "NONE"));
        println("instr at entry: " + getInstructionAt(ep));
        String[] want = {"playlist.json", "mqtts://", "ecies-p256-gcm-v1", "content.pattern"};
        Iterator<Data> it = currentProgram.getListing().getDefinedData(true);
        while (it.hasNext()) {
            Data d = it.next();
            Object v = d.getValue();
            if (!(v instanceof String)) continue;
            String sv = ((String) v).trim();
            for (String w : want) {
                if (sv.equals(w) || (w.equals("playlist.json") && sv.endsWith("playlist.json"))) {
                    int refs = getReferencesTo(d.getAddress()).length;
                    println(String.format("str \"%s\" @ %s  refs=%d", sv, d.getAddress(), refs));
                }
            }
        }
        println("blocks:");
        for (MemoryBlock b : currentProgram.getMemory().getBlocks())
            println("  "+b.getName()+"  "+b.getStart()+" - "+b.getEnd());
    }
}
