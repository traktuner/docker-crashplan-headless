package cph;

import com.code42.backup.manifest.file.FileManifest;
import com.code42.backup.manifest.file.FileManifestEntry;
import com.code42.backup.manifest.file.FileManifestEntryIterator;
import com.code42.io.IOIterator;
import com.code42.io.path.FileId;
import java.io.IOException;
import java.lang.invoke.MethodHandle;
import java.lang.invoke.MethodHandles;
import java.lang.invoke.MethodType;
import java.lang.invoke.VarHandle;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Lets the native maintenance job resume a file pass without reading the
 * file manifest from its start.
 *
 * <p>{@code SourceMaintenanceJob.processFiles} applies each version-reduction
 * point the destination sends. Every point names a start entry number, and
 * the native loop reaches it by reading and discarding every earlier entry.
 * On a large archive that is a full read of the file manifest (tens of GB)
 * for each point, even when the point covers a single entry.
 *
 * <p>The patch replaces only the iterator the loop uses. The iterator remembers
 * the position, file ID and end of the last entry it returned. A later pass
 * that starts after that entry gets the same entries the native iterator would
 * return: entries before the saved one are reported as {@code null} without
 * reading (the native loop discards them unread), and reading continues behind
 * the saved entry. The saved entry is checked before it is used: same manifest
 * file, a manifest that has not shrunk, and the manifest's own index still
 * places the saved file ID at the saved position. The native writer keeps entry
 * positions and lengths (an entry is rewritten in place when its length stays
 * the same and appended otherwise, and a removed entry keeps its file ID and
 * length), so a passing check means the entry numbering up to it is unchanged.
 * Anything else, including any error, uses the native iterator from the start.
 * Nothing is read at an unchecked position.
 *
 * <p>{@code CPH_REDUCE_RESUME=0} turns the shortcut off.
 */
public final class ReduceResume {

    /** Last entry returned for a manifest file, keyed by its path. */
    record Mark(int index, long position, long end, FileId fileId, long dataSize) {
    }

    private static final Map<String, Tracking> LAST = new ConcurrentHashMap<>();
    private static final boolean ENABLED = !"0".equals(System.getenv("CPH_REDUCE_RESUME"));
    private static volatile Access access;
    /** Successful resumes, for tests. */
    static volatile int seeks;

    /** Called in place of {@code fmf.fileManifestEntryIterator(includeRemoved)}. */
    public static IOIterator<FileManifestEntry> iterator(FileManifest fmf, boolean includeRemoved, int startEntryNum)
            throws IOException {
        IOIterator<FileManifestEntry> nativeIterator = fmf.fileManifestEntryIterator(includeRemoved);
        if (!ENABLED || !includeRemoved || !(nativeIterator instanceof FileManifestEntryIterator inner)) {
            return nativeIterator;
        }
        String key;
        try {
            key = fmf.getPath().getCanonicalPath();
        } catch (Exception e) {
            Agent.log("reduce-resume: manifest path unavailable, reading from the start: " + e);
            return nativeIterator;
        }
        Tracking previous = LAST.get(key);
        Mark mark = previous == null ? null : previous.mark();
        int skip = 0;
        if (mark != null && startEntryNum > mark.index()) {
            try {
                long next = verify(fmf, mark);
                if (next >= 0) {
                    access().position(inner, next);
                    skip = mark.index() + 1;
                    seeks++;
                    Agent.log("reduce-resume: start entry " + startEntryNum + ", continuing after saved entry "
                            + mark.index() + " instead of reading " + skip + " entries");
                } else {
                    Agent.log("reduce-resume: saved entry " + mark.index() + " no longer matches, reading from the start");
                }
            } catch (Throwable t) {
                Agent.log("reduce-resume: check failed, reading from the start: " + t);
                nativeIterator = fmf.fileManifestEntryIterator(true);
                if (!(nativeIterator instanceof FileManifestEntryIterator fresh)) {
                    return nativeIterator;
                }
                inner = fresh;
                skip = 0;
            }
        }
        Tracking tracking = new Tracking(fmf, inner, skip);
        LAST.put(key, tracking);
        return tracking;
    }

    /**
     * Returns the position after the saved entry, or -1 when it does not match.
     * The manifest's own index must still place the saved file ID at the saved
     * position. An entry only moves when its length changes, and compaction or
     * repair moves entries, so a match also keeps the saved entry end.
     */
    private static long verify(FileManifest fmf, Mark mark) throws IOException {
        synchronized (fmf) {
            long size = fmf.getDataSize();
            if (size < mark.dataSize() || mark.end() > size) {
                return -1;
            }
            return fmf.getPosition(mark.fileId()) == mark.position() ? mark.end() : -1;
        }
    }

    static void forget() {
        LAST.clear();
    }

    private static Access access() throws ReflectiveOperationException {
        Access a = access;
        if (a == null) {
            a = new Access();
            access = a;
        }
        return a;
    }

    /** Package-private engine members, resolved once. */
    private static final class Access {
        final VarHandle position;
        final MethodHandle end;

        Access() throws ReflectiveOperationException {
            var lookup = MethodHandles.lookup();
            position = MethodHandles.privateLookupIn(FileManifestEntryIterator.class, lookup)
                    .findVarHandle(FileManifestEntryIterator.class, "position", long.class);
            end = MethodHandles.privateLookupIn(FileManifestEntry.class, lookup)
                    .findVirtual(FileManifestEntry.class, "getNextEntryPosition", MethodType.methodType(long.class));
        }

        void position(FileManifestEntryIterator it, long value) {
            position.set(it, value);
        }

        long end(FileManifestEntry entry) throws Throwable {
            return (long) end.invokeExact(entry);
        }
    }

    /** The iterator the native loop uses; remembers the last entry it returned. */
    static final class Tracking implements IOIterator<FileManifestEntry> {
        private final FileManifest fmf;
        private final FileManifestEntryIterator inner;
        private int unread;
        private int index = -1;
        // The last entry returned; null until one was read.
        private FileManifestEntry last;
        private int lastIndex;
        private long lastDataSize;

        Tracking(FileManifest fmf, FileManifestEntryIterator inner, int unread) {
            this.fmf = fmf;
            this.inner = inner;
            this.unread = unread;
        }

        /** The last entry returned, or null when none was read or it cannot be described. */
        synchronized Mark mark() {
            if (last == null) {
                return null;
            }
            try {
                return new Mark(lastIndex, last.getPosition(), access().end(last), last.getFileId(), lastDataSize);
            } catch (Throwable t) {
                return null;
            }
        }

        @Override
        public synchronized boolean hasNext() throws IOException {
            return unread > 0 || inner.hasNext();
        }

        @Override
        public synchronized FileManifestEntry next() {
            if (unread > 0) {
                unread--;
                index++;
                return null;
            }
            FileManifestEntry entry = inner.next();
            if (entry != null) {
                index++;
                last = entry;
                lastIndex = index;
                lastDataSize = fmf.getDataSize();
            }
            return entry;
        }

        @Override
        public void remove() {
            inner.remove();
        }
    }

    private ReduceResume() {
    }
}
