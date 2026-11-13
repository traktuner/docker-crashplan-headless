package cph.archive;

import com.backup42.common.TargetComputerUsage;
import com.backup42.common.TargetComputerUsagePlan;
import com.backup42.service.model.SocialNetworkModel;
import com.code42.backup.BackupId;
import com.code42.backup.restore.BackupQueryData;
import com.code42.engine.v3.BackupClientV3MessagingAgent;
import com.code42.engine.v3.IBackupClientV3MessageHandler;
import com.code42.keys.BackupSetId;
import com.code42.keys.PlanUid;
import com.code42.messaging.*;
import com.code42.messaging.network.INetworkChannel;
import com.code42.protos.shared.RestoreMessages;
import com.code42.protos.shared.authority.PlanAdministrationMessages;
import com.code42.service.MessageReceiptExecutor;
import com.code42.service.MessageValidator;
import com.google.protobuf.Message;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;

/** Shared offline native transport and receipt path for archive regressions. */
final class ArchiveFixture implements AutoCloseable {
    final ISession session;
    final BackupClientV3MessagingAgent agent;
    final List<Object[]> outcomes = new ArrayList<>();
    final List<RequestProtoWrapper> requests = new ArrayList<>();
    final SocialNetworkModel social = new SocialNetworkModel(null);
    int validations;
    boolean failSend, failClose, failFuture, rejectAuthority;
    CompletableFuture<Void> deferredSend;

    ArchiveFixture() throws Exception {
        Class.forName("com.crashplan.proto.api.SharedMessageIdMap");
        Class.forName("com.crashplan.proto.api.ControlMessageIdMap");
        var channel = (INetworkChannel) Proxy.newProxyInstance(getClass().getClassLoader(),
            new Class<?>[]{INetworkChannel.class}, (p, m, a) -> {
                if (m.getName().equals("sendMessage")) {
                    if (failSend) throw new MessageException("offline send failure");
                    if (a[0] instanceof RequestProtoWrapper request) requests.add(request);
                    if (deferredSend != null) return deferredSend;
                    return failFuture ? CompletableFuture.failedFuture(new MessageException("offline future failure"))
                        : CompletableFuture.completedFuture(null);
                }
                if (m.getName().equals("close") && failClose) throw new IllegalStateException("offline close callback");
                if (m.getReturnType() == boolean.class) return false;
                if (m.getReturnType() == long.class) return 0L;
                if (m.getReturnType() == int.class) return 0;
                return null;
            });
        session = SessionImpl.createSessionForTest(channel);
        session.setUsingProtos(true);
        var field = SocialNetworkModel.class.getDeclaredField("targetComputerUsages");
        field.setAccessible(true);
        var usage = new TargetComputerUsage(7, 8);
        usage.setPlans(new TargetComputerUsagePlan(PlanUid.of(41), BackupSetId.of(1), "fixture"));
        field.set(social, new ArrayList<>(List.of(usage)));
        var handler = (IBackupClientV3MessageHandler) Proxy.newProxyInstance(getClass().getClassLoader(),
            new Class<?>[]{IBackupClientV3MessageHandler.class}, (p, m, a) -> { outcomes.add(a); return null; });
        var executor = new MessageReceiptExecutor(Runnable::run, new MessageValidator(null) {
            @Override public boolean validateMessage(ProtoWrapper wrapper) {
                validations++;
                if (rejectAuthority) throw new IllegalArgumentException("offline authority rejection");
                return true;
            }
        });
        agent = new BackupClientV3MessagingAgent(social, handler, executor);
    }

    RequestProtoWrapper send(Message message, BackupQueryData query) throws MessageException {
        var request = (RequestProtoWrapper) ProtoFactory.wrapMessage(message);
        ArchiveRequests.send(session, request, ArchiveRequests.context(session, new BackupId(99, 8), request, query));
        return request;
    }

    ProtoWrapper decode(Message message) {
        var wrapper = ProtoFactory.wrapMessage(message, false);
        return ProtoFactory.buildProtoMessage(wrapper.getMessageUid(), message.toByteArray(), session);
    }

    void receive(Message message) {
        var wrapper = decode(message);
        if (message instanceof PlanAdministrationMessages.GetPlanFileChildrenResponse children) agent.receiveMessage(wrapper, children);
        else if (message instanceof PlanAdministrationMessages.GetPlanFileVersionsResponse versions) agent.receiveMessage(wrapper, versions);
        else if (message instanceof RestoreMessages.FileTreeMetadataResponse counts) agent.receiveMessage(wrapper, counts);
        else throw new AssertionError("unsupported fixture response");
    }
    @Override public void close() { failClose = false; session.close("offline fixture complete"); }
}
