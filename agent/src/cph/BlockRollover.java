package cph;

import com.code42.backup.manifest.source.SourceBlockManifest;
import com.code42.backup.manifest.source.SourceBlockSiloManager;
import java.lang.invoke.MethodHandles;
import java.lang.invoke.VarHandle;

/**
 * Keeps new block numbers unique when the source block manifest starts another
 * silo.
 *
 * <p>A backup takes the number for a new block from
 * {@code SourceBlockManifest.getNewBlockNum()} when the block is read, and
 * saves the block record later, after compression and encryption. Each silo
 * keeps its own counter of numbers handed out. When a saved record reaches the
 * record limit of the latest silo, {@code SourceBlockSiloManager} starts another
 * silo. The new silo starts its counter from its own last saved block, so it
 * hands out again the numbers the old silo has handed out but not saved yet.
 * The second save of such a number fails with "BMF: Block out of order" and
 * stops the backup.
 *
 * <p>The patch runs at the end of {@code startAnotherBlockManifest}. It copies
 * the counter of the previous silo into the new silo when the previous silo has
 * handed out numbers at or after the new silo's first block. The counter then
 * continues as it does without a silo change. The silo manager holds its lock
 * for the whole change and for every {@code getNewBlockNum()} call, so no number
 * is handed out in between. Any error leaves the native counter unchanged.
 */
public final class BlockRollover {

    private static volatile VarHandle counter;

    /** Called by the patched {@code startAnotherBlockManifest(long)} before it returns. */
    public static void carry(SourceBlockSiloManager silos, long startingBlockNumber) {
        try {
            SourceBlockSiloManager.Pointer added = null;
            SourceBlockSiloManager.Pointer previous = null;
            for (SourceBlockSiloManager.Pointer p : silos.getCopyOfPointers()) {
                if (p == null) {
                    continue;
                }
                if (p.blockNumber == startingBlockNumber) {
                    added = p;
                } else if (p.blockNumber < startingBlockNumber && (previous == null || p.blockNumber > previous.blockNumber)) {
                    previous = p;
                }
            }
            if (added == null || previous == null) {
                Agent.log("block-rollover: no previous block manifest before " + startingBlockNumber + ", native counter kept");
                return;
            }
            VarHandle handle = counter();
            long handedOut;
            synchronized (previous.blockManifest) {
                handedOut = (long) handle.get(previous.blockManifest);
            }
            if (handedOut < startingBlockNumber) {
                return;
            }
            SourceBlockManifest next = added.blockManifest;
            synchronized (next) {
                long own = (long) handle.get(next);
                if (own == -1L || own < handedOut) {
                    handle.set(next, handedOut);
                    Agent.log("block-rollover: block manifest " + startingBlockNumber
                            + " continues after handed-out block " + handedOut);
                }
            }
        } catch (Throwable t) {
            Agent.log("block-rollover: counter not carried, native counter kept: " + t);
        }
    }

    private static VarHandle counter() throws ReflectiveOperationException {
        VarHandle h = counter;
        if (h == null) {
            h = MethodHandles.privateLookupIn(SourceBlockManifest.class, MethodHandles.lookup())
                    .findVarHandle(SourceBlockManifest.class, "newBlockNumber", long.class);
            counter = h;
        }
        return h;
    }

    private BlockRollover() {
    }
}
