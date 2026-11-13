package cph.archive;

import com.backup42.service.model.SocialNetworkModel;
import com.code42.backup.manifest.FileContents;
import com.code42.backup.manifest.SecureFileVersionSet;
import com.code42.backup.manifest.VersionSet;
import com.code42.backup.restore.BackupQueryErrorCode;
import com.code42.engine.v3.IBackupClientV3MessageHandler;
import com.code42.engine.v3.V1PlanProtoAdapter;
import com.code42.io.path.FileId;
import com.code42.keys.PlanUid;
import com.code42.messaging.ProtoWrapper;
import com.code42.protos.shared.RestoreMessages;
import com.code42.protos.shared.authority.PlanAdministrationMessages;
import com.code42.protos.shared.authority.PlanAdministrationStructure.PlanFilePair;
import com.code42.protos.shared.authority.PlanAdministrationStructure.PlanFileVersionPair;
import com.code42.service.MessageReceiptExecutor;
import com.google.protobuf.Message;
import java.util.HashSet;
import java.util.List;
import java.util.function.Consumer;

/** Validate native response identity before conversion and public event delivery. */
public final class ArchiveResponses {
    public static void children(SocialNetworkModel social, IBackupClientV3MessageHandler handler,
            MessageReceiptExecutor executor, ProtoWrapper wrapper, PlanAdministrationMessages.GetPlanFileChildrenResponse message) {
        receive(executor, wrapper, message, context -> {
            var query = context.query();
            var result = new SecureFileVersionSet();
            try {
                ownership(social, context);
                if (message.getErrorCount() != 0) throw invalid();
                requested(context, message.getRequestFileIdsList());
                payload(context, message.getChildrenList(), false);
                if (message.hasBatchFileId()) {
                    var cursor = PlanFilePair.parseFrom(message.getBatchFileId());
                    if (!cursor.hasPlanUid() || cursor.getFileId().size() != 16
                            || context.pairs.stream().noneMatch(pair -> pair.getPlanUid() == cursor.getPlanUid())) throw invalid();
                    var next = new FileId(cursor.getFileId().toByteArray());
                    if (next.equals(query.getBatchLastFileId())) throw invalid();
                    query.nextCursor(next);
                }
                result = V1PlanProtoAdapter.secureFileVersionSetFromProto(message.getChildrenList());
                if (result.getFileVersions().size() > query.getBatchSize()) throw invalid();
            } catch (Exception failure) {
                refused(query);
                result = new SecureFileVersionSet();
            }
            handler.onGetPlanFileChildrenResponse(context.getSourceGuid(), context.getTargetGuid(), query, result);
        });
    }

    public static void counts(SocialNetworkModel social, IBackupClientV3MessageHandler handler,
            MessageReceiptExecutor executor, ProtoWrapper wrapper, RestoreMessages.FileTreeMetadataResponse message) {
        receive(executor, wrapper, message, context -> {
            var query = context.query();
            var result = new FileContents(query.getFileId());
            try {
                ownership(social, context);
                if (message.hasErrorCode() || !message.hasFileTreeMetadata() || context.pairs.size() != 1) throw invalid();
                var metadata = message.getFileTreeMetadata();
                var pair = context.pairs.getFirst();
                if (!metadata.hasPlanUid() || !metadata.hasFileId() || metadata.getPlanUid() != pair.getPlanUid()
                        || !metadata.getFileId().equals(pair.getFileId())
                        || metadata.getFileCount() < 0 || metadata.getDirectoryCount() < 0 || metadata.getSizeInBytes() < 0) throw invalid();
                // The wire root is plan-specific; the result retains the admitted query root.
                result.setNumFiles(metadata.getFileCount());
                result.setNumDirectories(metadata.getDirectoryCount());
                result.setSize(metadata.getSizeInBytes());
                result.setDone(true);
            } catch (RuntimeException failure) { refused(query); }
            handler.onFileTreeMetadataResponse(context.getSourceGuid(), context.getTargetGuid(), query, result);
        });
    }

