package config

import (
	"encoding/base64"
	"strings"
)

// NativeEnvironment passes only the documented JVM/launcher inputs. Product
// web credentials and unrelated host credentials never enter the child.
func NativeEnvironment(input []string, userName, home string, restoreRoots ...string) []string {
	allowed := map[string]bool{
		"LANG": true, "LC_ALL": true, "TZ": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
		"CRASHPLAN_SRV_MAX_MEM": true, "CPH_PATCHES": true, "CPH_GC": true,
		"CPH_G1_PERIODIC_GC_MS": true, "CPH_MAX_HEAP_FREE_RATIO": true, "CPH_MIN_HEAP_FREE_RATIO": true,
		"CPH_TRIM_NATIVE_MS": true, "CPH_UNCOMMIT_DELAY_MS": true, "CPH_MALLOC_ARENA_MAX": true,
		"CPH_USER_INSTALL": true, "CPH_DIAG_DIR": true, "CPH_DIAGNOSTICS": true,
		"CPH_JFR": true, "CPH_HEAPDUMP_ON_OOM": true, "CPH_EXTRA_JAVA_OPTS": true, "CPH_RUN_DIR": true,
	}
	result := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + home, "USER=" + userName, "LOGNAME=" + userName}
	for _, entry := range input {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[key] {
			result = append(result, entry)
		}
	}
	encoded := make([]string, len(restoreRoots))
	for i, root := range restoreRoots {
		encoded[i] = base64.StdEncoding.EncodeToString([]byte(root))
	}
	result = append(result, "CPH_RESTORE_ROOTS_B64="+strings.Join(encoded, ":"))
	return result
}
