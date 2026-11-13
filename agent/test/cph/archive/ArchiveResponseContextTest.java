package cph.archive;

import com.backup42.common.TargetComputerUsage;
import com.backup42.common.TargetComputerUsagePlan;
import com.backup42.service.model.SocialNetworkModel;
import com.code42.backup.BackupId;
import com.code42.backup.manifest.FileContents;
import com.code42.backup.restore.BackupQueryData;
import com.code42.engine.v3.*;
import com.code42.io.path.FileId;
import com.code42.keys.*;
import com.code42.messaging.*;
import com.code42.messaging.network.INetworkChannel;
import com.code42.protos.shared.RestoreMessages;
import com.code42.protos.shared.RestoreStructure;
import com.code42.protos.shared.authority.PlanAdministrationMessages;
import com.code42.service.MessageReceiptExecutor;
import com.code42.service.MessageValidator;
import com.code42.utils.Pair;
import com.google.protobuf.ByteString;
import java.lang.reflect.Proxy;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

/** Actual patched adapters, receipt executor and native conversion. No engine. */
public final class ArchiveResponseContextTest {
    public static void main(String[] args) throws Exception {
        Class.forName("com.crashplan.proto.api.SharedMessageIdMap");
        Class.forName("com.crashplan.proto.api.ControlMessageIdMap");
        var channel = (INetworkChannel) Proxy.newProxyInstance(ArchiveResponseContextTest.class.getClassLoader(),
            new Class<?>[]{INetworkChannel.class}, (p, m, a) -> {
                if (m.getName().equals("sendMessage")) return CompletableFuture.completedFuture(null);
                if (m.getReturnType() == boolean.class) return false;
                if (m.getReturnType() == long.class) return 0L;
                if (m.getReturnType() == int.class) return 0;
                return null;
            });
        ISession session = SessionImpl.createSessionForTest(channel);
        session.setUsingProtos(true);
        var received = new AtomicReference<Object[]>();
        var validations = new AtomicInteger();
        var handler = (IBackupClientV3MessageHandler) Proxy.newProxyInstance(ArchiveResponseContextTest.class.getClassLoader(),
            new Class<?>[]{IBackupClientV3MessageHandler.class}, (p, m, a) -> { received.set(a); return null; });
        var social = new SocialNetworkModel(null);
        var usages = SocialNetworkModel.class.getDeclaredField("targetComputerUsages");
        usages.setAccessible(true);
        var tcu = new TargetComputerUsage(7, 8);
        tcu.setPlans(new TargetComputerUsagePlan(PlanUid.of(41), BackupSetId.of(1), "fixture"));
        usages.set(social, new ArrayList<>(List.of(tcu)));
        var executor = new MessageReceiptExecutor(Runnable::run, new MessageValidator(null) {
            @Override public boolean validateMessage(ProtoWrapper wrapper) { validations.incrementAndGet(); return true; }
        });
        var agent = new BackupClientV3MessagingAgent(social, handler, executor);
        var root = V3BackupAdapter.getV3FileId(FileId.ROOT_ID, PlanUid.of(41));
        var query = new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 123, true, null, 200, false);
        var request = (RequestProtoWrapper) ProtoFactory.wrapMessage(
            BackupClientV3MessageFactory.buildFileTreeMetadataRequest(PlanUid.of(41), root, 123, true));
        ArchiveRequests.send(session, request, ArchiveRequests.context(session, new BackupId(99, 8), request, query));
        var metadata = RestoreStructure.FileTreeMetadata.newBuilder().setPlanUid(41).setFileId(ByteString.copyFrom(root.array()))
            .setFileCount(7).setDirectoryCount(2).setSizeInBytes(1234).build();
        var message = RestoreMessages.FileTreeMetadataResponse.newBuilder().setRequestId(request.getRequestId()).setFileTreeMetadata(metadata).build();
        var wrapped = ProtoFactory.wrapMessage(message, false);
        var decoded = ProtoFactory.buildProtoMessage(wrapped.getMessageUid(), message.toByteArray(), session);
        agent.receiveMessage(decoded, message);
        Object[] outcome = received.get();
        if (outcome == null || (long) outcome[0] != 99 || (long) outcome[1] != 8) throw new AssertionError("client routing changed");
        var echo = (BackupQueryData) outcome[2];
        if (echo.getBackupSetId().isEmpty() || echo.getBackupSetId().orElseThrow().asPrimitive() != 1
                || echo.getSourceId() != 7 || !FileId.ROOT_ID.equals(echo.getFileId())
                || echo.getTimestamp() != 123 || !echo.isIncludeDeleted()) throw new AssertionError("original root/query lost");
        var counts = (FileContents) outcome[3];
        if (!FileId.ROOT_ID.equals(counts.getFileId()) || counts.getNumFiles() != 7 || counts.getNumDirectories() != 2
                || counts.getSize() != 1234 || !counts.isDone()) throw new AssertionError("count root normalization failed");
        if (validations.get() != 1) throw new AssertionError("native receipt validator bypassed");
        received.set(null);
        agent.receiveMessage(decoded, message);
        if (received.get() != null) throw new AssertionError("adapter emitted duplicate response");
        var cursor = new FileId(new byte[]{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0});
        var nextQuery = new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 123, true, cursor, 200, false);
        var nativeRequest = ArchiveRequests.children(Set.of(new Pair<>(PlanUid.of(41), root)),123,true,Optional.empty(),200,nextQuery);
        if (!nativeRequest.hasBatchFileId() || !cursor.equals(V1PlanProtoAdapter.batchFileIdFromProto(nativeRequest.getBatchFileId()).getTwo()))
            throw new AssertionError("incoming page cursor discarded");
        session.close("fixture complete");
        System.out.println("ArchiveResponseContextTest PASS");
    }
}
