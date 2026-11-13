package cph;

import com.backup42.service.ui.message.GetFileContentsResultMessage;
import com.backup42.service.ui.message.GetAllVersionsResultMessage;
import com.code42.backup.manifest.FileContents;
import com.code42.backup.manifest.VersionSet;
import com.code42.backup.restore.BackupQueryData;
import com.code42.io.path.FileId;
import com.google.gson.JsonParser;

/** Verify correlation and error metadata from the actual patched serializer. */
public final class NativeQueryEchoTest {
 public static void main(String[] args){
  var query=new BackupQueryData(7,8,FileId.ROOT_ID,Long.valueOf(1),123,false,null,200,false);
  for(String json:new String[]{new GetFileContentsResultMessage(query,new FileContents(FileId.ROOT_ID)).toJson(),new GetAllVersionsResultMessage(query,new VersionSet()).toJson()}){
   var result=JsonParser.parseString(json).getAsJsonObject();
   if(!result.has("backupQueryData"))throw new AssertionError("native query identity was discarded");
   var echo=result.getAsJsonObject("backupQueryData");
   if(echo.get("sourceId").getAsLong()!=7||echo.get("targetId").getAsLong()!=8||echo.get("timestamp").getAsLong()!=123)throw new AssertionError("query identity changed");
   if(result.has("flattened"))throw new AssertionError("internal version storage was exposed");
  }
  System.out.println("NativeQueryEchoTest PASS");
 }
}
