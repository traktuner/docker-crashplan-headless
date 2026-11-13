package cph.archive;

import com.code42.backup.manifest.VersionSet;
import com.code42.backup.restore.BackupQueryData;
import com.code42.engine.v3.BackupClientV3MessageFactory;
import com.code42.engine.v3.V3BackupAdapter;
import com.code42.io.path.FileId;
import com.code42.keys.PlanUid;
import com.code42.messaging.RequestProtoWrapper;
import com.code42.protos.shared.authority.PlanAdministrationMessages.*;
import com.code42.protos.shared.authority.PlanAdministrationStructure.*;
import com.code42.protos.shared.common.CoreStructure;
import com.google.protobuf.ByteString;

/** Actual response adapters must finish all opaque version pages exactly once. */
public final class ArchiveVersionPagesTest {
    private static final PlanUid PLAN = PlanUid.of(41);
    private static final FileId ROOT = V3BackupAdapter.getV3FileId(FileId.ROOT_ID, PLAN);
    private static final ByteString CURSOR = ByteString.copyFromUtf8("opaque-version-cursor");

    public static void main(String[] args) throws Exception {
        try (var fixture = new ArchiveFixture()) {
            var request = start(fixture);
            fixture.receive(page(request, 200, CURSOR));
            if (!fixture.outcomes.isEmpty() || fixture.requests.size() != 2) throw new AssertionError("first version page released as complete");
            var next = fixture.requests.getLast();
            var nativeNext = (GetPlanFileVersionsRequest) next.getMessage();
            if (!nativeNext.hasBatchVersion() || !nativeNext.getBatchVersion().equals(CURSOR)) throw new AssertionError("opaque cursor discarded");
            fixture.receive(page(next, 100, null));
            if (fixture.outcomes.size() != 1) throw new AssertionError("version terminal not emitted once");
            var result = fixture.outcomes.getFirst();
            var query = (BackupQueryData) result[2];
            var versions = (VersionSet) result[3];
            if (query.hasError() || query.getTimestamp() != 150 || versions.getVersions().size() != 2
                    || versions.getVersions().stream().noneMatch(version -> version.getTimestamp() == 100))
                throw new AssertionError("later historical version lost");
        }
        for (String fault : new String[]{"repeated-cursor", "foreign-plan", "no-progress", "native-error", "page-limit", "byte-limit", "conflicting-version"}) {
            try (var fixture = new ArchiveFixture()) {
                var request = start(fixture);
                fixture.receive(page(request, 200, CURSOR));
                var next = fixture.requests.getLast();
                var response = page(next, fault.equals("no-progress") || fault.equals("conflicting-version") ? 200 : 100,
                    fault.equals("repeated-cursor") ? CURSOR : fault.equals("no-progress") ? ByteString.copyFromUtf8("different-cursor") : null).toBuilder();
                if (fault.equals("foreign-plan")) response.setRequestPlanFiles(0, response.getRequestPlanFiles(0).toBuilder().setPlanUid(42));
                if (fault.equals("native-error")) response.addError(CoreStructure.Error.newBuilder().build());
                if (fault.equals("page-limit")) {
                    var context = (ArchiveRequests.Context) fixture.session.getMessageContext(next.getRequestId()).getContext();
                    var field = VersionPages.class.getDeclaredField("pages");
                    field.setAccessible(true);
                    field.setInt(context.versions, VersionPages.MAX_PAGES);
                }
                if (fault.equals("byte-limit")) {
                    var context = (ArchiveRequests.Context) fixture.session.getMessageContext(next.getRequestId()).getContext();
                    var field = VersionPages.class.getDeclaredField("bytes");field.setAccessible(true);
                    field.setLong(context.versions, 16L*1024*1024);
                }
                if (fault.equals("conflicting-version")) {
                    var part=response.getFileVersions(0).toBuilder();
                    var entry=part.getPlanFiles(0).toBuilder();
                    part.setPlanFiles(0,entry.setVersion(entry.getVersion().toBuilder().setSourceLength(8)));
                    response.setFileVersions(0,part);
                }
                fixture.receive(response.build());
                if (fixture.outcomes.size() != 1 || !((BackupQueryData) fixture.outcomes.getFirst()[2]).hasError()
                        || !((VersionSet) fixture.outcomes.getFirst()[3]).getVersions().isEmpty())
                    throw new AssertionError("version fault silently returned partial success: "+fault);
            }
        }
        System.out.println("ArchiveVersionPagesTest PASS");
    }

    private static RequestProtoWrapper start(ArchiveFixture fixture) throws Exception {
        var query = new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 150, true, null, 200, false);
        return fixture.send(BackupClientV3MessageFactory.buildGetPlanFileVersionsRequest(PLAN, ROOT, null, 200), query);
    }

    private static GetPlanFileVersionsResponse page(RequestProtoWrapper request, long timestamp, ByteString cursor) {
        var input = (GetPlanFileVersionsRequest) request.getMessage();
        var version = Version.newBuilder().setVersionTimestamp(timestamp).setSourceLastModified(timestamp-1).setSourceLength(7)
            .setFileType(CoreStructure.FileType.FILE).setSourceChecksumMd5(ByteString.copyFrom(new byte[16])).build();
        // Native versions conversion reads Version, not the optional PlanFile.
        var part = PlanFileVersionPair.newBuilder().setPlanUid(41).addPlanFiles(PlanFileVersion.newBuilder().setVersion(version)).build();
        var response = GetPlanFileVersionsResponse.newBuilder().setRequestId(request.getRequestId()).addAllRequestPlanFiles(input.getPlanFilesList()).addFileVersions(part);
        if (cursor != null) response.setBatchVersion(cursor);
        return response.build();
    }
}
