package cph;

import com.code42.os.file.FileStat;
import com.code42.os.metadata.MetadataItem;
import com.code42.os.metadata.ResourceFile;
import com.code42.restoretool.ipc.RestoreToolIpc;
import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.attribute.BasicFileAttributes;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.Map;

/** Operation-specific restore path admission. Parents never follow links.
 * Link creation, rename and link metadata operate on the leaf object itself.
 * Operators must control roots; this does not sandbox hostile concurrent races.
 */
public final class RestoreBoundary {
    private static final Map<Path, Object> ROOTS = new LinkedHashMap<>();
    private static boolean initialized;

    public static synchronized void check(Object message) {
        if (!(message instanceof RestoreToolIpc.RequestMessage)) deny();
        var request = (RestoreToolIpc.RequestMessage) message;
        if (!request.hasRequestType()) deny();
        switch (request.getRequestType()) {
            case KEEP_ALIVE, TERMINATE_GRACEFULLY -> { return; }
            case MAKE_FILE, MAKE_DIR -> {
                if (!request.hasRequestMakePathData()) deny();
                path(request.getRequestMakePathData().getRestorePath(), false);
            }
            case COPY_FILE -> {
                if (!request.hasRequestMoveFileData()) deny();
                var data = request.getRequestMoveFileData();
                copyFiles(new File(data.getSourcePath()), new File(data.getTargetPath()));
            }
            case MOVE_FILE -> {
                if (!request.hasRequestMoveFileData()) deny();
                var data = request.getRequestMoveFileData();
                moveFiles(new File(data.getSourcePath()), new File(data.getTargetPath()));
            }
            case MAKE_SYMLINK -> {
                if (!request.hasRequestMoveFileData()) deny();
                var data = request.getRequestMoveFileData();
                symlink(data.getSourcePath(), data.getTargetPath());
            }
            case WRITE_METADATA -> {
                if (!request.hasRequestWriteMetadata()) deny();
                var data = request.getRequestWriteMetadata();
                boolean link = data.hasLinuxMetadata() && (data.getLinuxMetadata().getMode() & 0170000) == 0120000;
                path(data.getPath(), link);
            }
            default -> deny();
        }
    }

    public static synchronized void file(File file) { file(file, false); }

    private static void file(File file, boolean linkObject) {
        if (file == null) deny();
        path(file.getPath(), linkObject);
        if (file instanceof ResourceFile resource) {
            File actual = resource.getActualFile();
            if (actual == null) deny();
            path(actual.getPath(), linkObject);
        }
    }

    public static synchronized void files(File source, File target) {
        file(source);
        file(target);
    }

    public static synchronized void moveFiles(File source, File target) {
        file(source, true);
        file(target, true);
    }

    public static synchronized void copyFiles(File source, File target) {
        files(source, target);
        // Native copyFile(directory) writes to directory/source.name.
        if (target.isDirectory()) file(new File(target, source.getName()));
    }

    public static synchronized void symlink(String contents, String link) {
        // The target string is archived link data, not a path to read or write.
        if (contents == null || contents.indexOf('\0') >= 0) deny();
        path(link, true);
    }

    public static String ipcPath(File file, RestoreToolIpc.RequestType type) {
        return type == RestoreToolIpc.RequestType.MAKE_SYMLINK ? file.getPath() : file.getAbsolutePath();
    }

    public static synchronized void metadata(MetadataItem item) {
        if (item == null) deny();
        file(item.getOutputFile(), FileStat.isSymlink(item.getFileType()));
    }

    private static void initialize() {
        if (initialized) return;
        String encoded = System.getProperty("cph.restore.roots", System.getenv("CPH_RESTORE_ROOTS_B64"));
        Map<Path, Object> roots = new LinkedHashMap<>();
        if (encoded != null && !encoded.isEmpty()) {
            String[] entries = encoded.split(":", -1);
            if (entries.length > 128) deny();
            for (String entry : entries) {
                Path root = Path.of(new String(Base64.getDecoder().decode(entry), StandardCharsets.UTF_8));
                if (!root.isAbsolute() || !root.normalize().equals(root) || root.getParent() == null || roots.containsKey(root)) deny();
                // Availability and identity are checked independently on first use.
                roots.put(root, null);
            }
        }
        ROOTS.putAll(roots);
        initialized = true;
    }

    private static void path(String value, boolean linkObject) {
        try {
            initialize();
            Path path = Path.of(value);
            if (!path.isAbsolute() || !path.normalize().equals(path)) deny();
            Path selected = null;
            for (Path root : ROOTS.keySet()) {
                if (path.startsWith(root) && (selected == null || root.getNameCount() > selected.getNameCount())) selected = root;
            }
            if (selected == null) deny();
            walk(selected, false, false);
            var attrs = Files.readAttributes(selected, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
            if (!attrs.isDirectory() || attrs.fileKey() == null) deny();
            Object pinned = ROOTS.get(selected);
            if (pinned == null) ROOTS.put(selected, attrs.fileKey());
            else if (!pinned.equals(attrs.fileKey())) deny();
            walk(path, true, linkObject);
        } catch (IOException | IllegalArgumentException e) {
            deny();
        }
    }

    private static void walk(Path path, boolean allowMissing, boolean linkObject) throws IOException {
        Path current = path.getRoot();
        for (Path component : path) {
            current = current.resolve(component);
            BasicFileAttributes attrs;
            try {
                attrs = Files.readAttributes(current, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
            } catch (java.nio.file.NoSuchFileException e) {
                if (allowMissing) return;
                throw e;
            }
            if (attrs.isSymbolicLink() && !(linkObject && current.equals(path))) deny();
        }
    }

    private static void deny() { throw new SecurityException("RESTORE_DESTINATION_REJECTED"); }
    private RestoreBoundary() {}
}
