// Pangu Ghidra headless post-script: decompile functions to C-like pseudocode.
//
// Invoked by the Pangu backend as:
//   analyzeHeadless <projDir> pangu -import <sample> -scriptPath <dir> \
//     -postScript DecompileAll.java <outRoot> -deleteProject -analysisTimeoutPerFile 600
//
// Writes one UTF-8 file per non-thunk function:
//   <outRoot>/<entryAddress>_<sanitizedFunctionName>.c
// The function count is capped to keep disk usage and runtime bounded. The
// sample is only ever opened read-only by Ghidra; nothing is executed.

import ghidra.app.decompiler.DecompInterface;
import ghidra.app.decompiler.DecompileResults;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.FunctionIterator;
import ghidra.util.task.TaskMonitor;

import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStreamWriter;
import java.io.Writer;
import java.nio.charset.StandardCharsets;

public class DecompileAll extends GhidraScript {

    /** Upper bound on decompiled functions; keeps a single run bounded. */
    private static final int MAX_FUNCTIONS = 500;

    /** Per-function decompiler timeout in seconds. */
    private static final int FUNCTION_TIMEOUT_SECONDS = 60;

    @Override
    public void run() throws Exception {
        String[] args = getScriptArgs();
        if (args == null || args.length < 1) {
            println("[pangu] missing output directory argument");
            return;
        }
        File outRoot = new File(args[0]);
        if (!outRoot.exists() && !outRoot.mkdirs()) {
            println("[pangu] cannot create output directory: " + outRoot);
            return;
        }
        if (!outRoot.isDirectory()) {
            println("[pangu] output path is not a directory: " + outRoot);
            return;
        }

        TaskMonitor mon = getMonitor();
        DecompInterface ifc = new DecompInterface();
        ifc.toggleCCode(true);
        ifc.toggleSyntaxTree(false);
        ifc.setSimplificationStyle("decompile");
        if (!ifc.openProgram(currentProgram)) {
            println("[pangu] decompiler failed to open program");
            return;
        }

        int tried = 0;
        int written = 0;
        try {
            FunctionIterator it = currentProgram.getFunctionManager().getFunctions(true);
            while (it.hasNext() && tried < MAX_FUNCTIONS) {
                if (mon.isCancelled()) {
                    break;
                }
                Function fn = it.next();
                if (fn == null || fn.isThunk() || fn.isExternal()) {
                    continue;
                }
                tried++;
                DecompileResults res;
                try {
                    res = ifc.decompileFunction(fn, FUNCTION_TIMEOUT_SECONDS, mon);
                } catch (Exception e) {
                    println("[pangu] decompile failed for " + fn.getName() + ": " + e.getMessage());
                    continue;
                }
                if (res == null || !res.decompileCompleted() || res.getDecompiledFunction() == null) {
                    continue;
                }
                String code = res.getDecompiledFunction().getC();
                if (code == null || code.isEmpty()) {
                    continue;
                }
                Address entry = fn.getEntryPoint();
                String addr = entry == null ? "unknown" : entry.toString();
                File out = new File(outRoot, addr + "_" + sanitize(fn.getName()) + ".c");
                try (Writer w = new OutputStreamWriter(
                        new FileOutputStream(out), StandardCharsets.UTF_8)) {
                    w.write("// " + fn.getName() + " @ " + addr + "\n");
                    w.write("// decompiled by Pangu / Ghidra headless\n\n");
                    w.write(code);
                    if (!code.endsWith("\n")) {
                        w.write("\n");
                    }
                }
                written++;
            }
        } finally {
            ifc.dispose();
        }
        println("[pangu] decompiled " + written + " of " + tried + " functions");
    }

    /** Keep filenames portable: strip path separators and shell-hostile chars. */
    private static String sanitize(String name) {
        if (name == null || name.isEmpty()) {
            return "func";
        }
        String s = name.replaceAll("[^A-Za-z0-9_.-]", "_");
        if (s.length() > 80) {
            s = s.substring(0, 80);
        }
        return s;
    }
}
