package cph;

import com.code42.backup.manifest.BlockManifestRuntimeException;
import com.code42.backup.manifest.SourceBlock;
import com.code42.backup.manifest.source.MultiSourceBlockManifest;
import com.code42.backup.manifest.source.SourceBlockSiloManager;
import com.code42.crypto.MD5Value;
import java.lang.reflect.Method;
import java.nio.file.Files;
import java.nio.file.Path;

/**
 * Drives a silo change in a real source block manifest while new block numbers
 * are handed out but not saved yet, as the backup queue does.
 *
 * <p>Without the agent the native duplicate number and the "Block out of order"
 * failure must appear; with {@code --agent} the numbers must stay unique and
 * every record must be saved.
 */
public final class BlockRolloverTest {

    public static void main(String[] args) throws Exception {
        boolean agent = args.length > 0 && args[0].equals("--agent");
        if (agent) {
            Class.forName("com.code42.backup.manifest.source.SourceBlockSiloManager");
            if (!Agent.APPLIED.contains("block-rollover")) throw new AssertionError("block-rollover not applied by the agent");
        }
        Path dir = Files.createTempDirectory("block-rollover");
        if (agent) {
            keepsNumbersUnique(dir.resolve("a"));
            keepsNativeCounterWithoutHandedOutNumbers(dir.resolve("b"));
            keepsNumbersUniqueAfterManyHandedOut(dir.resolve("c"));
        } else {
            reproducesNativeDuplicate(dir.resolve("a"));
        }
        System.out.println("BlockRolloverTest PASS" + (agent ? " (agent)" : " (native)"));
    }

    /** Two numbers handed out, silo change at the first save, one more number after it. */
    static void keepsNumbersUnique(Path dir) throws Exception {
        MultiSourceBlockManifest bmf = manifest(dir);
        fill(bmf, 10);
        long a = bmf.getNewBlockNum();
        long b = bmf.getNewBlockNum();
        startSilo(bmf, a);
        save(bmf, a);
        long c = bmf.getNewBlockNum();
        if (c != b + 1) throw new AssertionError("number after silo change " + c + ", expected " + (b + 1));
        save(bmf, b);
        save(bmf, c);
        if (bmf.getLastBlockNum() != c) throw new AssertionError("last block " + bmf.getLastBlockNum());
        if (bmf.getSiloManager().getNumberOfPointers() != 2) throw new AssertionError("expected two silos");
        bmf.close();
        MultiSourceBlockManifest reopened = manifest(dir);
        for (long n = 1; n <= c; n++) {
            if (!reopened.containsBlock(n)) throw new AssertionError("block " + n + " missing after reopen");
        }
        if (reopened.getNewBlockNum() != c + 1) throw new AssertionError("counter after reopen");
        reopened.close();
    }

    /** A silo change without numbers handed out (as during sync) keeps the native counter. */
    static void keepsNativeCounterWithoutHandedOutNumbers(Path dir) throws Exception {
        MultiSourceBlockManifest bmf = manifest(dir);
        fill(bmf, 10);
        bmf.resetNewBlockNumber();
        long start = bmf.getLastBlockNum() + 1;
        startSilo(bmf, start);
        SourceBlock existing = block(start);
        bmf.addExistingBlockRecord(existing);
        long next = bmf.getNewBlockNum();
        if (next != start + 1) throw new AssertionError("native counter changed: " + next);
        save(bmf, next);
        bmf.close();
    }

    /** Many numbers in flight across the change, saved in order afterwards. */
    static void keepsNumbersUniqueAfterManyHandedOut(Path dir) throws Exception {
        MultiSourceBlockManifest bmf = manifest(dir);
        fill(bmf, 50);
        long[] inFlight = new long[25];
        for (int i = 0; i < inFlight.length; i++) inFlight[i] = bmf.getNewBlockNum();
        startSilo(bmf, inFlight[0]);
        for (int i = 0; i < 5; i++) save(bmf, inFlight[i]);
        long after = bmf.getNewBlockNum();
        if (after != inFlight[inFlight.length - 1] + 1) throw new AssertionError("number after silo change " + after);
        for (int i = 5; i < inFlight.length; i++) save(bmf, inFlight[i]);
        save(bmf, after);
        bmf.close();
    }

    /** The native engine hands out the same number twice and fails on the second save. */
    static void reproducesNativeDuplicate(Path dir) throws Exception {
        MultiSourceBlockManifest bmf = manifest(dir);
        fill(bmf, 10);
        long a = bmf.getNewBlockNum();
        long b = bmf.getNewBlockNum();
        startSilo(bmf, a);
        save(bmf, a);
        long c = bmf.getNewBlockNum();
        if (c != b) throw new AssertionError("native engine no longer repeats the number: " + c + " vs " + b);
        save(bmf, b);
        try {
            save(bmf, c);
            throw new AssertionError("native engine saved a duplicate block number");
        } catch (BlockManifestRuntimeException expected) {
            if (!String.valueOf(expected.getMessage()).contains("out of order")) throw expected;
        }
        bmf.close();
    }

    static MultiSourceBlockManifest manifest(Path dir) throws Exception {
        Files.createDirectories(dir);
        MultiSourceBlockManifest bmf = new MultiSourceBlockManifest(dir.toString());
        bmf.open();
        return bmf;
    }

    static void fill(MultiSourceBlockManifest bmf, int count) throws Exception {
        for (int i = 0; i < count; i++) save(bmf, bmf.getNewBlockNum());
    }

    /** The native silo change the record limit triggers inside addBlockRecord. */
    static void startSilo(MultiSourceBlockManifest bmf, long startingBlockNumber) throws Exception {
        SourceBlockSiloManager silos = bmf.getSiloManager();
        Method start = SourceBlockSiloManager.class.getDeclaredMethod("startAnotherBlockManifest", long.class);
        start.setAccessible(true);
        synchronized (silos) {
            start.invoke(silos, startingBlockNumber);
        }
    }

    static void save(MultiSourceBlockManifest bmf, long number) throws Exception {
        bmf.addBlockRecord(block(number));
    }

    static SourceBlock block(long number) {
        return new SourceBlock(number, 100, (int) number, new MD5Value(new byte[16]), (byte) 0, (byte) 0);
    }

    private BlockRolloverTest() {
    }
}
