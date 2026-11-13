package cph.archive;

import com.code42.backup.BackupId;
import com.code42.backup.restore.BackupQueryData;
import com.code42.engine.v3.BackupClientV3;
import com.code42.io.path.FileId;
import com.code42.keys.ComputerGuid;
import com.code42.keys.DestinationGuid;
import com.code42.protos.shared.authority.PlanAdministrationMessages.GetPlanFileChildrenRequest;
import java.lang.reflect.Field;

/** Execute the three transformed public native callers with an offline session. */
public final class ArchiveNativeCallerTest {
    public static void main(String[] args) throws Exception {
        try (var fixture = new ArchiveFixture()) {
            // Bypass unrelated engine construction. Public query methods execute normally.
            var unsafeClass = Class.forName("sun.misc.Unsafe");
            var unsafeField = unsafeClass.getDeclaredField("theUnsafe");
            unsafeField.setAccessible(true);
            var client = (BackupClientV3) unsafeClass.getMethod("allocateInstance",Class.class)
                .invoke(unsafeField.get(null),BackupClientV3.class);
            set(client,"session",fixture.session); set(client,"authorized",true);
            set(client,"backupId",new BackupId(99,8)); set(client,"socialNetworkModel",fixture.social);
            set(client,"sourceGuid",ComputerGuid.of(99)); set(client,"targetGuid",DestinationGuid.of(8));
            var cursor = new FileId(new byte[]{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0});
            var query = new BackupQueryData(7,8,FileId.ROOT_ID,1L,123,true,cursor,200,false);
            client.getChildrenFileVersions(query);
            client.getAllVersions(query);
            client.getFileContents(query);
            if (fixture.requests.size()!=3 || query.hasError()) throw new AssertionError("native query caller did not dispatch once");
            var children=(GetPlanFileChildrenRequest)fixture.requests.getFirst().getMessage();
            if(!children.hasBatchFileId()) throw new AssertionError("native caller discarded incoming cursor");
            for(var request:fixture.requests) {
                var context=fixture.session.getMessageContext(request.getRequestId()).getContext();
                if(!(context instanceof ArchiveRequests.Context owned) || owned.query().getSourceId()!=7 || owned.getSourceGuid()!=99)
                    throw new AssertionError("native caller did not retain distinct query/client identity");
            }
            for(String method:new String[]{"getChildrenFileVersions","getAllVersions","getFileContents"}) {
                var missing=new BackupQueryData(7,8,FileId.ROOT_ID,2L,0,false,null,200,false);
                BackupClientV3.class.getMethod(method,BackupQueryData.class).invoke(client,missing);
                if(!missing.hasError() || fixture.requests.size()!=3) throw new AssertionError("missing plan became silent success: "+method);
            }
        }
        System.out.println("ArchiveNativeCallerTest PASS");
    }
    private static void set(BackupClientV3 client,String name,Object value)throws Exception {
        Field field=BackupClientV3.class.getDeclaredField(name);field.setAccessible(true);field.set(client,value);
    }
}
