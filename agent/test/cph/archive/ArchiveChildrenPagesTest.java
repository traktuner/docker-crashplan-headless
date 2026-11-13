package cph.archive;

import com.code42.backup.restore.BackupQueryData;
import com.code42.engine.v3.BackupClientV3MessageFactory;
import com.code42.engine.v3.V3BackupAdapter;
import com.code42.io.path.FileId;
import com.code42.keys.PlanUid;
import com.code42.protos.shared.authority.PlanAdministrationMessages.*;
import com.code42.protos.shared.authority.PlanAdministrationStructure.*;
import com.code42.protos.shared.common.CoreStructure;
import com.code42.utils.Pair;
import com.google.protobuf.ByteString;
import java.util.Optional;
import java.util.Set;

/** Request and response cursors are separate; foreign parts never become data. */
public final class ArchiveChildrenPagesTest {
    public static void main(String[] args) throws Exception {
        var plan=PlanUid.of(41);
        var root=V3BackupAdapter.getV3FileId(FileId.ROOT_ID,plan);
        var a=new FileId(new byte[]{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0});
        var b=new FileId(new byte[]{2,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0});
        for(String mode:new String[]{"next","last","foreign-part","foreign-cursor","malformed-cursor","wrong-parent","native-error"}) {
            try(var fixture=new ArchiveFixture()) {
                var query=new BackupQueryData(7,8,FileId.ROOT_ID,1L,123,true,a,200,false);
                var request=fixture.send(ArchiveRequests.children(Set.of(new Pair<>(plan,root)),123,true,Optional.empty(),200,query),query);
                var input=(GetPlanFileChildrenRequest)request.getMessage();
                var response=GetPlanFileChildrenResponse.newBuilder().setRequestId(request.getRequestId()).addAllRequestFileIds(input.getFileIdsList());
                if(mode.equals("next")||mode.equals("foreign-cursor")) response.setBatchFileId(PlanFilePair.newBuilder().setPlanUid(mode.equals("next")?41:42).setFileId(ByteString.copyFrom(b.array())).build().toByteString());
                if(mode.equals("foreign-part")) response.addChildren(PlanFileVersionPair.newBuilder().setPlanUid(42));
                if(mode.equals("wrong-parent")) response.setRequestFileIds(0,input.getFileIds(0).toBuilder().setFileId(ByteString.copyFrom(b.array())));
                if(mode.equals("malformed-cursor")) response.setBatchFileId(ByteString.copyFromUtf8("invalid"));
                if(mode.equals("native-error")) response.clearRequestFileIds().addError(CoreStructure.Error.newBuilder().build());
                fixture.receive(response.build());
                if(fixture.outcomes.size()!=1) throw new AssertionError("children terminal missing: "+mode);
                var echo=(BackupQueryData)fixture.outcomes.getFirst()[2];
                boolean failed=!mode.equals("next")&&!mode.equals("last");
                if(echo.hasError()!=failed) throw new AssertionError("foreign/error children became success: "+mode);
                if(!a.equals(echo.getBatchLastFileId())||!FileId.ROOT_ID.equals(echo.getFileId())||echo.getTimestamp()!=123||!echo.isIncludeDeleted())
                    throw new AssertionError("original children query changed");
                if(!failed && (mode.equals("next")?!b.equals(QuerySnapshot.nextCursor(echo)):QuerySnapshot.nextCursor(echo)!=null))
                    throw new AssertionError("requested cursor reused as response cursor");
            }
        }
        System.out.println("ArchiveChildrenPagesTest PASS");
    }
}
