package cph.archive;

import cph.Patches.Patch;
import java.lang.classfile.CodeBuilder;
import java.lang.classfile.CodeElement;
import java.lang.classfile.CodeTransform;
import java.lang.classfile.instruction.FieldInstruction;
import java.lang.classfile.instruction.InvokeInstruction;
import java.lang.constant.ClassDesc;
import java.lang.constant.MethodTypeDesc;
import java.util.function.Consumer;

/** Version-checked integration with the native archive owners. */
public final class ArchivePatches {
    private static final String QUERY = "Lcom/code42/backup/restore/BackupQueryData;";
    private static final String CONTEXT = "com/code42/service/app/SearchFileVersionsContext";
    private static final String SEARCH = "com/code42/service/app/SearchFileVersionsV3Context";
    private static final ClassDesc SNAPSHOT = ClassDesc.of("cph.archive.QuerySnapshot");

    public static void register(Consumer<Patch> add) {
        registerRequests(add);
        registerResponses(add);
        add.accept(new Patch("archive-query-search-snapshot", "retain immutable search query values", CONTEXT,
            "<init>", "(" + QUERY + "Lcom/code42/archive/key/IArchiveKey;)V", null, false,
            CodeTransform.ofStateful(() -> new CodeTransform() {
                int fields;
                @Override public void accept(CodeBuilder cb, CodeElement element) {
                    if (element instanceof FieldInstruction field && field.name().equalsString("backupQueryData")
                            && field.opcode() == java.lang.classfile.Opcode.PUTFIELD) {
                        fields++;
                        cb.invokestatic(SNAPSHOT, "snapshot", MethodTypeDesc.ofDescriptor("("+QUERY+")"+QUERY));
                    }
                    cb.with(element);
                }
                @Override public void atEnd(CodeBuilder cb) { require(fields, 1); }
            })));
        add.accept(new Patch("archive-query-search-mode", "retain native search expression and mode", SEARCH,
            "startFileVersionsSearch", "("+QUERY+"Ljava/lang/String;Z)V",
            cb -> cb.aload(0).getfield(ClassDesc.ofInternalName(CONTEXT), "backupQueryData", ClassDesc.ofDescriptor(QUERY))
                .aload(2).iload(3).invokestatic(SNAPSHOT, "configureSearch",
                    MethodTypeDesc.ofDescriptor("("+QUERY+"Ljava/lang/String;Z)V")), true));
        for (String event : new String[]{"CreateSearchArchiveJobEvent", "GetSearchArchiveJobEvent"}) {
            add.accept(new Patch("archive-query-search-"+event, "project search query after native GUID check", SEARCH,
                "handleEvent", "(Lcom/code42/backup/event/query/search/"+event+
                    ";)Lcom/backup42/service/ui/message/SearchFileVersionsResultMessage;", null, false,
                CodeTransform.ofStateful(() -> new CodeTransform() {
                    int queries, guids, constructors;
                    @Override public void atStart(CodeBuilder cb) {
                        var active = cb.newLabel();
                        var unfinished = cb.newLabel();
                        cb.aload(0).invokevirtual(ClassDesc.ofInternalName(CONTEXT), "isFinished", MethodTypeDesc.ofDescriptor("()Z"))
                            .ifeq(unfinished).aconst_null().areturn().labelBinding(unfinished)
                            .aload(0).getfield(ClassDesc.ofInternalName(CONTEXT), "backupQueryData", ClassDesc.ofDescriptor(QUERY))
                            .invokestatic(SNAPSHOT, "searchFinished", MethodTypeDesc.ofDescriptor("("+QUERY+")Z"))
                            .ifeq(active).aconst_null().areturn().labelBinding(active);
                    }
                    @Override public void accept(CodeBuilder cb, CodeElement element) {
                        if (event.startsWith("Create") && element instanceof java.lang.classfile.instruction.ReturnInstruction returned
                                && returned.opcode() == java.lang.classfile.Opcode.ARETURN) {
                            var absent = cb.newLabel();
                            cb.dup().ifnull(absent).aload(0).invokevirtual(ClassDesc.ofInternalName(CONTEXT), "setFinished", MethodTypeDesc.ofDescriptor("()V"))
                                .labelBinding(absent);
                        }
                        cb.with(element);
                        if (element instanceof InvokeInstruction call) {
                            if (call.owner().asInternalName().equals("com/code42/keys/MessageRequestGuid") && call.name().equalsString("equals")) guids++;
                            if (call.owner().asInternalName().equals("com/backup42/service/ui/message/SearchFileVersionsResultMessage") && call.name().equalsString("<init>")) constructors++;
                            if (call.name().equalsString("getBackupQueryData") && call.owner().asInternalName().endsWith("/"+event)) {
                                queries++;
                                if (queries == (event.startsWith("Create") ? 2 : 3)) {
                                    cb.aload(0).getfield(ClassDesc.ofInternalName(CONTEXT), "backupQueryData", ClassDesc.ofDescriptor(QUERY))
                                        .aload(0).getfield(ClassDesc.ofInternalName(SEARCH), "requestGuid", ClassDesc.of("com.code42.keys.MessageRequestGuid"))
                                        .invokestatic(SNAPSHOT, "completeSearch", MethodTypeDesc.ofDescriptor("("+QUERY+QUERY+"Lcom/code42/keys/MessageRequestGuid;)"+QUERY));
                                }
                            }
                        }
                    }
                    @Override public void atEnd(CodeBuilder cb) {
                        require(queries, event.startsWith("Create") ? 2 : 3); require(guids, 1); require(constructors, 1);
                    }
                })));
        }
    }

