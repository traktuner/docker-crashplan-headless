package cph.archive;

import com.code42.backup.manifest.Version;
import com.code42.backup.manifest.VersionSet;
import com.code42.engine.v3.BackupClientV3MessageFactory;
import com.code42.io.path.FileId;
import com.code42.keys.PlanUid;
import com.code42.messaging.ProtoFactory;
import com.code42.messaging.RequestProtoWrapper;
import com.code42.protos.shared.authority.PlanAdministrationMessages.GetPlanFileVersionsResponse;
import com.google.protobuf.ByteString;
import java.util.Arrays;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;

/** One bounded continuation chain moves through the native request registry. */
final class VersionPages {
    static final int MAX_VERSIONS = 10_000;
    static final int MAX_PAGES = 128;
    static final long MAX_BYTES = 16L * 1024 * 1024;
    private final Map<Long, Version> versions = new LinkedHashMap<>();
    private final Set<ByteString> cursors = new HashSet<>();
    private int pages;
    private long bytes;

    synchronized VersionSet accept(ArchiveRequests.Context context, GetPlanFileVersionsResponse response, VersionSet page) throws com.code42.messaging.MessageException {
        if (++pages > MAX_PAGES || (bytes += response.getSerializedSize()) > MAX_BYTES || context.expired()) throw ArchiveResponses.invalid();
        int before = versions.size();
        for (var version : page.getVersions()) {
            var previous = versions.putIfAbsent(version.getTimestamp(), version);
            if (previous != null && !Arrays.equals(previous.toVersionBytes(), version.toVersionBytes())) throw ArchiveResponses.invalid();
            if (versions.size() > MAX_VERSIONS) throw ArchiveResponses.invalid();
        }
        if (response.hasBatchVersion()) {
            var cursor = response.getBatchVersion();
            if (cursor.isEmpty() || cursor.size() > 4096 || !cursors.add(cursor) || versions.size() == before
                    || pages >= MAX_PAGES || context.pairs.size() != 1) throw ArchiveResponses.invalid();
            var pair = context.pairs.getFirst();
            var request = (RequestProtoWrapper) ProtoFactory.wrapMessage(BackupClientV3MessageFactory.buildGetPlanFileVersionsRequest(
                PlanUid.of(pair.getPlanUid()), new FileId(pair.getFileId().toByteArray()), cursor.toByteArray(), context.query().getBatchSize()));
            var next = new ArchiveRequests.Context(context, request);
            ArchiveRequests.send(context.session, request, next);
            return null;
        }
        var result = new VersionSet();
        result.addAll(versions.values());
        return result;
    }
}
