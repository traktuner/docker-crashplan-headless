package cph.archive;

import com.code42.backup.BackupId;
import com.code42.backup.restore.BackupQueryData;
import com.code42.io.path.FileId;
import com.code42.messaging.*;
import com.code42.protos.shared.authority.PlanAdministrationMessages.GetPlanFileChildrenResponse;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

/** Failure, lifetime and concurrency challenges for the real native registry. */
public final class ArchiveRegistryBoundsTest {
    public static void main(String[] args) throws Exception {
        var query = new BackupQueryData(7,8,FileId.ROOT_ID,1L,123,true,null,200,false);
        try (var fixture = new ArchiveFixture()) {
            var request = ArchiveRequestContextTest.request();
            var context = ArchiveRequests.context(fixture.session,new BackupId(99,8),request,query);
            ArchiveRequests.send(fixture.session,request,context);
            var decoded = ArchiveRequestContextTest.response(fixture.session,request);
            var wrongId = cloneResponse(decoded, decoded.getRequestId()+1, fixture.session);
            if (ArchiveRequests.claim(wrongId,GetPlanFileChildrenResponse.class)!=null) throw new AssertionError("wrong request ID claimed");
            try (var other = new ArchiveFixture()) {
                if (ArchiveRequests.claim(cloneResponse(decoded,decoded.getRequestId(),other.session),GetPlanFileChildrenResponse.class)!=null)
                    throw new AssertionError("wrong session claimed");
            }
            if (ArchiveRequests.claim(decoded,com.code42.protos.shared.RestoreMessages.FileTreeMetadataResponse.class)!=null)
                throw new AssertionError("wrong response type claimed");
            var wrongMessage = cloneResponse(decoded, decoded.getRequestId(), fixture.session);
            wrongMessage.setMessage(com.code42.protos.shared.RestoreMessages.FileTreeMetadataResponse.getDefaultInstance());
            if (ArchiveRequests.claim(wrongMessage,com.code42.protos.shared.RestoreMessages.FileTreeMetadataResponse.class)!=null
                    || ArchiveRequests.claim(wrongMessage,GetPlanFileChildrenResponse.class)!=null)
                throw new AssertionError("response kind differs from captured request or decoded message");
            var gate = new CountDownLatch(1);
            try (var pool = Executors.newFixedThreadPool(2)) {
                var one = pool.submit(() -> { gate.await(); return ArchiveRequests.claim(decoded,GetPlanFileChildrenResponse.class); });
                var two = pool.submit(() -> { gate.await(); return ArchiveRequests.claim(cloneResponse(decoded,decoded.getRequestId(),fixture.session),GetPlanFileChildrenResponse.class); });
                gate.countDown();
                if ((one.get(2,TimeUnit.SECONDS)==context ? 1:0)+(two.get(2,TimeUnit.SECONDS)==context ? 1:0)!=1)
                    throw new AssertionError("concurrent duplicate responses emitted twice");
            }
            var savedFileId=context.query().getFileId().array().clone();
            context.query().getFileId().array()[0]=1;
            if (!java.util.Arrays.equals(savedFileId,context.query().getFileId().array())) throw new AssertionError("snapshot array escaped");
            var pending = ArchiveRequestContextTest.request();
            var owned = ArchiveRequests.context(fixture.session,new BackupId(99,8),pending,query);
            ArchiveRequests.send(fixture.session,pending,owned);
            try {
                ArchiveRequests.send(fixture.session,pending,ArchiveRequests.context(fixture.session,new BackupId(99,8),pending,query));
                throw new AssertionError("duplicate native ID admitted");
            } catch (IllegalStateException expected) { /* identity conflict */ }
            if (fixture.session.getMessageContext(pending.getRequestId()) == null
                    || fixture.session.getMessageContext(pending.getRequestId()).getContext()!=owned)
                throw new AssertionError("failed cleanup removed another request");
            expire(owned);
            if (ArchiveRequests.claim(ArchiveRequestContextTest.response(fixture.session,pending),GetPlanFileChildrenResponse.class)!=null)
                throw new AssertionError("expired query accepted");
        }
        try (var fixture = new ArchiveFixture()) {
            for(int i=0;i<ArchiveRequests.MAX_PENDING;i++) fixture.send(
                ArchiveRequestContextTest.request().getMessage(),query);
            var overflow = ArchiveRequestContextTest.request();
            try {
                ArchiveRequests.send(fixture.session,overflow,ArchiveRequests.context(fixture.session,new BackupId(99,8),overflow,query));
                throw new AssertionError("unbounded native context registry");
            } catch (IllegalStateException expected) { /* finite capacity */ }
            var first=fixture.requests.getFirst();
            expire((ArchiveRequests.Context)fixture.session.getMessageContext(first.getRequestId()).getContext());
            try { fixture.send(overflow.getMessage(),query); }
            catch (IllegalStateException failure) { throw new AssertionError("expired capacity not reclaimed",failure); }
            if(fixture.session.getMessageContext(first.getRequestId())!=null) throw new AssertionError("expired context not pruned");
        }
        try (var fixture = new ArchiveFixture()) {
            fixture.deferredSend = new CompletableFuture<>();
            var request = fixture.send(ArchiveRequestContextTest.request().getMessage(),query);
            fixture.deferredSend.completeExceptionally(new MessageException("offline asynchronous failure"));
            if(ArchiveRequests.claim(ArchiveRequestContextTest.response(fixture.session,request),GetPlanFileChildrenResponse.class)!=null)
                throw new AssertionError("failed future allowed successful data");
        }
        try (var fixture = new ArchiveFixture()) {
            var request=fixture.send(ArchiveRequestContextTest.request().getMessage(),query);
            fixture.failClose=true;
            try { fixture.session.close("offline close fault"); throw new AssertionError("close fault did not run"); }
            catch(IllegalStateException expected) { /* native callback threw after close */ }
            if(fixture.session.getMessageContext(request.getRequestId())!=null) throw new AssertionError("close exception retained query");
            var closed=ArchiveRequestContextTest.request();
            try {
                ArchiveRequests.send(fixture.session,closed,ArchiveRequests.context(fixture.session,new BackupId(99,8),closed,query));
                throw new AssertionError("closed-session admission succeeded");
            } catch(IllegalStateException expected) { /* closed session */ }
        }
        System.out.println("ArchiveRegistryBoundsTest PASS");
    }

    private static void expire(ArchiveRequests.Context context) throws Exception {
        var field=ArchiveRequests.Context.class.getDeclaredField("deadline");
        field.setAccessible(true);
        field.setLong(context,System.nanoTime()-1);
    }
    private static ResponseProtoWrapper cloneResponse(ResponseProtoWrapper original,long id,ISession session) {
        var clone=new ResponseProtoWrapper(null) { @Override public long getRequestId(){return id;} };
        clone.cloneMessageContext(original); clone.setSession(session); clone.setMessage(original.getMessage());
        return clone;
    }
}
