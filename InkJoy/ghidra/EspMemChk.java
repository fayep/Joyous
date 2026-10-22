//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.Address;
import ghidra.program.model.mem.*;
public class EspMemChk extends GhidraScript {
    @Override public void run() throws Exception {
        Address a = toAddr(0x408002beL);
        MemoryBlock b = currentProgram.getMemory().getBlock(a);
        println("block=" + b.getName() + " initialized=" + b.isInitialized()
            + " start=" + b.getStart() + " size=0x" + Long.toHexString(b.getSize()));
        try {
            byte[] buf = new byte[8];
            int n = currentProgram.getMemory().getBytes(a, buf);
            StringBuilder sb = new StringBuilder();
            for (byte x : buf) sb.append(String.format("%02x ", x & 0xff));
            println("bytes@entry (n=" + n + "): " + sb);
        } catch (Exception e) { println("getBytes threw: " + e); }
        println("instr@entry: " + getInstructionAt(a));
        // also check a flash function addr
        Address f = toAddr(0x4200ee64L);
        MemoryBlock fb = currentProgram.getMemory().getBlock(f);
        println("flash block initialized=" + (fb!=null && fb.isInitialized()));
        try { byte[] b2=new byte[8]; currentProgram.getMemory().getBytes(f,b2);
            StringBuilder sb=new StringBuilder(); for(byte x:b2) sb.append(String.format("%02x ",x&0xff));
            println("bytes@4200ee64: "+sb);
        } catch(Exception e){ println("getBytes@flash threw: "+e); }
    }
}
