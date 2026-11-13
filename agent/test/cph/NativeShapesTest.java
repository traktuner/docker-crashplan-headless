package cph;

import com.backup42.common.config.ServiceBackupConfig;
import com.backup42.common.config.ServicePeerConfig;
import com.backup42.service.ui.message.GetFileContentsResultMessage;
import com.backup42.service.ui.message.BackupStatsMessage;
import com.backup42.service.ui.message.GetBackupStatsMessage;
import com.backup42.service.ui.message.ComputersMessage;
import com.backup42.common.Computer;
import com.backup42.common.IComputer;
import com.code42.backup.save.BackupClientStats;
import com.code42.backup.CustomKeyGenerator;
import com.code42.archive.key.ArchiveKeyAbstractFactory;
import com.code42.archive.key.ArchiveKeyVersion;
import com.backup42.service.util.RestJsonBuilder;
import com.code42.backup.manifest.FileContents;
import com.code42.backup.restore.BackupQueryData;
import com.code42.io.path.FileId;
import com.google.gson.JsonParser;

/** Source-backed wire shapes, using the actual image's vendor serializer. */
public final class NativeShapesTest {
 public static void main(String[] args){
  var gson=new RestJsonBuilder().excludeFields("publish").create();
  var notify=JsonParser.parseString(gson.toJson(new ServiceBackupConfig().notifyDeliveryTime)).getAsJsonObject();
  if(!notify.get("value").getAsString().equals("ANY"))throw new AssertionError("notification default changed");
  var proxy=JsonParser.parseString(gson.toJson(new ServicePeerConfig().remotePACFileUrl)).getAsJsonObject();
  if(proxy.has("value") || !proxy.has("locked"))throw new AssertionError("optional proxy shape changed");
  var contents=new FileContents(FileId.ROOT_ID);contents.setNumFiles(42);contents.setNumDirectories(3);contents.setSize(9007199254740993L);contents.setDone(true);
  var query=new BackupQueryData(7,8,FileId.ROOT_ID,Long.valueOf(1),0,false,null,200,false);
  var result=JsonParser.parseString(new GetFileContentsResultMessage(query,contents).toJson()).getAsJsonObject();
  if(result.has("backupQueryData"))throw new AssertionError("contents query-envelope contract changed");
  var data=result.getAsJsonObject("contents");
  if(!data.get("done").getAsBoolean() || data.get("numFiles").getAsInt()!=42 || data.get("numDirectories").getAsInt()!=3 || !data.get("size").getAsString().equals("9007199254740993") || !data.getAsJsonObject("fileId").get("array").getAsString().equals("AAAAAAAAAAAAAAAAAAAAAA=="))throw new AssertionError("contents statistics wire contract changed");
  var stats=new BackupClientStats(7L,8L);stats.setRemoteManifestSize(42L);
  if(gson.fromJson("{\"targetId\":\"8\"}",GetBackupStatsMessage.class).getTargetId()!=8L)throw new AssertionError("maintenance request contract changed");
  Computer cloud=new Computer(8L,"Fixture cloud",1,false,false,false,false,true,true);
  cloud.setDisconnectedCode(IComputer.DisconnectedCode.CONNECTED);
  cloud.setDefaultArchiveType(com.code42.backup.identity.ArchiveType.ARCHIVE_V3);
  var computer=JsonParser.parseString(new ComputersMessage(java.util.List.of(cloud),true).toJson()).getAsJsonObject().getAsJsonArray("computers").get(0).getAsJsonObject();
  if(computer.has("connectedTimeInMillis")||!computer.getAsJsonObject("properties").get("DCODE").getAsString().equals("CONNECTED"))throw new AssertionError("computer connection contract changed");
  if(!computer.get("defaultArchiveType").getAsString().equals("ARCHIVE_V3"))throw new AssertionError("archive type projection changed");
  var wire=JsonParser.parseString(new BackupStatsMessage(stats).toJson()).getAsJsonObject().getAsJsonObject("backupStats");
  if(wire.get("sourceId").getAsLong()!=7L || wire.get("targetId").getAsLong()!=8L || !wire.get("isBackupTarget").getAsBoolean() || wire.get("remoteManifestSize").getAsLong()!=42L)throw new AssertionError("maintenance eligibility shape changed");
  var serializer=ArchiveKeyAbstractFactory.getFactory(ArchiveKeyVersion.V1).createArchiveKeySerializer();
  for(boolean v2:new boolean[]{false,true}){
   var generator=new CustomKeyGenerator(7,v2);
   var first=serializer.serialize(generator.generateFromPassphrase("fixture phrase"));
   var second=serializer.serialize(generator.generateFromPassphrase("fixture phrase"));
   if(!first.equals(second) || java.util.Base64.getDecoder().decode(first).length!=56)throw new AssertionError("native passphrase contract changed");
   if(v2 && first.equals(serializer.serialize(new CustomKeyGenerator(8,true).generateFromPassphrase("fixture phrase"))))throw new AssertionError("V2 derivation lost user context");
  }
  System.out.println("NativeShapesTest PASS: settings, contents, maintenance and native passphrase derivation");
 }
 private NativeShapesTest(){}
}
