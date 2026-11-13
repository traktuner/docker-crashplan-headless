package cph.archive;

import com.code42.backup.BackupId;
import com.code42.backup.restore.BackupQueryData;
import com.code42.backup.restore.BackupQueryErrorCode;
import com.code42.engine.v3.BackupClientV3;
import com.code42.engine.v3.BackupClientV3MessageFactory;
import com.code42.engine.v3.V3BackupAdapter;
import com.code42.io.path.FileId;
import com.code42.keys.PlanUid;
import com.code42.messaging.*;
import com.code42.protos.shared.RestoreMessages;
import com.code42.protos.shared.authority.PlanAdministrationMessages;
import com.code42.protos.shared.authority.PlanAdministrationStructure.PlanFilePair;
import com.code42.utils.Pair;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.atomic.AtomicBoolean;

/** Bounded query ownership inside the existing native session registry. */
public final class ArchiveRequests {
    static final int MAX_PENDING = 32;
    static final long MAX_AGE = Duration.ofMinutes(2).toNanos();

    static final class Context extends BackupId {
        private static final long serialVersionUID = 1L;
        final transient ISession session;
        final transient long requestId, deadline;
        final transient Class<?> responseType;
        final transient List<PlanFilePair> pairs;
        final transient AtomicBoolean claimed = new AtomicBoolean();
        final transient VersionPages versions;
        volatile transient java.util.concurrent.Future<Void> sendResult;
        private final transient QuerySnapshot snapshot;

        Context(ISession session, BackupId client, RequestProtoWrapper request, BackupQueryData query,
                Class<?> responseType, List<PlanFilePair> pairs) {
            super(client.getSourceGuid(), client.getTargetGuid());
            this.session = session;
            this.requestId = request.getRequestId();
            this.deadline = System.nanoTime() + MAX_AGE;
            this.snapshot = new QuerySnapshot(query);
            this.responseType = responseType;
            this.pairs = List.copyOf(pairs);
            this.versions = responseType == PlanAdministrationMessages.GetPlanFileVersionsResponse.class ? new VersionPages() : null;
        }
        Context(Context previous, RequestProtoWrapper request) {
            super(previous.getSourceGuid(), previous.getTargetGuid());
            this.session = previous.session;
            this.requestId = request.getRequestId();
            this.deadline = previous.deadline;
            this.snapshot = new QuerySnapshot(previous.snapshot);
            this.responseType = previous.responseType;
            this.pairs = previous.pairs;
            this.versions = previous.versions;
        }
        QuerySnapshot query() { return new QuerySnapshot(snapshot); }
        boolean expired() { return System.nanoTime() - deadline >= 0; }
        boolean failed() {
            var result = sendResult;
            if (result == null || !result.isDone()) return false;
            try { result.get(); return false; }
            catch (InterruptedException failure) { Thread.currentThread().interrupt(); return true; }
            catch (java.util.concurrent.ExecutionException | java.util.concurrent.CancellationException failure) { return true; }
        }
    }

    static Context context(ISession session, BackupId client, RequestProtoWrapper request, BackupQueryData query) {
        Class<?> responseType;
        List<PlanFilePair> pairs;
        if (request.getMessage() instanceof PlanAdministrationMessages.GetPlanFileChildrenRequest children) {
            responseType = PlanAdministrationMessages.GetPlanFileChildrenResponse.class;
            pairs = children.getFileIdsList();
        } else if (request.getMessage() instanceof PlanAdministrationMessages.GetPlanFileVersionsRequest versions) {
            responseType = PlanAdministrationMessages.GetPlanFileVersionsResponse.class;
            pairs = versions.getPlanFilesList();
        } else if (request.getMessage() instanceof RestoreMessages.FileTreeMetadataRequest metadata) {
            responseType = RestoreMessages.FileTreeMetadataResponse.class;
            var tree = metadata.getFileTreeQueryData();
            pairs = List.of(PlanFilePair.newBuilder().setPlanUid(tree.getPlanUid()).setFileId(tree.getFileId()).build());
        } else throw new IllegalArgumentException("unsupported archive request type");
        if (session == null || request.getRequestId() == 0 || pairs.isEmpty() || pairs.size() > MAX_PENDING
                || query.getFileId() == null || query.getBatchSize() < 1 || query.isInterrupt()
                || client.getTargetGuid() != query.getTargetId()
                || query.getBackupSetId().isPresent() && pairs.size() != 1)
            throw new IllegalArgumentException("invalid archive request identity");
        var plans = new java.util.HashSet<Long>();
        for (var pair : pairs) {
            if (!pair.hasPlanUid() || !pair.hasFileId() || pair.getFileId().size() != 16 || !plans.add(pair.getPlanUid())
                    || !new FileId(pair.getFileId().toByteArray()).equals(V3BackupAdapter.getV3FileId(query.getFileId(), PlanUid.of(pair.getPlanUid()))))
                throw new IllegalArgumentException("archive wire identity differs from query");
        }
        return new Context(session, client, request, query, responseType, pairs);
    }

