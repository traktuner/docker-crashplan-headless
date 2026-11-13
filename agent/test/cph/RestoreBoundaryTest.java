package cph;

import com.code42.restoretool.ipc.RestoreToolIpc;
import com.code42.backup.handler.restoretool.RestoreToolProxy;
import com.code42.os.pipe.IPipe;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Proxy;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;

/** Exercise the real patched IPC dispatch, including an already-used proxy. */
public final class RestoreBoundaryTest {
 public static void main(String[] args) throws Exception {
  Path root=Files.createTempDirectory("restore-boundary-");
  System.setProperty("cph.restore.roots",Base64.getEncoder().encodeToString(root.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8)));
  AtomicInteger writes=new AtomicInteger();
  IPipe pipe=(IPipe)Proxy.newProxyInstance(IPipe.class.getClassLoader(),new Class<?>[]{IPipe.class},(o,m,a)->{if(m.getName().equals("write")){writes.incrementAndGet();return ((byte[])a[0]).length;}if(m.getReturnType()==boolean.class)return false;if(m.getReturnType()==int.class)return 0;return null;});
  var executor=Executors.newSingleThreadScheduledExecutor();
  try {
   RestoreToolProxy proxy=new RestoreToolProxy(pipe,executor);
   var send=RestoreToolProxy.class.getDeclaredMethod("sendRequest",RestoreToolIpc.RequestMessage.class);send.setAccessible(true);
   var inside=RestoreToolIpc.RequestMessage.newBuilder().setRequestType(RestoreToolIpc.RequestType.MAKE_FILE).setRequestId(1).setRequestMakePathData(RestoreToolIpc.RequestMakePathData.newBuilder().setRestorePath(root.resolve("allowed").toString())).build();
   send.invoke(proxy,inside);
   int before=writes.get();if(before!=2)throw new AssertionError("allowed request did not reach IPC");
   for(String path:new String[]{root.resolveSibling("outside").toString(),root.resolve("../outside").toString()}) {
    var outside=inside.toBuilder().setRequestId(2).setRequestMakePathData(RestoreToolIpc.RequestMakePathData.newBuilder().setRestorePath(path)).build();
    try {send.invoke(proxy,outside);throw new AssertionError("later outside-root request reached cached proxy");}
    catch(InvocationTargetException e){if(!(e.getCause() instanceof SecurityException))throw e;}
    if(writes.get()!=before)throw new AssertionError("rejected request wrote IPC");
   }
   Path external=Files.createTempDirectory("restore-outside-");Files.createSymbolicLink(root.resolve("link"),external);
   var link=inside.toBuilder().setRequestId(3).setRequestMakePathData(RestoreToolIpc.RequestMakePathData.newBuilder().setRestorePath(root.resolve("link/file").toString())).build();
   try{send.invoke(proxy,link);throw new AssertionError("symlink escape reached IPC");}catch(InvocationTargetException e){if(!(e.getCause() instanceof SecurityException))throw e;}
   // Every filesystem operation uses the same guarded dispatch method.
   for(var type:new RestoreToolIpc.RequestType[]{RestoreToolIpc.RequestType.MOVE_FILE,RestoreToolIpc.RequestType.COPY_FILE,RestoreToolIpc.RequestType.MAKE_SYMLINK}) {
    var request=RestoreToolIpc.RequestMessage.newBuilder().setRequestType(type).setRequestId(4).setRequestMoveFileData(RestoreToolIpc.RequestMoveFileData.newBuilder().setSourcePath(root.resolve("one").toString()).setTargetPath(external.resolve("two").toString())).build();
    try{send.invoke(proxy,request);throw new AssertionError("outside target reached IPC for "+type);}catch(InvocationTargetException e){if(!(e.getCause() instanceof SecurityException))throw e;}
   }
   var metadata=RestoreToolIpc.RequestMessage.newBuilder().setRequestType(RestoreToolIpc.RequestType.WRITE_METADATA).setRequestId(5).setRequestWriteMetadata(RestoreToolIpc.RequestWriteMetadata.newBuilder().setPath(external.toString()).setWritePermissions(true)).build();
   try{send.invoke(proxy,metadata);throw new AssertionError("outside metadata reached IPC");}catch(InvocationTargetException e){if(!(e.getCause() instanceof SecurityException))throw e;}
   // Link text is data. Only the link object and its real parents are admitted.
   var linkObject=RestoreToolIpc.RequestMessage.newBuilder().setRequestType(RestoreToolIpc.RequestType.MAKE_SYMLINK).setRequestId(6).setRequestMoveFileData(RestoreToolIpc.RequestMoveFileData.newBuilder().setSourcePath("../archived-target").setTargetPath(root.resolve("archived-link").toString())).build();
   send.invoke(proxy,linkObject);
   var linkMetadata=metadata.toBuilder().setRequestId(7).setRequestWriteMetadata(RestoreToolIpc.RequestWriteMetadata.newBuilder().setPath(root.resolve("link").toString()).setWritePermissions(true).setLinuxMetadata(RestoreToolIpc.LinuxMetadata.newBuilder().setUid(0).setGid(0).setCtime(0).setMtime(0).setMode(0120777))).build();
   send.invoke(proxy,linkMetadata);
   before=writes.get();
   var throughParent=linkObject.toBuilder().setRequestMoveFileData(RestoreToolIpc.RequestMoveFileData.newBuilder().setSourcePath("relative").setTargetPath(root.resolve("link/child").toString())).build();
   try{send.invoke(proxy,throughParent);throw new AssertionError("link creation traversed a linked parent");}catch(InvocationTargetException e){if(!(e.getCause() instanceof SecurityException))throw e;}
   var regularMetadata=linkMetadata.toBuilder().setRequestWriteMetadata(linkMetadata.getRequestWriteMetadata().toBuilder().setLinuxMetadata(RestoreToolIpc.LinuxMetadata.newBuilder().setUid(0).setGid(0).setCtime(0).setMtime(0).setMode(0100644))).build();
   try{send.invoke(proxy,regularMetadata);throw new AssertionError("regular metadata followed a linked leaf");}catch(InvocationTargetException e){if(!(e.getCause() instanceof SecurityException))throw e;}
   Files.move(root,root.resolveSibling(root.getFileName()+"-old"));Files.createDirectory(root);
   try{send.invoke(proxy,inside);throw new AssertionError("replaced root reached IPC");}catch(InvocationTargetException e){if(!(e.getCause() instanceof SecurityException))throw e;}
   if(writes.get()!=before)throw new AssertionError("rejected later request wrote IPC");
   System.out.println("RestoreBoundaryTest PASS: cached proxy per-operation admission");
  } finally {executor.shutdownNow();}
 }
 private RestoreBoundaryTest(){}
}
