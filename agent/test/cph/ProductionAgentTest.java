package cph;

/** Runs with the release javaagent and no vendor engine or account. */
public final class ProductionAgentTest {
    public static void main(String[] args) {
        for (String retired : new String[]{"Diagnostics", "Control", "CrashPlanEngine", "SelfTest"}) {
            if (ClassLoader.getSystemResource("cph/" + retired + ".class") != null) {
                throw new AssertionError("Experiment or build-only class shipped: " + retired);
            }
        }
        for (Thread thread : Thread.getAllStackTraces().keySet()) {
            if (thread.getName().equals("cph-diagnostics") || thread.getName().equals("cph-control")) {
                throw new AssertionError("Retired runtime thread started: " + thread.getName());
            }
        }
        System.out.println("ProductionAgentTest PASS: patch-only agent; no experiment runtime");
    }
    private ProductionAgentTest() { }
}
