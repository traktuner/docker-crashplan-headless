package cph.archive;

import com.code42.backup.restore.BackupQueryData;
import com.code42.io.path.FileId;

/** Query values only. No key, session, path or request object enters JSON. */
public final class QuerySnapshot extends BackupQueryData {
    private static final long serialVersionUID = 1L;
    private transient FileId nextCursor;
    private String cphSearchPattern;
    private Boolean cphSearchFullPath;
    private Long cphSearchGuid;
    private transient boolean searchTerminal;

    public QuerySnapshot(BackupQueryData query) {
        super(query.getSourceId(), query.getTargetId(), copy(query.getFileId()),
            query.getBackupSetId().map(id -> Long.valueOf(id.asPrimitive())).orElse(null),
            query.getTimestamp(), query.isIncludeDeleted(), copy(query.getBatchLastFileId()),
            query.getBatchSize(), query.isInterrupt());
        if (query.hasError()) setErrorCode(query.getErrorCode());
    }

    static FileId copy(FileId id) { return id == null ? null : new FileId(id.getArray().clone()); }

    public static BackupQueryData snapshot(BackupQueryData query) { return new QuerySnapshot(query); }

    public static boolean searchFinished(BackupQueryData query) {
        if (!(query instanceof QuerySnapshot snapshot)) return false;
        synchronized (snapshot) { return snapshot.searchTerminal; }
    }

    public static FileId nextCursor(BackupQueryData query) {
        return query instanceof QuerySnapshot snapshot ? copy(snapshot.nextCursor) : query.getBatchLastFileId();
    }

    void nextCursor(FileId cursor) { nextCursor = copy(cursor); }

    public static void configureSearch(BackupQueryData query, String pattern, boolean filenameOnly) {
        if (!(query instanceof QuerySnapshot snapshot) || pattern == null || pattern.length() > 4096)
            throw new IllegalArgumentException("archive search context unavailable");
        snapshot.cphSearchPattern = pattern;
        snapshot.cphSearchFullPath = !filenameOnly;
    }

    public static BackupQueryData completeSearch(BackupQueryData event, BackupQueryData original,
            com.code42.keys.MessageRequestGuid guid) {
        if (!(original instanceof QuerySnapshot snapshot) || guid == null || snapshot.cphSearchPattern == null)
            throw new IllegalStateException("archive search identity unavailable");
        synchronized (snapshot) {
            if (snapshot.searchTerminal) throw new IllegalStateException("duplicate archive search terminal");
            snapshot.searchTerminal = true;
        }
        var result = new QuerySnapshot(snapshot);
        result.cphSearchPattern = snapshot.cphSearchPattern;
        result.cphSearchFullPath = snapshot.cphSearchFullPath;
        result.cphSearchGuid = guid.asPrimitive();
        if (event.hasError()) result.setErrorCode(event.getErrorCode());
        if (event.getSourceId() != snapshot.getSourceId() || event.getTargetId() != snapshot.getTargetId()
                || event.getBackupSetId().isPresent() && !event.getBackupSetId().equals(snapshot.getBackupSetId()))
            result.setErrorCode(com.code42.backup.restore.BackupQueryErrorCode.SEVERE);
        return result;
    }
}