    private static void registerResponses(Consumer<Patch> add) {
        String agent = "com/code42/engine/v3/BackupClientV3MessagingAgent";
        for (String[] response : new String[][]{
                {"children", "com/code42/protos/shared/authority/PlanAdministrationMessages$GetPlanFileChildrenResponse"},
                {"versions", "com/code42/protos/shared/authority/PlanAdministrationMessages$GetPlanFileVersionsResponse"},
                {"counts", "com/code42/protos/shared/RestoreMessages$FileTreeMetadataResponse"}}) {
            String descriptor = "(Lcom/code42/messaging/ProtoWrapper;L"+response[1]+";)V";
            add.accept(new Patch("archive-query-response-"+response[0], "validate exact native response before projection", agent,
                "receiveMessage", descriptor, null, false, CodeTransform.ofStateful(() -> new CodeTransform() {
                    int contexts, usages, queries, errors, executors;
                    @Override public void atStart(CodeBuilder cb) {
                        cb.aload(0).getfield(ClassDesc.ofInternalName(agent), "socialNetwork", ClassDesc.of("com.backup42.service.model.SocialNetworkModel"))
                            .aload(0).getfield(ClassDesc.ofInternalName(agent), "handler", ClassDesc.of("com.code42.engine.v3.IBackupClientV3MessageHandler"))
                            .aload(0).getfield(ClassDesc.ofInternalName(agent), "msgReceiptExecutor", ClassDesc.of("com.code42.service.MessageReceiptExecutor"))
                            .aload(1).aload(2).invokestatic(ClassDesc.of("cph.archive.ArchiveResponses"), response[0],
                                MethodTypeDesc.ofDescriptor("(Lcom/backup42/service/model/SocialNetworkModel;Lcom/code42/engine/v3/IBackupClientV3MessageHandler;Lcom/code42/service/MessageReceiptExecutor;Lcom/code42/messaging/ProtoWrapper;L"+response[1]+";)V"))
                            .return_();
                    }
                    @Override public void accept(CodeBuilder cb, CodeElement element) {
                        if (element instanceof InvokeInstruction call) {
                            if (call.owner().asInternalName().equals(agent) && call.name().equalsString("getBackupIdFromContext")) contexts++;
                            if (call.owner().asInternalName().equals(agent) && call.name().equalsString("getUsageByPlanUidAndDestination")) usages++;
                            if (call.owner().asInternalName().equals("com/code42/backup/restore/BackupQueryData")
                                    && call.name().equalsString("<init>") && call.type().equalsString("(JJLcom/code42/io/path/FileId;Lcom/code42/io/path/FileId;)V")) queries++;
                            if (call.name().equalsString("setErrorCode")) errors++;
                            if (call.owner().asInternalName().equals("com/code42/service/MessageReceiptExecutor") && call.name().equalsString("onReceiveMessage")) executors++;
                        }
                    }
                    @Override public void atEnd(CodeBuilder cb) {
                        require(contexts, 1); require(usages, 1); require(queries, 1); require(errors, 1); require(executors, 1);
                    }
                })));
        }
        add.accept(new Patch("archive-query-children-next", "separate requested cursor from next cursor",
            "com/code42/engine/v3/BackupClientV3MessageHandler", "onGetPlanFileChildrenResponse",
            "(JJ"+QUERY+"Lcom/code42/backup/manifest/SecureFileVersionSet;)V", null, false,
            CodeTransform.ofStateful(() -> new CodeTransform() {
                int cursors, constructors, next;
                @Override public void accept(CodeBuilder cb, CodeElement element) {
                    if (element instanceof InvokeInstruction call) {
                        if (call.name().equalsString("<init>") && call.owner().asInternalName().equals("com/code42/backup/event/query/ChildrenFileVersionsEvent")) constructors++;
                        if (call.name().equalsString("setNextBatchFileId")) next++;
                        if (call.name().equalsString("getBatchLastFileId") && call.owner().asInternalName().equals("com/code42/backup/restore/BackupQueryData")
                                && call.type().equalsString("()Lcom/code42/io/path/FileId;")) {
                            cursors++;
                            cb.invokestatic(SNAPSHOT, "nextCursor", MethodTypeDesc.ofDescriptor("("+QUERY+")Lcom/code42/io/path/FileId;"));
                            return;
                        }
                    }
                    cb.with(element);
                }
                @Override public void atEnd(CodeBuilder cb) { require(cursors, 1); require(constructors, 1); require(next, 1); }
            })));
    }

