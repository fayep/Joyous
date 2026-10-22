// Pre-analysis GhidraScript: rebuild ESP32 (esptool v2) app-image memory map.
// Run as -preScript after a flat BinaryLoader import at base 0. Parses the image
// header from the imported FileBytes, drops the flat block, and lays down one
// correctly addressed/permissioned block per segment. Reusable for any ij_epd .bin.
//
// ESP image layout: 8-byte header (0xE9, seg_count, ...) + 16-byte extended header
// => first segment header at 0x18; each = u32 load_addr, u32 size, then data.
//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.Address;
import ghidra.program.model.address.AddressSpace;
import ghidra.program.database.mem.FileBytes;
import ghidra.program.model.mem.Memory;
import ghidra.program.model.mem.MemoryBlock;
import java.util.List;

public class EspMapSegments extends GhidraScript {
    @Override
    public void run() throws Exception {
        Memory mem = currentProgram.getMemory();
        List<FileBytes> fbs = mem.getAllFileBytes();
        if (fbs.isEmpty()) throw new Exception("no FileBytes; import with BinaryLoader first");
        FileBytes fb = fbs.get(0);

        int magic = fb.getOriginalByte(0) & 0xff;
        if (magic != 0xE9) throw new Exception(String.format("not an ESP image (magic 0x%02X)", magic));
        int segCount = fb.getOriginalByte(1) & 0xff;

        AddressSpace space = currentProgram.getAddressFactory().getDefaultAddressSpace();

        for (MemoryBlock b : mem.getBlocks()) mem.removeBlock(b, monitor);

        long off = 0x18;
        for (int s = 0; s < segCount; s++) {
            long load = u32(fb, off);
            long size = u32(fb, off + 4);
            long dataOff = off + 8;

            boolean inFlash = (load >= 0x42000000L && load < 0x43000000L);
            boolean inRtc   = (load >= 0x50000000L);
            boolean r = true;
            boolean w = !inFlash;                 // flash-mapped = read-only
            boolean x = true;
            String kind = inFlash ? "flash" : (inRtc ? "rtc" : "sram");
            String name = String.format("seg%d_%s_%08X", s, kind, load);

            Address addr = space.getAddress(load);
            MemoryBlock blk = mem.createInitializedBlock(name, addr, fb, dataOff, size, false);
            blk.setRead(r); blk.setWrite(w); blk.setExecute(x);
            println(String.format("  %-22s @ 0x%08X len=0x%06X file=0x%06X %s%s%s",
                    name, load, size, dataOff, r?"r":"-", w?"w":"-", x?"x":"-"));
            off = dataOff + size;
        }
        println(String.format("ESP map: %d segments laid down.", segCount));
    }

    private long u32(FileBytes fb, long o) throws Exception {
        long v = 0;
        for (int i = 0; i < 4; i++) v |= ((long)(fb.getOriginalByte(o + i) & 0xff)) << (8 * i);
        return v;
    }
}
