package cph;

import com.code42.backup.handler.AgentRestoreEnv;
import com.code42.backup.handler.restoretool.IRestoreToolService;
import com.code42.backup.restore.RestoreJob;
import com.code42.backup.restore.RestoreType;
import com.code42.os.metadata.MetadataItem;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.nio.charset.StandardCharsets;

/** Actual pinned direct restore classes, not an imitation filesystem helper. */
public final class DirectRestoreBoundaryTest {
 @FunctionalInterface interface Action { void run() throws Exception; }
 private static void denied(Action action) throws Exception {
  try {action.run();throw new AssertionError("direct restore escaped declared roots");}
  catch(SecurityException expected) { }
 }
 public static void main(String[] args) throws Exception {
  Path base=Files.createTempDirectory("direct-restore-");
  Path root=Files.createDirectory(base.resolve("admitted")),outside=Files.createDirectory(base.resolve("outside"));
  System.setProperty("cph.restore.roots",Base64.getEncoder().encodeToString(root.toString().getBytes(StandardCharsets.UTF_8)));
  for(boolean permissionFlag:new boolean[]{true,false}) {
   IRestoreToolService.writePermissionEnforcementEnabled.set(permissionFlag);
   RestoreJob job=new RestoreJob(1,7L,8L);job.setRestoreType(RestoreType.CLIENT);if(!permissionFlag)job.setUserName("root");
   if(job.isUsingRestoreTool())throw new AssertionError("fixture did not select direct mode");
   var files=new AgentRestoreEnv(null,job).getRestoreFileUtilityService();
   Path allowed=root.resolve("allowed-"+permissionFlag);files.createRestoreFile(allowed.toFile());
   if(!Files.exists(allowed))throw new AssertionError("admitted direct restore failed");
   Path external=outside.resolve("file-"+permissionFlag);
   denied(()->files.createRestoreFile(external.toFile()));
   denied(()->files.mkDir(outside.resolve("dir").toFile()));
   denied(()->files.ensureRestorePath(outside.resolve("nested/file").toFile(),false));
   denied(()->files.moveRestoreFile(allowed.toFile(),external.toFile()));
   denied(()->files.moveRestoreFile(external.toFile(),allowed.toFile()));
   denied(()->files.copyRestoreFile(allowed.toFile(),external.toFile()));
   denied(()->files.copyRestoreFile(external.toFile(),allowed.toFile()));
   denied(()->files.symlinkRestoreFile(allowed.toString(),external.toString()));
   denied(()->files.applyMetadata(job,null,new MetadataItem(external.toFile(),(byte)0,1L,null,false)));
   Path directory=Files.createDirectory(root.resolve("directory-"+permissionFlag));
   Path victim=Files.writeString(outside.resolve("victim-"+permissionFlag),"preserve");
   Files.createSymbolicLink(directory.resolve(allowed.getFileName()),victim);
   denied(()->files.copyRestoreFile(allowed.toFile(),directory.toFile()));
   if(!Files.readString(victim).equals("preserve"))throw new AssertionError("directory copy followed a child symlink");
   if(Files.exists(external))throw new AssertionError("outside file was created");
  }
  System.out.println("DirectRestoreBoundaryTest PASS: no-user and enforcement-disabled paths");
 }
 private DirectRestoreBoundaryTest(){}
}