    public static void validateFields(java.lang.classfile.ClassModel model, Patch patch) {
        if (!patch.name().startsWith("archive-query-response-")) return;
        for (var field : java.util.Map.of("socialNetwork", "Lcom/backup42/service/model/SocialNetworkModel;",
                "handler", "Lcom/code42/engine/v3/IBackupClientV3MessageHandler;", "msgReceiptExecutor", "Lcom/code42/service/MessageReceiptExecutor;").entrySet())
            require((int) model.fields().stream().filter(value -> value.fieldName().equalsString(field.getKey()) && value.fieldType().equalsString(field.getValue())).count(), 1);
    }

    private static void registerRequests(Consumer<Patch> add) {
        String client = "com/code42/engine/v3/BackupClientV3";
        ClassDesc requests = ClassDesc.of("cph.archive.ArchiveRequests");
        for (String method : new String[]{"getChildrenFileVersions", "getAllVersions", "getFileContents"}) {
            add.accept(new Patch("archive-query-send-"+method, "bind native archive request context", client,
                method, "("+QUERY+")V", null, false, CodeTransform.ofStateful(() -> new CodeTransform() {
                    int sends, factories, missing;
                    @Override public void accept(CodeBuilder cb, CodeElement element) {
                        if (element instanceof InvokeInstruction call) {
                            if (call.owner().asInternalName().equals(client) && call.name().equalsString("sendRequestMessage")
                                    && call.type().equalsString("(Lcom/code42/messaging/IMessage;)V")) {
                                sends++;
                                cb.aload(1).invokestatic(requests, "send", MethodTypeDesc.ofDescriptor(
                                    "(L"+client+";Lcom/code42/messaging/IMessage;"+QUERY+")V"));
                                return;
                            }
                            if (method.equals("getChildrenFileVersions") && call.owner().asInternalName().equals("com/code42/engine/v3/BackupClientV3MessageFactory")
                                    && call.name().equalsString("buildGetPlanFileChildrenRequest")) {
                                factories++;
                                require(call.type().equalsString("(Ljava/util/Set;JZLjava/util/Optional;I)Lcom/code42/protos/shared/authority/PlanAdministrationMessages$GetPlanFileChildrenRequest;") ? 1 : 0, 1);
                                cb.aload(1).invokestatic(requests, "children", MethodTypeDesc.ofDescriptor(
                                    "(Ljava/util/Set;JZLjava/util/Optional;I"+QUERY+")Lcom/code42/protos/shared/authority/PlanAdministrationMessages$GetPlanFileChildrenRequest;"));
                                return;
                            }
                            if (call.name().equalsString("isEmpty") && call.type().equalsString("()Z")
                                    && call.owner().asInternalName().equals(method.equals("getChildrenFileVersions") ? "java/util/Set" : "java/util/Optional")) {
                                missing++;
                                cb.with(element).aload(1).invokestatic(requests, "missing", MethodTypeDesc.ofDescriptor("(Z"+QUERY+")Z"));
                                return;
                            }
                        }
                        cb.with(element);
                    }
                    @Override public void atEnd(CodeBuilder cb) {
                        require(sends, 1); require(missing, method.equals("getChildrenFileVersions") ? 1 : 2);
                        require(factories, method.equals("getChildrenFileVersions") ? 1 : 0);
                    }
                })));
        }
        String session = "com/code42/messaging/SessionImpl";
        for (String receiver : new String[]{"IMessageReceiver", "IMessageReceiverProxyTarget"}) {
            add.accept(new Patch("archive-query-registry-"+receiver, "bound typed native request retention", session,
                "sendRequest", "(Lcom/code42/messaging/message/IRequestMessage;Lcom/code42/messaging/"+receiver+";Ljava/lang/Object;)V",
                null, false, CodeTransform.ofStateful(() -> new CodeTransform() {
                    int puts, constructors, sends;
                    @Override public void accept(CodeBuilder cb, CodeElement element) {
                        if (element instanceof InvokeInstruction call) {
                            if (call.owner().asInternalName().equals("com/code42/messaging/MessageContext") && call.name().equalsString("<init>")) constructors++;
                            if (call.owner().asInternalName().equals(session) && call.name().equalsString("sendMessage")
                                    && call.type().equalsString("(Lcom/code42/messaging/IMessage;)V")) {
                                sends++;
                                cb.aload(3).invokestatic(requests, "sendRegistered", MethodTypeDesc.ofDescriptor(
                                    "(Lcom/code42/messaging/ISession;Lcom/code42/messaging/IMessage;Ljava/lang/Object;)V"));
                                return;
                            }
                            if (call.owner().asInternalName().equals("java/util/Map") && call.name().equalsString("put")
                                    && call.type().equalsString("(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;")) {
                                puts++;
                                cb.aload(0).invokestatic(requests, "put", MethodTypeDesc.ofDescriptor(
                                    "(Ljava/util/Map;Ljava/lang/Object;Ljava/lang/Object;Lcom/code42/messaging/ISession;)Ljava/lang/Object;"));
                                return;
                            }
                        }
                        cb.with(element);
                    }
                    @Override public void atEnd(CodeBuilder cb) { require(puts, 1); require(constructors, 1); require(sends, 1); }
                })));
        }
        add.accept(new Patch("archive-query-registry-close", "clear typed query state before close callbacks", session,
            "close", "(Lcom/code42/messaging/network/sabre/ChannelCloseReason;)V", null, false,
            CodeTransform.ofStateful(() -> new CodeTransform() {
                int switches, clears;
                @Override public void accept(CodeBuilder cb, CodeElement element) {
                    cb.with(element);
                    if (element instanceof InvokeInstruction call) {
                        if (call.name().equalsString("clearReceivers")) clears++;
                        if (call.owner().asInternalName().equals("com/code42/utils/OneWaySwitch") && call.name().equalsString("turnOff")
                                && call.type().equalsString("()Z")) {
                            switches++;
                            cb.aload(0).getfield(ClassDesc.ofInternalName(session), "requestMessageContexts", ClassDesc.of("java.util.Map"))
                                .aload(0).invokestatic(requests, "closed", MethodTypeDesc.ofDescriptor(
                                    "(ZLjava/util/Map;Lcom/code42/messaging/ISession;)Z"));
                        }
                    }
                }
                @Override public void atEnd(CodeBuilder cb) { require(switches, 1); require(clears, 1); }
            })));
    }

    private static void require(int actual, int expected) {
        if (actual != expected) throw new IllegalStateException("native archive instruction contract changed");
    }
    private ArchivePatches() {}
}
