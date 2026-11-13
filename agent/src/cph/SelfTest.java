package cph;

import java.io.InputStream;
import java.lang.classfile.ClassFile;
import java.util.List;

/**
 * Build-time check: applies every known patch to the CrashPlan classes on the
 * class path and verifies the result. Exit code 1 on any mismatch.
 *
 * <p>Run in the agent build stage:
 * {@code java -cp /agent/out:/crashplan/lib/cpg-agent-service.jar cph.SelfTest}
 */
public final class SelfTest {

    public static void main(String[] args) throws Exception {
        int failures = 0;
        var composed = new java.util.LinkedHashMap<String, byte[]>();
        for (Patches.Patch p : Patches.all().values()) {
            String resource = p.className() + ".class";
            try (InputStream in = ClassLoader.getSystemResourceAsStream(resource)) {
                if (in == null) {
                    System.out.println("FAIL " + p.name() + ": class not found: " + p.className());
                    failures++;
                    continue;
                }
                byte[] patched = p.apply(in.readAllBytes());
                byte[] previous = composed.get(p.className());
                composed.put(p.className(), previous == null ? patched : p.apply(previous));
                List<VerifyError> errors = ClassFile.of().verify(patched);
                if (!errors.isEmpty()) {
                    System.out.println("FAIL " + p.name() + ": verifier: " + errors);
                    failures++;
                    continue;
                }
                System.out.println("OK   " + p.name() + ": " + p.className() + "." + p.methodName() + p.descriptor());
            } catch (Exception e) {
                System.out.println("FAIL " + p.name() + ": " + e.getMessage());
                failures++;
            }
        }
        for (var entry : composed.entrySet()) {
            var errors = ClassFile.of().verify(entry.getValue());
            if (!errors.isEmpty()) {
                System.out.println("FAIL composed " + entry.getKey() + ": " + errors);
                failures++;
            }
        }
        if (failures > 0) {
            System.out.println(failures + " patch(es) do not match this CrashPlan version");
            System.exit(1);
        }
        System.out.println("all " + Patches.all().size() + " patch(es) match");
    }

    private SelfTest() {
    }
}
