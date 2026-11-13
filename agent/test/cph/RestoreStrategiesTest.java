package cph;
import java.io.File;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.zip.GZIPOutputStream;
import com.code42.backup.handler.AgentRestoreEnv;
import com.code42.backup.handler.restoretool.ARestoreFileUtilityService;
import com.code42.backup.handler.restoretool.RestoreFileUtilityService;
import com.code42.backup.handler.restoretool.RestoreToolFileUtilityService;
import com.code42.backup.handler.strategy.CompressFirstRestoreStrategy;
import com.code42.backup.handler.strategy.SymlinkRestoreStrategy;
import com.code42.backup.restore.RestoreJob;
import com.code42.backup.restore.RestoreType;
import com.code42.transport.FileType;
public final class RestoreStrategiesTest {
 static class Compressed extends CompressFirstRestoreStrategy {
  Compressed(File output,File tmp,RestoreJob job,ARestoreFileUtilityService fs){super(null,null,FileType.FILE,8L,null,output,job,fs);setTmpFile(tmp);}
 }
 static class Link extends SymlinkRestoreStrategy {
  Link(File output,File tmp,RestoreJob job,ARestoreFileUtilityService fs){super(null,null,FileType.SYMLINK,8L,null,output,job,fs);setTmpFile(tmp);}
 }
 public static void main(String[] args)throws Exception {
  Path base=Files.createTempDirectory("restore-strategy-"),root=Files.createDirectory(base.resolve("root")),outside=Files.createDirectory(base.resolve("outside"));
  System.setProperty("cph.restore.roots",Base64.getEncoder().encodeToString(root.toString().getBytes(StandardCharsets.UTF_8)));
  RestoreJob job=new RestoreJob(1,7L,8L);job.setUserName("root");job.setRestoreType(RestoreType.CLIENT);job.setValidate(false);
  ARestoreFileUtilityService fs=new AgentRestoreEnv(null,job).getRestoreFileUtilityService();
  if(!(fs instanceof RestoreToolFileUtilityService))throw new AssertionError("fixture did not select helper mode");
  Path zipped=root.resolve("compressed.tmp"),victim=outside.resolve("victim"),output=root.resolve("output");
  try(var gzip=new GZIPOutputStream(Files.newOutputStream(zipped))){gzip.write("restored".getBytes(StandardCharsets.UTF_8));}
  Files.createSymbolicLink(output,victim);
  new Compressed(output.toFile(),zipped.toFile(),job,fs).finishRestore();
  int failures=0;
  if(Files.exists(victim)){System.out.println("FAIL compressed write followed dangling output symlink");failures++;}
  Files.delete(output);new Compressed(output.toFile(),zipped.toFile(),job,fs).finishRestore();
  if(!Files.readString(output).equals("restored"))throw new AssertionError("normal compressed restore failed");
  int index=0;
  for(String target:new String[]{"relative-target",root.resolve("internal-target").toString(),outside.resolve("link-target").toString()}){
   Path tmp=root.resolve("link-"+index+".tmp"),dst=root.resolve("link-"+index);index++;Files.writeString(tmp,target);
   new Link(dst.toFile(),tmp.toFile(),job,new RestoreFileUtilityService()).finishRestore();
   if(!Files.isSymbolicLink(dst)||!Files.readSymbolicLink(dst).toString().equals(target)){System.out.println("FAIL native symlink object "+index);failures++;}
  }
  if(failures>0)throw new AssertionError("restore strategy failures="+failures);
  System.out.println("RestoreStrategiesTest PASS: compressed helper output and relative/internal/external link objects");
 }
 private RestoreStrategiesTest(){}
}
