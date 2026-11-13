package cph;

import java.lang.classfile.ClassFile;
import java.util.Map;

/** Build-only compatibility checks. This class is absent from the runtime agent. */
public final class NativeContractsTest {
    public static void main(String[] args) throws Exception {
        NativeContracts.verify();
        var fixture = ClassFile.of().build(java.lang.constant.ClassDesc.of("Fixture"), builder ->
                builder.withField("token", java.lang.constant.ClassDesc.of("java.lang.String"), 0));
        NativeContracts.verifyFields(ClassFile.of().parse(fixture), Map.of("token", "Ljava/lang/String;"));
        try {
            NativeContracts.verifyFields(ClassFile.of().parse(fixture), Map.of("token", "J"));
            throw new AssertionError("a changed native field type was accepted");
        } catch (IllegalStateException expected) { }
        try {
            NativeContracts.verifyFields(ClassFile.of().parse(fixture), Map.of("missing", "J"));
            throw new AssertionError("a removed native field was accepted");
        } catch (IllegalStateException expected) { }
        System.out.println("native contracts and incompatible-field probes PASS");
    }
    private NativeContractsTest() { }
}
