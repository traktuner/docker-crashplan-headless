package cph;

import com.code42.backup.handler.restoretool.RestoreToolProxy;
import com.code42.backup.handler.restoretool.RestoreToolException;
import com.code42.os.pipe.IPipe;
import com.code42.restoretool.ipc.RestoreToolIpc;
import java.io.IOException;
import java.lang.reflect.Proxy;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicReference;

/** Captures the actual native serialized request before helper transport. */
public final class RestoreLinkSerializationTest {
    public static void main(String[] args) throws Exception {
        Path base=Files.createTempDirectory("link-serialization-");
        Path root=Files.createDirectory(base.resolve("root"));
        System.setProperty("cph.restore.roots",Base64.getEncoder().encodeToString(root.toString().getBytes(StandardCharsets.UTF_8)));
        var captured=new AtomicReference<RestoreToolIpc.RequestMessage>();
        IPipe pipe=(IPipe)Proxy.newProxyInstance(IPipe.class.getClassLoader(),new Class<?>[]{IPipe.class},(proxy,method,values)->{
            if(method.getName().equals("write")){
                byte[] bytes=(byte[])values[0];
                if(bytes.length!=4){captured.set(RestoreToolIpc.RequestMessage.parseFrom(bytes));throw new IOException("fixture transport stops after capture");}
                return bytes.length;
            }
            if(method.getReturnType()==int.class)return 0;
            if(method.getReturnType()==boolean.class)return false;
            return null;
        });
        var executor=Executors.newSingleThreadScheduledExecutor();
        try {
            var proxy=new RestoreToolProxy(pipe,executor);
            for(String target:new String[]{"../relative",root.resolve("internal").toString(),base.resolve("external").toString()}) {
                captured.set(null);
                try{proxy.symlink(target,root.resolve("link").toString());}catch(IOException | RestoreToolException expected){}
                var message=captured.get();
                if(message==null||!message.getRequestMoveFileData().getSourcePath().equals(target))throw new AssertionError("native IPC changed archived link text");
            }
            System.out.println("RestoreLinkSerializationTest PASS");
        }finally{executor.shutdownNow();}
    }
    private RestoreLinkSerializationTest(){}
}
