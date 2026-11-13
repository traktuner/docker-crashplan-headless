package cph;
import java.nio.file.*;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import com.code42.backup.handler.restoretool.RestoreFileUtilityService;
public final class RestoreRootAvailabilityTest {
 public static void main(String[] args)throws Exception{
  Path base=Files.createTempDirectory("restore-availability-"),good=Files.createDirectory(base.resolve("good")),missing=base.resolve("missing");
  var encoder=Base64.getEncoder();System.setProperty("cph.restore.roots",encoder.encodeToString(good.toString().getBytes(StandardCharsets.UTF_8))+":"+encoder.encodeToString(missing.toString().getBytes(StandardCharsets.UTF_8)));
  var files=new RestoreFileUtilityService();files.createRestoreFile(good.resolve("output").toFile());
  if(!Files.exists(good.resolve("output")))throw new AssertionError("available root refused");
  try{files.createRestoreFile(missing.resolve("output").toFile());throw new AssertionError("missing selected root admitted");}catch(SecurityException expected){}
  System.out.println("RestoreRootAvailabilityTest PASS");
 }
 private RestoreRootAvailabilityTest(){}
}
