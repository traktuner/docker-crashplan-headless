package cph;

import java.lang.instrument.ClassFileTransformer;
import java.lang.instrument.Instrumentation;
import java.security.ProtectionDomain;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Java agent loaded into the CrashPlan service by cph-launch.
 *
 * <p>Environment:
 * <ul>
 *   <li>CPH_PATCHES: comma-separated patch names (default: none)</li>
 * </ul>
 */
public final class Agent {

    static final Set<String> APPLIED = ConcurrentHashMap.newKeySet();
    static volatile List<Patches.Patch> enabled = List.of();

    public static void premain(String agentArgs, Instrumentation inst) {
        // An unknown patch name throws here, and the JVM refuses to start: a
        // typo must not silently run the service unpatched.
        var selected = new java.util.ArrayList<>(Patches.select(System.getenv("CPH_PATCHES")));
        for (var patch : Patches.all().values()) {
            if ((patch.name().startsWith("restore-") || patch.name().startsWith("archive-query-")) && selected.stream().noneMatch(p -> p.name().equals(patch.name()))) selected.add(patch);
        }
        enabled = List.copyOf(selected);
        if (!enabled.isEmpty()) {
            inst.addTransformer(new Transformer(enabled));
        }
        log("agent loaded; patches=" + (enabled.isEmpty() ? "none"
                : enabled.stream().map(Patches.Patch::name).toList()));
    }

    static void log(String msg) {
        System.err.println("[cph-agent] " + msg);
    }

    private static final class Transformer implements ClassFileTransformer {
        private final Map<String, List<Patches.Patch>> byClass = new HashMap<>();

        Transformer(List<Patches.Patch> patches) {
            for (Patches.Patch p : patches) {
                byClass.computeIfAbsent(p.className(), k -> new java.util.ArrayList<>()).add(p);
            }
        }

        @Override
        public byte[] transform(ClassLoader loader, String className, Class<?> redefined,
                                ProtectionDomain pd, byte[] bytes) {
            List<Patches.Patch> patches = byClass.get(className);
            if (patches == null) {
                return null;
            }
            byte[] out = bytes;
            for (Patches.Patch p : patches) {
                try {
                    out = p.apply(out);
                    APPLIED.add(p.name());
                    log("patch applied: " + p.name() + " -> " + className + "." + p.methodName());
                } catch (Throwable t) {
                    // The JVM swallows transformer exceptions. Stop loudly instead of
                    // running a service the user expects to be patched.
                    log("FATAL: patch " + p.name() + " failed on " + className + ": " + t);
                    Runtime.getRuntime().halt(70);
                }
            }
            return out;
        }
    }

    private Agent() {
    }
}
