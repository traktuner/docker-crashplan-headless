package cph;

import com.code42.backup.BackupCiphers;
import com.code42.backup.manifest.FileHistory;
import com.code42.backup.manifest.SecureBackupFile;
import com.code42.backup.manifest.SecureFileVersion;
import com.code42.backup.manifest.Version;
import com.code42.backup.manifest.file.FileManifest;
import com.code42.backup.manifest.file.FileManifestEntry;
import com.code42.backup.manifest.file.FileManifestState;
import com.code42.crypto.MD5Value;
import com.code42.io.IOIterator;
import com.code42.io.path.FileId;
import java.io.File;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;

/**
 * Runs ReduceResume against real file manifests written by the engine classes
 * and compares every pass with the native iterator.
 */
public final class ReduceResumeTest {
    private static final long GUID = 42L;

    record Seen(int index, long position, FileId fileId) { }

    public static void main(String[] args) throws Exception {
        if (args.length > 0 && args[0].equals("--agent")) {
            // Loading the native job through the release agent applies and verifies the patch.
            Class.forName("com.code42.backup.manifest.source.SourceMaintenanceJob");
            if (!Agent.APPLIED.contains("reduce-resume")) throw new AssertionError("reduce-resume not applied by the agent");
        }
        Path dir = Files.createTempDirectory("reduce-resume");
        resumesAfterCheckedEntry(dir.resolve("a"));
        fallsBackWhenEntryMoved(dir.resolve("b"));
        fallsBackWhenManifestReplaced(dir.resolve("c"));
        keepsNativeOrderForEarlierStart(dir.resolve("d"));
        System.out.println("ReduceResumeTest PASS");
    }

    /** Several reduction points in a row, with appends and removals between them. */
    static void resumesAfterCheckedEntry(Path dir) throws Exception {
        ReduceResume.forget();
        FileManifest fmf = manifest(dir);
        for (int i = 0; i < 3000; i++) add(fmf, i, 1 + i % 37);
        int start = 0;
        int seeksBefore = ReduceResume.seeks;
        int[] sizes = {1, 4538 % 700, 209, 1, 1500, 3};
        for (int pass = 0; pass < sizes.length; pass++) {
            List<Seen> expected = nativeOrder(fmf);
            List<Seen> got = pass(fmf, start, sizes[pass]);
            compare(expected, got, start, "pass " + pass);
            start += sizes[pass];
            // Changes the native job and sync make between points.
            add(fmf, 10_000 + pass, 5);
            remove(fmf, got.getLast().fileId());
            if (pass == 2) {
                fmf.close();
                fmf = manifest(dir);
            }
        }
        if (ReduceResume.seeks - seeksBefore != sizes.length - 1) {
            throw new AssertionError("expected a resume for every pass after the first, got " + (ReduceResume.seeks - seeksBefore));
        }
        // The rest of the manifest, as at the end of a cycle.
        List<Seen> expected = nativeOrder(fmf);
        compare(expected, pass(fmf, start, Integer.MAX_VALUE), start, "rest");
        fmf.close();
    }

    /** The saved entry was rewritten with another length, so it moved to the end. */
    static void fallsBackWhenEntryMoved(Path dir) throws Exception {
        ReduceResume.forget();
        FileManifest fmf = manifest(dir);
        for (int i = 0; i < 500; i++) add(fmf, i, 4);
        List<Seen> first = pass(fmf, 0, 100);
        add(fmf, 99, 30); // same file ID as the last entry read, longer path
        int seeks = ReduceResume.seeks;
        compare(nativeOrder(fmf), pass(fmf, 100, 50), 100, "moved");
        if (ReduceResume.seeks != seeks) throw new AssertionError("resumed behind a moved entry");
        if (!first.getLast().fileId().equals(fileId(99))) throw new AssertionError("fixture");
        fmf.close();
    }

    /** Another manifest at the same path (as after compaction or repair). */
    static void fallsBackWhenManifestReplaced(Path dir) throws Exception {
        ReduceResume.forget();
        FileManifest fmf = manifest(dir);
        for (int i = 0; i < 400; i++) add(fmf, i, 9);
        pass(fmf, 0, 300);
        fmf.close();
        deleteTree(dir);
        fmf = manifest(dir);
        for (int i = 0; i < 400; i++) if (i % 3 != 0) add(fmf, i, 9);
        int seeks = ReduceResume.seeks;
        compare(nativeOrder(fmf), pass(fmf, 300, 20), 300, "replaced");
        if (ReduceResume.seeks != seeks) throw new AssertionError("resumed in a replaced manifest");
        fmf.close();
    }

