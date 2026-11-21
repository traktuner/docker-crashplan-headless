package cph;

import java.lang.classfile.ClassFile;
import java.lang.classfile.ClassHierarchyResolver;
import java.lang.classfile.ClassModel;
import java.lang.classfile.ClassTransform;
import java.lang.classfile.CodeBuilder;
import java.lang.classfile.CodeModel;
import java.lang.classfile.CodeTransform;
import java.lang.constant.ClassDesc;
import java.lang.constant.MethodTypeDesc;
import java.lang.classfile.MethodModel;
import java.lang.classfile.instruction.InvokeInstruction;
import java.lang.classfile.instruction.ConstantInstruction;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;

/**
 * Bytecode patches for the CrashPlan service.
 *
 * <p>Every patch selects exactly one method, named by its class,
 * method name and descriptor. It replaces, prefixes or transforms that method.
 * If that method is missing (for example after a
 * CrashPlan update), applying the patch throws. The image build runs
 * {@link SelfTest} against the shipped jars, so such a mismatch fails the CI
 * build instead of the running service.
 */
public final class Patches {

    /** One verified method transformation. */
    public record Patch(String name, String description, String className,
                        String methodName, String descriptor, Consumer<CodeBuilder> body, boolean prepend, CodeTransform codeTransform) {
        public Patch(String name, String description, String className, String methodName, String descriptor, Consumer<CodeBuilder> body, boolean prepend) {
            this(name, description, className, methodName, descriptor, body, prepend, null);
        }
        public Patch(String name, String description, String className, String methodName, String descriptor, Consumer<CodeBuilder> body) {
            this(name, description, className, methodName, descriptor, body, false, null);
        }

        byte[] apply(byte[] classBytes) {
            ClassFile cf = ClassFile.of(ClassFile.ClassHierarchyResolverOption.of(HIERARCHY));
            ClassModel model = cf.parse(classBytes);
            cph.archive.ArchivePatches.validateFields(model, this);
            long matches = model.methods().stream().filter(this::matches).count();
            if (matches != 1) {
                throw new IllegalStateException("patch '" + name + "': expected exactly one method "
                        + className + "." + methodName + descriptor + ", found " + matches);
            }
            ClassTransform transform = ClassTransform.transformingMethods(this::matches, (mb, me) -> {
                if (me instanceof CodeModel code) {
                    if (codeTransform != null) mb.transformCode(code, codeTransform);
                    else if (prepend) mb.transformCode(code, new CodeTransform() {
                        @Override public void atStart(CodeBuilder cb) { body.accept(cb); }
                        @Override public void accept(CodeBuilder cb, java.lang.classfile.CodeElement element) { cb.with(element); }
                    });
                    else mb.withCode(body);
                } else {
                    mb.with(me);
                }
            });
            return cf.transformClass(model, transform);
        }

        private boolean matches(MethodModel m) {
            return m.methodName().equalsString(methodName) && m.methodType().equalsString(descriptor);
        }
    }

    private static final Map<String, Patch> ALL = new LinkedHashMap<>();

    // Stack maps need the supertypes of the classes a patched method uses. The
    // default resolver loads those classes, and a class loaded inside a
    // transformer is not transformed again, so a patch for it would be skipped.
    // Reading the class files keeps the load order irrelevant.
    private static final ClassHierarchyResolver HIERARCHY = ClassHierarchyResolver
            .ofResourceParsing(ClassLoader.getSystemClassLoader())
            .orElse(ClassHierarchyResolver.defaultResolver()).cached();

