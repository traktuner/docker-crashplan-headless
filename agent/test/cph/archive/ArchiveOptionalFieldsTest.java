package cph.archive;

import com.code42.backup.manifest.FileContents;
import com.code42.backup.manifest.VersionSet;
import com.code42.backup.restore.BackupQueryData;
import com.code42.engine.v3.BackupClientV3MessageFactory;
import com.code42.engine.v3.V1PlanProtoAdapter;
import com.code42.engine.v3.V3BackupAdapter;
import com.code42.io.path.FileId;
import com.code42.keys.PlanUid;
import com.code42.protos.shared.RestoreMessages;
import com.code42.protos.shared.RestoreStructure;
import com.code42.protos.shared.authority.PlanAdministrationMessages.*;
import com.code42.protos.shared.authority.PlanAdministrationStructure.*;
import com.code42.protos.shared.common.CoreStructure;
import com.google.protobuf.ByteString;
import java.util.List;

/** Preserve native protobuf defaults without relaxing request or plan identity. */
public final class ArchiveOptionalFieldsTest {
    public static void main(String[] args) throws Exception {
        var plan = PlanUid.of(41);
        var root = V3BackupAdapter.getV3FileId(FileId.ROOT_ID, plan);
        for (String omitted : new String[]{"fileCount", "directoryCount", "size", "all", "foreign", "negative"}) {
            try (var fixture = new ArchiveFixture()) {
                var query = new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 123, true, null, 200, false);
                var request = fixture.send(BackupClientV3MessageFactory.buildFileTreeMetadataRequest(plan, root, 123, true), query);
                var metadata = RestoreStructure.FileTreeMetadata.newBuilder().setPlanUid(41).setFileId(ByteString.copyFrom(root.array()))
                    .setFileCount(0).setDirectoryCount(0).setSizeInBytes(0);
                if (omitted.equals("fileCount") || omitted.equals("all")) metadata.clearFileCount();
                if (omitted.equals("directoryCount") || omitted.equals("all")) metadata.clearDirectoryCount();
                if (omitted.equals("size") || omitted.equals("all")) metadata.clearSizeInBytes();
                if (omitted.equals("foreign")) metadata.setPlanUid(42);
                if (omitted.equals("negative")) metadata.setFileCount(-1);
                boolean failed = omitted.equals("foreign") || omitted.equals("negative");
                var nativeCounts = V1PlanProtoAdapter.fileContentsFromProto(metadata.build());
                fixture.receive(RestoreMessages.FileTreeMetadataResponse.newBuilder().setRequestId(request.getRequestId()).setFileTreeMetadata(metadata).build());
                var outcome = fixture.outcomes.getFirst();
                if (((BackupQueryData)outcome[2]).hasError() != failed) throw new AssertionError("native count defaults changed: " + omitted);
                var counts = (FileContents)outcome[3];
                if (!failed && (!counts.isDone() || counts.getNumFiles() != nativeCounts.getNumFiles()
                        || counts.getNumDirectories() != nativeCounts.getNumDirectories() || counts.getSize() != nativeCounts.getSize()))
                    throw new AssertionError("native default counters lost");
            }
        }
        for (String grouping : new String[]{"omitted", "explicit", "foreign"}) {
            try (var fixture = new ArchiveFixture()) {
                var query = new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 123, true, null, 200, false);
                var request = fixture.send(BackupClientV3MessageFactory.buildGetPlanFileVersionsRequest(plan, root, null, 200), query);
                var input = (GetPlanFileVersionsRequest)request.getMessage();
                var version = Version.newBuilder().setVersionTimestamp(100).setSourceLastModified(90).setSourceLength(0)
                    .setSourceChecksumMd5(ByteString.copyFrom(new byte[16])).setFileType(CoreStructure.FileType.FILE);
                var part = PlanFileVersionPair.newBuilder().addPlanFiles(PlanFileVersion.newBuilder().setVersion(version));
                if (!grouping.equals("omitted")) part.setPlanUid(grouping.equals("foreign") ? 42 : 41);
                var nativeVersions = V1PlanProtoAdapter.versionSetFromProto(List.of(part.build()));
                fixture.receive(GetPlanFileVersionsResponse.newBuilder().setRequestId(request.getRequestId())
                    .addAllRequestPlanFiles(input.getPlanFilesList()).addFileVersions(part).build());
                var result = fixture.outcomes.getFirst();
                if (((BackupQueryData)result[2]).hasError() != grouping.equals("foreign")) throw new AssertionError("optional versions group identity rejected or foreign plan accepted");
                if (!grouping.equals("foreign") && ((VersionSet)result[3]).getVersions().size() != nativeVersions.getVersions().size())
                    throw new AssertionError("native versions lost");
            }
        }
        System.out.println("ArchiveOptionalFieldsTest PASS");
    }
}
