package cph;

import java.io.DataInputStream;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.lang.reflect.Proxy;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

/** Runs the actual packaged helper without starting a backup engine. */
public final class RestoreHelperTest {
    @SuppressWarnings("try") // IPipe.close must close the same owned streams.
    public static void main(String[] args) throws Exception {
        Path base = Files.createTempDirectory("restore-helper-");
        Path root = Files.createDirectory(base.resolve("admitted"));
        Path request = root.resolve("request"), response = root.resolve("response");
        System.setProperty("cph.restore.roots", Base64.getEncoder().encodeToString(root.toString().getBytes(StandardCharsets.UTF_8)));
        if (new ProcessBuilder("mkfifo", "-m", "600", request.toString(), response.toString()).start().waitFor() != 0) {
            throw new AssertionError("fixture FIFO creation failed");
        }
        Process helper = new ProcessBuilder("/usr/local/crashplan/bin/restore-tool", "-userName", "root", "-logDir", root.toString(), request.toString(), response.toString())
            .redirectOutput(root.resolve("startup.log").toFile()).redirectError(root.resolve("stderr.log").toFile()).start();
        var executor = Executors.newSingleThreadScheduledExecutor();
        int failures = 0;
        try (var output = new FileOutputStream(request.toFile());
             var input = new DataInputStream(new FileInputStream(response.toFile()))) {
            Class<?> pipeType = Class.forName("com.code42.os.pipe.IPipe");
            Object pipe = Proxy.newProxyInstance(pipeType.getClassLoader(), new Class<?>[]{pipeType}, (proxy, method, values) -> {
                return switch (method.getName()) {
                    case "getUserName" -> "root";
                    case "getRequestPath" -> request.toString();
                    case "getResponsePath" -> response.toString();
                    case "read" -> { byte[] bytes = (byte[]) values[0]; input.readFully(bytes); yield bytes.length; }
                    case "write" -> { byte[] bytes = (byte[]) values[0]; output.write(bytes); output.flush(); yield bytes.length; }
                    case "available" -> input.available();
                    case "close" -> { output.close(); input.close(); yield null; }
                    default -> null;
                };
            });
            Class<?> proxyType = Class.forName("com.code42.backup.handler.restoretool.RestoreToolProxy");
            Object proxy = proxyType.getConstructor(pipeType, java.util.concurrent.ScheduledExecutorService.class).newInstance(pipe, executor);
            String[] targets = {"../relative-target", root.resolve("internal-target").toString(), base.resolve("external-target").toString()};
            for (int index = 0; index < targets.length; index++) {
                Path link = root.resolve("link-" + index);
                proxyType.getMethod("symlink", String.class, String.class).invoke(proxy, targets[index], link.toString());
                if (!Files.readSymbolicLink(link).toString().equals(targets[index])) failures++;
            }
            proxyType.getMethod("terminateGracefully").invoke(proxy);
        } finally {
            executor.shutdownNow();
            if (!helper.waitFor(2, TimeUnit.SECONDS)) {
                helper.destroyForcibly();
                helper.waitFor();
            }
        }
        if (failures != 0) throw new AssertionError("helper changed archived symlink text: " + failures);
        if (helper.exitValue() != 0) throw new AssertionError("helper did not exit cleanly");
        System.out.println("RestoreHelperTest PASS: real helper preserves relative, internal and external symlink text");
    }
    private RestoreHelperTest() { }
}