    /** A start at or before the saved entry reads natively from the beginning. */
    static void keepsNativeOrderForEarlierStart(Path dir) throws Exception {
        ReduceResume.forget();
        FileManifest fmf = manifest(dir);
        for (int i = 0; i < 300; i++) add(fmf, i, 2 + i % 5);
        pass(fmf, 0, 200);
        int seeks = ReduceResume.seeks;
        compare(nativeOrder(fmf), pass(fmf, 150, 20), 150, "earlier");
        compare(nativeOrder(fmf), pass(fmf, 0, 10), 0, "restart");
        if (ReduceResume.seeks != seeks) throw new AssertionError("resumed for an earlier start");
        fmf.close();
    }

    /** The native processFiles loop: skip to start, then read up to count entries. */
    static List<Seen> pass(FileManifest fmf, int start, int count) throws Exception {
        IOIterator<FileManifestEntry> it = ReduceResume.iterator(fmf, true, start);
        List<Seen> out = new ArrayList<>();
        int entryIndex = -1;
        int processed = 0;
        while (it.hasNext()) {
            FileManifestEntry entry = it.next();
            if (++entryIndex < start) continue;
            if (entry == null) throw new AssertionError("null entry at " + entryIndex);
            out.add(new Seen(entryIndex, entry.getPosition(), entry.getFileId()));
            if (++processed == count) break;
        }
        return out;
    }

    static List<Seen> nativeOrder(FileManifest fmf) throws Exception {
        IOIterator<FileManifestEntry> it = fmf.fileManifestEntryIterator(true);
        List<Seen> out = new ArrayList<>();
        int index = 0;
        while (it.hasNext()) {
            FileManifestEntry entry = it.next();
            out.add(new Seen(index++, entry.getPosition(), entry.getFileId()));
        }
        return out;
    }

    static void compare(List<Seen> expected, List<Seen> got, int start, String what) {
        if (got.isEmpty() && start < expected.size()) throw new AssertionError(what + ": nothing read");
        for (Seen seen : got) {
            Seen want = expected.get(seen.index());
            if (!want.equals(seen)) throw new AssertionError(what + ": " + seen + " != native " + want);
        }
        if (!got.isEmpty() && got.getFirst().index() != start) throw new AssertionError(what + ": first index " + got.getFirst());
    }

    static FileManifest manifest(Path dir) throws Exception {
        Files.createDirectories(dir);
        var props = new com.code42.backup.manifest.source.SourceArchiveProperties(dir.resolve("cp.properties").toFile());
        return new FileManifest(dir.resolve("cpfmf").toString(), dir.resolve("cphdf").toString(), GUID,
                new FileManifestState(props)).open();
    }

    static FileId fileId(int n) {
        return FileId.getFileId(GUID, "/data/file-" + n);
    }

    static void add(FileManifest fmf, int n, int pathBytes) throws Exception {
        FileId id = fileId(n);
        var sbf = new SecureBackupFile(id, FileId.getFileIdRoot(GUID), (byte) 0,
                BackupCiphers.CipherCode.NO_ENCRYPTION, new byte[pathBytes]);
        long now = 1_700_000_000_000L + n;
        var version = new Version(now, now, 10L + n, new MD5Value(new byte[16]), (byte) 0);
        var history = new FileHistory(id);
        history.addVersionData(version, (short) 0, 0L, new MD5Value(new byte[16]), new long[]{1L});
        fmf.saveSecureBackupFileVersionAndFileHistory(new SecureFileVersion(sbf, version), history);
    }

    static void remove(FileManifest fmf, FileId id) throws Exception {
        fmf.removeSecureBackupFile(id);
    }

    static void deleteTree(Path dir) throws Exception {
        try (var walk = Files.walk(dir)) {
            walk.sorted(java.util.Comparator.reverseOrder()).map(Path::toFile).forEach(File::delete);
        }
    }

    private ReduceResumeTest() { }
}
