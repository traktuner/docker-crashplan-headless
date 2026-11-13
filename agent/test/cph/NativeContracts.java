package cph;

import java.io.InputStream;
import java.lang.classfile.ClassFile;
import java.lang.classfile.ClassModel;
import java.util.List;
import java.util.Map;

/** Checks the vendor protocol and key-size constant without starting an engine. */
final class NativeContracts {
    private static final String MESSAGE = "com/backup42/service/ui/message/";
    private static final String REST = "com/backup42/service/ui/http/rest/";
    private static final Map<String, Map<String, String>> FIELDS = Map.ofEntries(
        Map.entry(MESSAGE + "LoginRequestMessage", Map.of("username", "Ljava/lang/String;", "password", "Ljava/lang/String;", "serverAddress", "Ljava/lang/String;", "register", "Z", "ssoAuthToken", "Ljava/lang/String;")),
        Map.entry(MESSAGE + "SsoAuthFinalizeMessage", Map.of("authToken", "Ljava/lang/String;", "username", "Ljava/lang/String;")),
        Map.entry(MESSAGE + "GetScanStatsMessage", Map.of("backupSetId", "Ljava/lang/Long;")),
        Map.entry(MESSAGE + "AddRestoreMessage", Map.of("restoreId", "Ljava/lang/Long;", "userId", "Ljava/lang/Integer;", "sourceId", "Ljava/lang/Long;", "targetId", "Ljava/lang/Long;", "dataKey", "[B", "restorePath", "Ljava/lang/String;", "files", "Ljava/util/Map;")),
        Map.entry(MESSAGE + "SearchFileVersionsMessage", Map.of("searchRegex", "Ljava/lang/String;", "privateKey", "[B", "searchFullPath", "Z")),
        Map.entry(MESSAGE + "PauseBackupRequestMessage", Map.of("backupSetId", "Ljava/lang/Long;", "durationMinutes", "Ljava/lang/Long;")),
        Map.entry(MESSAGE + "SecurityKeyTypeRequestMessage", Map.of("dataPassword", "Ljava/lang/String;", "privateKey", "Lcom/code42/backup/DataKey;")),
        Map.entry(REST + "DataKeyRestHandler$DataKeyRequest", Map.of("passphrase", "Ljava/lang/String;", "userid", "Ljava/lang/String;", "importFile", "Ljava/lang/String;")),
        Map.entry(MESSAGE + "ChangeArchivePasswordRequestMessage", Map.of("currentPassword", "Ljava/lang/String;", "newPassword", "Ljava/lang/String;"))
    );
    private static final List<String> HANDLERS = List.of(
        "LoginSetupRestHandler", "LoginConfigurationRestHandler", "AuthTokenRestHandler",
        "BackupSetRestHandler", "BackupConfigRestHandler", "ServicePeerConfigRestHandler",
        "ServiceUIConfigRestHandler", "DataKeyRestHandler", "PrivatePasswordDataKeyRestHandler",
        "CustomDataKeyRestHandler", "DeviceMigrationSelfServiceRestHandler",
        "DeviceMigrationAuthorizedRestHandler", "ReplaceDeviceRestHandler"
    );
    static ClassModel read(String name) throws Exception {
        try (InputStream input = ClassLoader.getSystemResourceAsStream(name + ".class")) {
            if (input == null) throw new IllegalStateException("missing native contract class: " + name);
            return ClassFile.of().parse(input.readAllBytes());
        }
    }
    static void verifyFields(ClassModel model, Map<String, String> fields) {
        fields.forEach((name, descriptor) -> {
            long found = model.fields().stream().filter(field ->
                    field.fieldName().equalsString(name) && field.fieldType().equalsString(descriptor)).count();
            if (found != 1) throw new IllegalStateException("native contract field changed: " + name);
        });
    }
    static void verify() throws Exception {
        for (var contract : FIELDS.entrySet()) verifyFields(read(contract.getKey()), contract.getValue());
        for (String handler : HANDLERS) read(REST + handler);
        read("com/backup42/service/ui/http/messageHandler/AddRestoreRequestMessageHandler");
        int keyBytes = Class.forName("com.code42.archive.key.v1.KeyInfoV1").getField("KEY_LENGTH_IN_BYTES").getInt(null);
        if (keyBytes != 56) throw new IllegalStateException("native V1 key-size contract changed");
    }
    private NativeContracts() { }
}
