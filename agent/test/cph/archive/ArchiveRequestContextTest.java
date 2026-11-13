package cph.archive;

import com.code42.backup.BackupId;
import com.code42.backup.restore.BackupQueryData;
import com.code42.engine.v3.BackupClientV3MessageFactory;
import com.code42.engine.v3.V3BackupAdapter;
import com.code42.io.path.FileId;
import com.code42.keys.PlanUid;
import com.code42.messaging.*;
import com.code42.messaging.network.INetworkChannel;
import com.code42.protos.shared.authority.PlanAdministrationMessages;
import com.code42.utils.Pair;
import java.lang.reflect.Proxy;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.CompletableFuture;

/** Native registry, actual protobuf decode and offline transport. */
public final class ArchiveRequestContextTest {
    public static void main(String[] args) throws Exception {
        Class.forName("com.crashplan.proto.api.SharedMessageIdMap");
        Class.forName("com.crashplan.proto.api.ControlMessageIdMap");
        boolean[] fail = {false};
        var channel = (INetworkChannel) Proxy.newProxyInstance(ArchiveRequestContextTest.class.getClassLoader(),
            new Class<?>[]{INetworkChannel.class}, (p, m, a) -> {
                if (m.getName().equals("sendMessage")) {
                    if (fail[0]) throw new MessageException("offline send failure");
                    return CompletableFuture.completedFuture(null);
                }
                if (m.getReturnType() == boolean.class) return false;
                if (m.getReturnType() == long.class) return 0L;
                if (m.getReturnType() == int.class) return 0;
                return null;
            });
        var session = SessionImpl.createSessionForTest(channel);
        session.setUsingProtos(true);
        var query = new BackupQueryData(7, 8, FileId.ROOT_ID, 1L, 123, true, null, 200, false);
        var first = request();
        var second = request();
        var a = ArchiveRequests.context(session, new BackupId(99, 8), first, query);
        var b = ArchiveRequests.context(session, new BackupId(99, 8), second, query);
        if (a.getSourceGuid() != 99 || a.query().getSourceId() != 7) throw new AssertionError("client/archive identity conflated");
        ArchiveRequests.send(session, first, a);
        ArchiveRequests.send(session, second, b);
        var responseB = response(session, second);
        var responseA = response(session, first);
        if (ArchiveRequests.claim(responseB, PlanAdministrationMessages.GetPlanFileChildrenResponse.class) != b
                || ArchiveRequests.claim(responseA, PlanAdministrationMessages.GetPlanFileChildrenResponse.class) != a)
            throw new AssertionError("out-of-order native context lost");
        if (ArchiveRequests.claim(responseA, PlanAdministrationMessages.GetPlanFileChildrenResponse.class) != null)
            throw new AssertionError("duplicate response accepted");
        if (ArchiveRequests.claim(response(session, first), PlanAdministrationMessages.GetPlanFileChildrenResponse.class) != null)
            throw new AssertionError("context-free duplicate accepted");
        fail[0] = true;
        var failed = request();
        try {
            ArchiveRequests.send(session, failed, ArchiveRequests.context(session, new BackupId(99, 8), failed, query));
            throw new AssertionError("failed send accepted");
        } catch (MessageException expected) { /* exact native failure */ }
        if (session.getMessageContext(failed.getRequestId()) != null) throw new AssertionError("failed-send context retained");
        fail[0] = false;
        var pending = request();
        ArchiveRequests.send(session, pending, ArchiveRequests.context(session, new BackupId(99, 8), pending, query));
        var unrelated = request();
        session.sendRequest(unrelated, (IMessageReceiver) null, new BackupId(9, 10));
        session.close("offline fixture complete");
        if (session.getMessageContext(pending.getRequestId()) != null) throw new AssertionError("closed-session query retained");
        if (session.getMessageContext(unrelated.getRequestId()) == null) throw new AssertionError("unrelated context removed");
        System.out.println("ArchiveRequestContextTest PASS");
    }

    static RequestProtoWrapper request() {
        var plan = PlanUid.of(41);
        var pair = new Pair<PlanUid, FileId>(plan, V3BackupAdapter.getV3FileId(FileId.ROOT_ID, plan));
        return (RequestProtoWrapper) ProtoFactory.wrapMessage(BackupClientV3MessageFactory.buildGetPlanFileChildrenRequest(
            Set.of(pair), 123, true, Optional.empty(), 200));
    }

    static ResponseProtoWrapper response(ISession session, RequestProtoWrapper request) {
        var input = (PlanAdministrationMessages.GetPlanFileChildrenRequest) request.getMessage();
        var message = PlanAdministrationMessages.GetPlanFileChildrenResponse.newBuilder()
            .setRequestId(request.getRequestId()).addAllRequestFileIds(input.getFileIdsList()).build();
        var wrapped = ProtoFactory.wrapMessage(message, false);
        return (ResponseProtoWrapper) ProtoFactory.buildProtoMessage(wrapped.getMessageUid(), message.toByteArray(), session);
    }
}