    public static void send(BackupClientV3 client, IMessage message, BackupQueryData query) throws MessageException {
        try {
            if (!client.isAuthorized() || !(message instanceof RequestProtoWrapper request))
                throw new IllegalArgumentException("archive client unavailable");
            var session = client.getSession();
            send(session, request, context(session, client.getBackupId(), request, query));
        } catch (MessageException failure) {
            query.setErrorCode(BackupQueryErrorCode.IO_EXCEPTION);
            throw failure;
        } catch (RuntimeException failure) {
            query.setErrorCode(BackupQueryErrorCode.SEVERE);
            throw new MessageException("archive request refused", failure);
        }
    }

    static void send(ISession session, RequestProtoWrapper request, Context context) throws MessageException {
        try {
            session.sendRequest(request, (IMessageReceiver) null, context);
        } catch (MessageException | RuntimeException failure) {
            synchronized (session) {
                var retained = session.getMessageContext(context.requestId);
                if (retained != null && retained.getContext() == context) session.removeMessageContext(context.requestId);
            }
            throw failure;
        }
    }

    /** Map.put substitution. Ordinary native contexts retain their existing behavior. */
    public static Object put(Map<Object, Object> registry, Object key, Object value, ISession session) {
        if (!(value instanceof MessageContext message) || !(message.getContext() instanceof Context context))
            return registry.put(key, value);
        synchronized (session) {
            if (session.isClosed() || context.session != session || !Long.valueOf(context.requestId).equals(key))
                throw new IllegalStateException("archive session closed or changed");
            registry.entrySet().removeIf(entry -> entry.getValue() instanceof MessageContext retained
                && retained.getContext() instanceof Context typed && (typed.expired() || typed.failed()));
            long pending = registry.values().stream().filter(item -> item instanceof MessageContext retained
                && retained.getContext() instanceof Context).count();
            if (registry.containsKey(key) || pending >= MAX_PENDING || context.expired())
                throw new IllegalStateException("archive request capacity or identity unavailable");
            return registry.put(key, value);
        }
    }

    /** Retain the native send future; a failed transport cannot later attest success. */
    public static void sendRegistered(ISession session, IMessage message, Object value) throws MessageException {
        if (!(value instanceof Context context)) { session.sendMessage(message); return; }
        synchronized (session) {
            if (context.session != session || session.isClosed()) throw new MessageException("archive session unavailable");
            context.sendResult = session.sendMessageWithFuture(message);
            if (context.failed()) throw new MessageException("archive transport failed");
        }
    }

    /** Called immediately after native turnOff, before any close callbacks. */
    public static boolean closed(boolean changed, Map<Object, Object> registry, ISession session) {
        synchronized (session) {
            registry.entrySet().removeIf(entry -> entry.getValue() instanceof MessageContext retained
                && retained.getContext() instanceof Context);
        }
        return changed;
    }

    static Context claim(ProtoWrapper wrapper, Class<?> responseType) {
        if (!(wrapper instanceof ResponseProtoWrapper response) || !(response.getContext() instanceof Context context)
                || context.responseType != responseType || wrapper.getMessage().getClass() != responseType
                || context.requestId != wrapper.getRequestId() || context.session != wrapper.getSession()
                || context.session.isClosed() || context.expired() || context.failed() || !context.claimed.compareAndSet(false, true))
            return null;
        return context;
    }

    public static boolean missing(boolean missing, BackupQueryData query) {
        if (missing) query.setErrorCode(BackupQueryErrorCode.SEVERE);
        return missing;
    }

    public static PlanAdministrationMessages.GetPlanFileChildrenRequest children(
            Set<Pair<PlanUid, FileId>> pairs, long timestamp, boolean deleted,
            Optional<Pair<PlanUid, FileId>> nativeCursor, int limit, BackupQueryData query) {
        var cursor = nativeCursor;
        if (query.getBatchLastFileId() != null) {
            if (pairs.size() != 1) throw new IllegalArgumentException("multi-plan cursor unsupported");
            cursor = Optional.of(new Pair<>(pairs.iterator().next().getOne(), QuerySnapshot.copy(query.getBatchLastFileId())));
        }
        return BackupClientV3MessageFactory.buildGetPlanFileChildrenRequest(pairs, timestamp, deleted, cursor, limit);
    }
    private ArchiveRequests() {}
}
