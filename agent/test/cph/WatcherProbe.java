package cph;

import java.io.File;
import java.io.IOException;
import java.lang.reflect.Proxy;
import java.nio.file.Files;
import java.nio.file.NoSuchFileException;
import java.nio.file.Path;
import java.util.Collection;
import java.util.List;
import java.util.Set;
import java.util.concurrent.atomic.AtomicInteger;

/** Exercises real vendor/JNA/inotify calls on seven private temporary directories. */
public final class WatcherProbe {
    private static final AtomicInteger LIST_CALLS = new AtomicInteger();

    public static void main(String[] args) throws Exception {
        String mode = args[0];
        boolean suppressWatches = !mode.equals("baseline");
        boolean suppressWalk = mode.equals("scan-only");
        Path root = Files.createTempDirectory("cph-native-watch-");
        for (String path : List.of("a/c", "a/d", "b/e", "b/f")) {
            Files.createDirectories(root.resolve(path));
        }
        Class<?> managerType = Class.forName("com.code42.jna.inotify.InotifyManager");
        Object manager = managerType.getConstructor().newInstance();
        int baseline = kernelWatches();
        managerType.getMethod("start").invoke(manager);
        try {
            boolean reported = (Boolean) managerType.getMethod("watch", File.class)
                    .invoke(manager, root.toFile());
            check(reported, "valid direct watch reports success");
            int managerCount = (Integer) managerType.getMethod("getWatchCount").invoke(manager);
            int delta = kernelWatches() - baseline;
            check(managerCount == (suppressWatches ? 0 : 1), "direct manager count");
            check(delta == managerCount, "direct kernel registration matches manager");
        } finally {
            managerType.getMethod("stop").invoke(manager);
        }
        check(kernelWatches() == baseline, "stop releases all direct watches");

        Class<?> rulesType = Class.forName("com.code42.io.filewatcher.IBackupPathsRules");
        Object rules = Proxy.newProxyInstance(rulesType.getClassLoader(), new Class<?>[] {rulesType},
                (proxy, method, params) -> switch (method.getName()) {
                    case "isSelected" -> true;
                    case "getSelectedPaths" -> Set.of();
                    default -> throw new UnsupportedOperationException(method.getName());
                });
        Class<?> workerType = Class.forName("com.code42.jna.inotify.JNAInotifyWorker");
        Object worker = workerType.getConstructor(Collection.class, boolean.class, rulesType, managerType)
                .newInstance(List.of(new CountedFile(root)), true, rules, manager);
        managerType.getMethod("start").invoke(manager);
        int walks, watches;
        try {
            ((Runnable) worker).run();
            walks = LIST_CALLS.get();
            watches = kernelWatches() - baseline;
            check(walks == (suppressWalk ? 0 : 7), "registration worker listFiles count");
            check(watches == (suppressWatches ? 0 : 7), "worker kernel watch count");
            check((Integer) managerType.getMethod("getWatchCount").invoke(manager) == watches,
                    "worker manager/kernel counts match");
        } finally {
            managerType.getMethod("stop").invoke(manager);
        }
        check(kernelWatches() == baseline, "stop releases worker watches");
        System.out.println("{\"test\":\"WatcherProbe\",\"mode\":\"" + mode
                + "\",\"fixtureDirectories\":7,\"registrationListCalls\":" + walks
                + ",\"kernelWatches\":" + watches + ",\"stopReleased\":true}");
    }

    private static int kernelWatches() throws IOException {
        List<Path> paths;
        try (var entries = Files.list(Path.of("/proc/self/fdinfo"))) { paths = entries.toList(); }
        int count = 0;
        for (Path path : paths) {
            try {
                for (String line : Files.readAllLines(path)) if (line.startsWith("inotify ")) count++;
            } catch (NoSuchFileException closedDescriptor) {
                // Listing our own fdinfo opens a directory FD that is closed before this pass.
            }
        }
        return count;
    }

    private static void check(boolean condition, String message) {
        if (!condition) throw new AssertionError(message);
    }

    private static final class CountedFile extends File {
        private static final long serialVersionUID = 1L;
        CountedFile(Path path) { super(path.toString()); }
        @Override public File[] listFiles() {
            LIST_CALLS.incrementAndGet();
            File[] children = super.listFiles();
            if (children == null) throw new IllegalStateException("private fixture unreadable");
            for (int i = 0; i < children.length; i++) children[i] = new CountedFile(children[i].toPath());
            return children;
        }
    }
}
