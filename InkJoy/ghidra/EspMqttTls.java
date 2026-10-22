//@category ESP32
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.Reference;
import java.util.*;

public class EspMqttTls extends GhidraScript {
    DecompInterface dec;
    @Override public void run() throws Exception {
        dec = new DecompInterface();
        dec.openProgram(currentProgram);
        // 1) functions that reference the key strings
        String[] markers = {"mqtts://","mqtt://","ecies-p256-gcm-v1",
            "skip_cert_common_name_check","crt_bundle","use_global_ca_store",
            "Failed to verify certificate","mbedtls_ssl_conf_own_cert",
            "clientcert_buf","serverInfo","mqtt_config","Missing server certificate"};
        Set<Function> hits = new LinkedHashSet<>();
        Map<String,String> whereStr = new LinkedHashMap<>();
        Iterator<Data> it = currentProgram.getListing().getDefinedData(true);
        while (it.hasNext()) {
            Data d = it.next();
            Object v = d.getValue();
            if (!(v instanceof String)) continue;
            String sv = ((String) v).trim();
            for (String m : markers) {
                if (sv.contains(m)) {
                    for (Reference r : getReferencesTo(d.getAddress())) {
                        Function f = getFunctionContaining(r.getFromAddress());
                        if (f != null) { hits.add(f);
                            whereStr.merge(f.getName(), m, (a,b)->a.contains(b)?a:a+","+b); }
                    }
                }
            }
        }
        println("=== functions referencing MQTT/TLS markers ===");
        for (Function f : hits)
            println(String.format("  %-28s @ %s  [%s]", f.getName(), f.getEntryPoint(), whereStr.get(f.getName())));
        println("\n=== decompilations ===");
        for (Function f : hits) decompile(f);
    }
    void decompile(Function f) {
        DecompileResults res = dec.decompileFunction(f, 60, monitor);
        println("\n----- " + f.getName() + " @ " + f.getEntryPoint() + " -----");
        if (res != null && res.decompileCompleted())
            println(res.getDecompiledFunction().getC());
        else println("  <decompile failed>");
    }
}