    static {
        cph.archive.ArchivePatches.register(Patches::register);
        // Preserve native query/error metadata in private loopback responses.
        // The Go boundary projects only validated public fields.
        for (String result : new String[]{"GetFileContentsResultMessage", "GetAllVersionsResultMessage"}) {
            register(new Patch("archive-query-" + result, "retain private archive query identity and errors",
                "com/backup42/service/ui/message/" + result, "toJson", "()Ljava/lang/String;",
                null, false, CodeTransform.ofStateful(() -> new CodeTransform() {
                    private int matches;
                    @Override public void accept(CodeBuilder cb, java.lang.classfile.CodeElement element) {
                        if (element instanceof ConstantInstruction value && value.constantValue().equals("backupQueryData")) {
                            matches++;
                            cb.ldc("cphNoExcludedQueryField");
                        } else cb.with(element);
                    }
                    @Override public void atEnd(CodeBuilder cb) {
                        if (matches != 1) throw new IllegalStateException("native query serialization changed");
                    }
                })));
        }
        register(new Patch("restore-boundary", "admit every restore helper IPC filesystem request",
            "com/code42/backup/handler/restoretool/RestoreToolProxy", "sendRequest",
            "(Lcom/code42/restoretool/ipc/RestoreToolIpc$RequestMessage;)V",
            cb -> cb.aload(1).invokestatic(ClassDesc.of("cph.RestoreBoundary"), "check",
                MethodTypeDesc.ofDescriptor("(Ljava/lang/Object;)V")), true));

        // Preserve the archived link text before the shared IPC builder turns
        // File paths into absolute paths. Move/copy retain their native rules.
        register(new Patch("restore-symlink-text", "preserve archived symlink contents through helper IPC",
            "com/code42/backup/handler/restoretool/RestoreToolProxy", "sendMoveCopyOrSymlinkFileRequest",
            "(Lcom/code42/restoretool/ipc/RestoreToolIpc$RequestType;Ljava/io/File;Ljava/io/File;)Lcom/code42/utils/Pair;",
            null, false, CodeTransform.ofStateful(() -> new CodeTransform() {
                private int matches;
                @Override public void accept(CodeBuilder cb, java.lang.classfile.CodeElement element) {
                    if (element instanceof InvokeInstruction call && call.owner().asInternalName().equals("java/io/File")
                            && call.name().equalsString("getAbsolutePath") && call.type().equalsString("()Ljava/lang/String;")) {
                        matches++;
                        cb.aload(1).invokestatic(ClassDesc.of("cph.RestoreBoundary"), "ipcPath",
                            MethodTypeDesc.ofDescriptor("(Ljava/io/File;Lcom/code42/restoretool/ipc/RestoreToolIpc$RequestType;)Ljava/lang/String;"));
                    } else cb.with(element);
                }
                @Override public void atEnd(CodeBuilder cb) {
                    if (matches != 5) throw new IllegalStateException("native IPC path serialization changed");
                }
            })));

        String direct = "com/code42/backup/handler/restoretool/RestoreFileUtilityService";
        for (String method : new String[]{"mkDir", "createRestoreFile"}) {
            register(new Patch("restore-direct-"+method, "admit direct restore filesystem operation", direct,
                method, "(Ljava/io/File;)V", cb -> checkFile(cb, 1), true));
        }
        register(new Patch("restore-direct-ensurePath", "admit direct restore path creation", direct,
            "ensureRestorePath", "(Ljava/io/File;Z)V", cb -> checkFile(cb, 1), true));
        for (String method : new String[]{"moveRestoreFile", "copyRestoreFile"}) {
            register(new Patch("restore-direct-"+method, "admit both direct restore paths", direct,
                method, "(Ljava/io/File;Ljava/io/File;)V", cb -> cb.aload(1).aload(2).invokestatic(
                    ClassDesc.of("cph.RestoreBoundary"), method.equals("copyRestoreFile") ? "copyFiles" : "moveFiles", MethodTypeDesc.ofDescriptor("(Ljava/io/File;Ljava/io/File;)V")), true));
        }
        register(new Patch("restore-direct-symlink", "admit direct restore link paths", direct,
            "symlinkRestoreFile", "(Ljava/lang/String;Ljava/lang/String;)V", cb -> cb.aload(1).aload(2).invokestatic(
                ClassDesc.of("cph.RestoreBoundary"), "symlink", MethodTypeDesc.ofDescriptor("(Ljava/lang/String;Ljava/lang/String;)V")), true));
        register(new Patch("restore-direct-metadata", "admit direct restore metadata target", direct,
            "applyMetadata", "(Lcom/code42/backup/restore/RestoreJob;Lcom/code42/os/metadata/IMetadataServices;Lcom/code42/os/metadata/MetadataItem;)V",
            cb -> cb.aload(3).invokestatic(ClassDesc.of("cph.RestoreBoundary"), "metadata",
                MethodTypeDesc.ofDescriptor("(Lcom/code42/os/metadata/MetadataItem;)V")), true));

        register(new Patch("restore-compressed-output", "admit inherited decompression input and output",
            "com/code42/backup/handler/strategy/CompressFirstRestoreStrategy", "replace", "(Ljava/io/File;Ljava/io/File;Z)V",
            cb -> cb.aload(1).aload(2).invokestatic(ClassDesc.of("cph.RestoreBoundary"), "files",
                MethodTypeDesc.ofDescriptor("(Ljava/io/File;Ljava/io/File;)V")), true));

        // Each version-reduction point the destination sends makes the native
        // maintenance job read the file manifest from its first entry up to the
        // point's start entry. ReduceResume continues behind the last entry of
        // the previous pass instead, after checking it against the manifest index.
        register(new Patch("reduce-resume", "resume maintenance file passes behind the checked last entry",
            "com/code42/backup/manifest/source/SourceMaintenanceJob", "processFiles",
            "(Lcom/code42/backup/manifest/AgentManifest;Lcom/code42/backup/manifest/BackupArchiveProperties;"
                + "Lcom/code42/backup/manifest/BackupArchiveProperties$ReduceState;)V",
            null, false, CodeTransform.ofStateful(() -> new CodeTransform() {
                private int matches;
                @Override public void accept(CodeBuilder cb, java.lang.classfile.CodeElement element) {
                    if (element instanceof InvokeInstruction call
                            && call.owner().asInternalName().equals("com/code42/backup/manifest/file/FileManifest")
                            && call.name().equalsString("fileManifestEntryIterator")
                            && call.type().equalsString("(Z)Lcom/code42/io/IOIterator;")) {
                        matches++;
                        cb.aload(0).getfield(ClassDesc.ofInternalName("com/code42/backup/manifest/source/SourceMaintenanceJob"),
                                "startEntryNum", java.lang.constant.ConstantDescs.CD_int)
                            .invokestatic(ClassDesc.of("cph.ReduceResume"), "iterator", MethodTypeDesc.ofDescriptor(
                                "(Lcom/code42/backup/manifest/file/FileManifest;ZI)Lcom/code42/io/IOIterator;"));
                    } else cb.with(element);
                }
                @Override public void atEnd(CodeBuilder cb) {
                    if (matches != 1) throw new IllegalStateException("native maintenance file loop changed");
                }
            })));

        // Repair can renumber entries without moving them, and compaction,
        // truncation, clearing and deletion rewrite the manifest. Each of them
        // drops every saved resume entry first.
        String fmfClass = "com/code42/backup/manifest/file/FileManifest";
        String[][] rewrites = {
            {"repair", "(Lcom/code42/throttle/IThrottlerInstance;)I"},
            {"compact", "(Lcom/code42/backup/manifest/file/CompactFileManifestRule;Lcom/code42/io/ProgressControl;Z)V"},
            {"truncate", "(J)V"},
            {"clear", "()V"},
            {"delete", "()Z"},
        };
        for (String[] method : rewrites) {
            register(new Patch("reduce-resume-forget-" + method[0], "drop saved resume entries before the manifest is rewritten",
                fmfClass, method[0], method[1],
                cb -> cb.invokestatic(ClassDesc.of("cph.ReduceResume"), "forget", MethodTypeDesc.ofDescriptor("()V")), true));
        }

        // A new source block silo restarts its new-block counter from its own
        // last saved block and hands out numbers the previous silo has already
        // handed out. BlockRollover continues the previous counter instead.
        register(new Patch("block-rollover", "continue new block numbers across a source block silo change",
            "com/code42/backup/manifest/source/SourceBlockSiloManager", "startAnotherBlockManifest", "(J)V",
            null, false, CodeTransform.ofStateful(() -> new CodeTransform() {
                private int returns;
                @Override public void accept(CodeBuilder cb, java.lang.classfile.CodeElement element) {
                    if (element instanceof java.lang.classfile.instruction.ReturnInstruction) {
                        returns++;
                        cb.aload(0).lload(1).invokestatic(ClassDesc.of("cph.BlockRollover"), "carry", MethodTypeDesc.ofDescriptor(
                            "(Lcom/code42/backup/manifest/source/SourceBlockSiloManager;J)V"));
                    }
                    cb.with(element);
                }
                @Override public void atEnd(CodeBuilder cb) {
                    if (returns != 1) throw new IllegalStateException("native silo start changed");
                }
            })));

        // All inotify watches of both backup engines (V1 BackupSetsManager and
        // V3 FileActivityService) go through InotifyManager.watch(File). Reporting
        // success without calling inotify_add_watch leaves the kernel without any
        // watches. Changes are then found by the scheduled verification scan only,
        // which is what the old "watchFiles=false" setting did.
        register(new Patch(
                "no-file-watcher",
                "skip all inotify watches; changes are found by the verification scan",
                "com/code42/jna/inotify/InotifyManager",
                "watch",
                "(Ljava/io/File;)Z",
                cb -> cb.iconst_1().ireturn()));

        // Suppressing watch() alone still lets the registration worker enumerate
        // every readable descendant. That extra walk loads filesystem metadata.
        // Use with no-file-watcher for scan-only discovery; the actual backup
        // verification worker is a different class and stays unchanged.
        register(new Patch(
                "no-watcher-walk",
                "skip the recursive watcher-registration walk",
                "com/code42/jna/inotify/JNAInotifyWorker",
                "run",
                "()V",
                cb -> cb.return_()));
    }

    private static void checkFile(CodeBuilder cb, int argument) {
        cb.aload(argument).invokestatic(ClassDesc.of("cph.RestoreBoundary"), "file", MethodTypeDesc.ofDescriptor("(Ljava/io/File;)V"));
    }
    private static void register(Patch p) {
        ALL.put(p.name(), p);
    }

    public static Map<String, Patch> all() {
        return ALL;
    }

    /** Resolves a comma-separated list such as "no-file-watcher". Unknown names throw. */
    public static List<Patch> select(String spec) {
        List<Patch> out = new ArrayList<>();
        if (spec == null || spec.isBlank() || spec.equalsIgnoreCase("none")) {
            return out;
        }
        for (String raw : spec.split(",")) {
            String name = raw.trim();
            if (name.isEmpty()) {
                continue;
            }
            Patch p = ALL.get(name);
            if (p == null) {
                throw new IllegalArgumentException("unknown patch '" + name + "'; known: " + ALL.keySet());
            }
            out.add(p);
        }
        return out;
    }

    private Patches() {
    }
}
