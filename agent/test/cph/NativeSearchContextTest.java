package cph;

import com.backup42.service.ui.message.SearchFileVersionsResultMessage;
import com.code42.archive.key.IArchiveKey;
import com.code42.backup.IBackupClient;
import com.code42.backup.event.query.search.GetSearchArchiveJobEvent;
import com.code42.backup.manifest.FileVersionSet;
import com.code42.backup.restore.BackupQueryData;
import com.code42.io.path.FileId;
import com.code42.keys.MessageRequestGuid;
import com.code42.protos.shared.v3.ArchiveSearchStructure;
import com.code42.service.app.SearchFileVersionsV3Context;
import com.google.gson.JsonParser;
import java.lang.reflect.Proxy;

/** Execute the patched native search owner without a network or account. */
public final class NativeSearchContextTest {
    private static final MessageRequestGuid GUID = MessageRequestGuid.of(123L);
    private static final IBackupClient CLIENT = (IBackupClient) Proxy.newProxyInstance(
        NativeSearchContextTest.class.getClassLoader(), new Class<?>[]{IBackupClient.class},
        (p, m, a) -> m.getName().equals("searchFileVersions") ? GUID : null);

    public static void main(String[] args) throws Exception {
        for (var status : new ArchiveSearchStructure.SearchArchiveJobStatus[]{
                ArchiveSearchStructure.SearchArchiveJobStatus.DONE,
                ArchiveSearchStructure.SearchArchiveJobStatus.ERROR}) {
            var query = new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 123, true, null, 200, false);
            var context = context(query);
            if (context.handleEvent(event(456, status)) != null) throw new AssertionError("foreign GUID accepted");
            if (context.handleEvent(event(123, ArchiveSearchStructure.SearchArchiveJobStatus.WORKING)) != null)
                throw new AssertionError("working search became terminal");
            SearchFileVersionsResultMessage result = context.handleEvent(event(123, status));
            var echo = result.getBackupQueryData();
            if (!FileId.ROOT_ID.equals(echo.getFileId()) || echo.getSourceId() != 7 || echo.getTargetId() != 8
                    || echo.getBackupSetId().orElseThrow().asPrimitive() != 1 || echo.getTimestamp() != 123
                    || !echo.isIncludeDeleted() || echo.getBatchSize() != 200 || echo.getBatchLastFileId() != null)
                throw new AssertionError("original query discarded");
            if (echo.hasError() != (status == ArchiveSearchStructure.SearchArchiveJobStatus.ERROR))
                throw new AssertionError("native search error discarded");
            var json = JsonParser.parseString(result.toJson()).getAsJsonObject().getAsJsonObject("backupQueryData");
            if (!json.get("cphSearchPattern").getAsString().equals("photo.*")
                    || !json.get("cphSearchFullPath").getAsBoolean()
                    || json.get("cphSearchGuid").getAsLong() != 123)
                throw new AssertionError("search mode/pattern/GUID discarded");
            if (context.handleEvent(event(123, status)) != null) throw new AssertionError("duplicate terminal emitted");
        }
        var canceled = context(new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 0, false, null, 200, false));
        if (canceled.handleEvent(event(123, ArchiveSearchStructure.SearchArchiveJobStatus.CANCELLED)) != null
                || canceled.handleEvent(event(123, ArchiveSearchStructure.SearchArchiveJobStatus.DONE)) != null)
            throw new AssertionError("canceled search emitted success");
        var failedCreate = context(new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 0, false, null, 200, false));
        var createError = new com.code42.backup.event.query.search.CreateSearchArchiveJobEvent(CLIENT,
            BackupQueryData.builder(7,8,null).build(), GUID, new com.code42.backup.jobmanager.JobId(1),
            ArchiveSearchStructure.ArchiveSearchJobErrors.ARCHIVE_NOT_READY);
        if (failedCreate.handleEvent(createError) == null || !failedCreate.isFinished()
                || failedCreate.handleEvent(createError) != null) throw new AssertionError("create error did not finish native search owner");
        System.out.println("NativeSearchContextTest PASS");
    }

    private static SearchFileVersionsV3Context context(BackupQueryData q) throws Exception {
        IArchiveKey key = (IArchiveKey) Proxy.newProxyInstance(NativeSearchContextTest.class.getClassLoader(),
            new Class<?>[]{IArchiveKey.class}, (p, m, a) -> null);
        var constructor = SearchFileVersionsV3Context.class.getDeclaredConstructors()[0];
        constructor.setAccessible(true);
        var context = (SearchFileVersionsV3Context) constructor.newInstance(CLIENT, q, key);
        context.startFileVersionsSearch(q, "photo.*", false);
        return context;
    }

    private static GetSearchArchiveJobEvent event(long guid, ArchiveSearchStructure.SearchArchiveJobStatus status) {
        return new GetSearchArchiveJobEvent(CLIENT, BackupQueryData.builder(7, 8, null).build(),
            MessageRequestGuid.of(guid), new FileVersionSet(), status,
            status == ArchiveSearchStructure.SearchArchiveJobStatus.ERROR
                ? ArchiveSearchStructure.ArchiveSearchJobErrors.ARCHIVE_NOT_READY : null);
    }
}