    public static void versions(SocialNetworkModel social, IBackupClientV3MessageHandler handler,
            MessageReceiptExecutor executor, ProtoWrapper wrapper, PlanAdministrationMessages.GetPlanFileVersionsResponse message) {
        receive(executor, wrapper, message, context -> {
            var query = context.query();
            VersionSet result;
            try {
                ownership(social, context);
                if (message.getErrorCount() != 0) throw invalid();
                requested(context, message.getRequestPlanFilesList());
                payload(context, message.getFileVersionsList(), true);
                result = V1PlanProtoAdapter.versionSetFromProto(message.getFileVersionsList());
                // Continuation ownership is implemented by VersionPages.
                result = context.versions.accept(context, message, result);
                if (result == null) return;
            } catch (Exception failure) {
                refused(query);
                result = new VersionSet();
            }
            handler.onGetPlanFileVersionsResponse(context.getSourceGuid(), context.getTargetGuid(), query, result);
        });
    }

    private static <T extends Message> void receive(MessageReceiptExecutor executor, ProtoWrapper wrapper,
            T message, Consumer<ArchiveRequests.Context> work) {
        // Keep the vendor authority validator and receipt executor in the actual path.
        executor.onReceiveMessage(wrapper, new MessageReceiptExecutor.AlwaysExecutedMessageReceiptWork<T>(wrapper, message) {
            @Override public void doWork() {
                var context = ArchiveRequests.claim(wrapper, message.getClass());
                if (context != null) work.accept(context);
            }
        });
    }

    private static void ownership(SocialNetworkModel social, ArchiveRequests.Context context) {
        var query = context.query();
        if (query.getBackupSetId().isPresent()) {
            var plan = social.getPlanUidForBackupSetId(query.getSourceId(), query.getBackupSetId().orElseThrow());
            if (context.pairs.size() != 1 || plan.isEmpty() || plan.orElseThrow().asPrimitive() != context.pairs.getFirst().getPlanUid()) throw invalid();
        }
        for (var pair : context.pairs) {
            long count = social.getTargetComputerUsages().stream().filter(usage -> usage.getSourceGuid() == query.getSourceId()
                && usage.getTargetGuid() == query.getTargetId() && usage.getPlanUids().contains(PlanUid.of(pair.getPlanUid()))).count();
            if (count != 1) throw invalid();
        }
    }

    private static void requested(ArchiveRequests.Context context, List<PlanFilePair> pairs) {
        if (pairs.size() != context.pairs.size() || new HashSet<>(pairs).size() != pairs.size()
                || !new HashSet<>(pairs).equals(new HashSet<>(context.pairs))) throw invalid();
    }

    private static void payload(ArchiveRequests.Context context, List<PlanFileVersionPair> parts, boolean versions) {
        var seen = new HashSet<Long>();
        var files = new HashSet<com.google.protobuf.ByteString>();
        int entries = 0;
        for (var part : parts) {
            // An omitted grouping ID is unambiguous only for an exactly bound single-plan request.
            long plan = part.hasPlanUid() ? part.getPlanUid()
                : context.pairs.size() == 1 ? context.pairs.getFirst().getPlanUid() : -1;
            if (!seen.add(plan)) throw invalid();
            var expected = context.pairs.stream().filter(pair -> pair.getPlanUid() == plan).findFirst().orElseThrow(ArchiveResponses::invalid);
            for (var item : part.getPlanFilesList()) {
                var file = item.getPlanFile();
                if (++entries > (versions ? VersionPages.MAX_VERSIONS : context.query().getBatchSize())
                        || !item.hasVersion() || !versions && !item.hasPlanFile()
                        || item.hasPlanFile() && (!file.hasPlanUid() || file.getPlanUid() != plan
                            || !file.hasFileId() || file.getFileId().size() != 16 || versions && !file.getFileId().equals(expected.getFileId()))
                        || !versions && (!file.hasParentFileId() || !file.getParentFileId().equals(expected.getFileId()) || !files.add(file.getFileId()))) throw invalid();
            }
        }
    }

    static IllegalArgumentException invalid() { return new IllegalArgumentException("invalid archive response identity or data"); }
    private static void refused(QuerySnapshot query) {
        query.setErrorCode(BackupQueryErrorCode.IO_EXCEPTION);
        System.err.println("[cph-agent] archive response rejected: INVALID_RESPONSE");
    }
    private ArchiveResponses() {}
}
