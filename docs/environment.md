# Environment reference

Values configure the container at startup. Native account preferences, backup sets and schedules persist in CrashPlan configuration.
Use the CLI or web interface to change those preferences without restarting the container.

## Container and storage

| Variable | Default | Meaning |
| --- | --- | --- |
| `CPH_MODE` | `headless` | `headless`: private Unix control only. `web`: also serve the React interface. |
| `CPH_SOURCE_ROOTS` | Required | JSON array of 1–128 absolute container mount paths, for example `["/storage/documents","/storage/photos"]`. |
| `CPH_SOURCE_POLICY` | `local` | `local`: ZFS/ext4/XFS/Btrfs. `mounted`: also allow existing NFS/NFS4/SMB source mounts. |
| `CPH_SOURCE_SENTINEL` | Empty | Marker filename required inside each source. A subdirectory bind requires a marker. The Compose example uses `.cph-source-ready`. |
| `CPH_RESTORE_ROOTS` | `[]` | JSON array of writable restore/local-archive roots. An empty list disables restore/local-archive writes. |
| `CPH_CONFIG_DIR` | `/config` | Fixed persistence layout. Other values are rejected because image paths use `/config`. |
| `CPH_RUN_DIR` | `/run/cph` | Absolute private runtime directory containing `control.sock`. The CLI uses the same value. |
| `CPH_STOP_GRACE_SECONDS` | `120` | JVM termination grace, 1–240 seconds. Give the container runtime additional shutdown time. |
| `CPH_LOG_LEVEL` | `INFO` | Product log threshold: `DEBUG`, `INFO`, `WARN`, `ERROR`. |
| `CPH_LOG_FORMAT` | `json` | Product stdout format: `json` or `text`. |
| `TZ` | Image/system default | Native process timezone. Compose uses `UTC` unless overridden. |
| `LANG`, `LC_ALL` | `C.UTF-8` | Native locale and filename encoding. Keep a UTF-8 locale. |

The daemon runs as UID 0. `USER_ID` or `GROUP_ID` values other than `0`, as used by some other images, are refused at startup.
Mount every declared source at its exact container path.
For ordinary host-directory binds, create the configured marker in each source before starting.
The marker must be a regular file. Its contents are not used.
Source identities remain bound for the daemon lifetime; remapping a source requires a deliberate restart.
Set restore roots to `[]` and remove their mounts when restore/local-archive writes are not needed.

## Web interface

Web settings apply when `CPH_MODE=web`. Headless mode requires no web credentials.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CPH_LISTEN` | `:8080` | Container listener address, such as `0.0.0.0:8080`. |
| `CPH_PUBLIC_ORIGINS` | Required in web mode | JSON array of up to eight exact browser origins, for example `["https://backup.example.org"]`. |
| `CPH_WEB_USERNAME` | `admin` | Local web username, up to 256 characters. Separate from the CrashPlan account. |
| `CPH_WEB_PASSWORD_FILE` | Unset | Read a 12–65536-character password from a mounted regular file. A final newline is removed. |
| `CPH_WEB_PASSWORD` | Unset | Direct alternative to the password file. Do not set both. Prefer a private mounted file. |
| `CPH_TLS_CERT_FILE` | Unset | Mounted PEM certificate for direct HTTPS. Requires the key setting. |
| `CPH_TLS_KEY_FILE` | Unset | Mounted private key for direct HTTPS. Requires the certificate setting. |
| `CPH_TRUSTED_PROXIES` | `[]` | JSON array of up to 32 explicit proxy CIDRs used to interpret the client address. |

The web interface uses a local session cookie and CSRF token.
Engine credentials are never sent to the browser.
An engine session change invalidates the page context; reload before submitting another form.
Do not publish port 4243 or mount the private Unix socket into other containers.

Direct HTTPS example:

```yaml
environment:
  CPH_MODE: web
  CPH_PUBLIC_ORIGINS: '["https://backup.example.org:8443"]'
  CPH_TLS_CERT_FILE: /run/secrets/web-cert.pem
  CPH_TLS_KEY_FILE: /run/secrets/web-key.pem
ports:
  - "8443:8080"
# Also bind the certificate and key as read-only files at these container paths.
```

For a reverse proxy, use its public HTTPS origin and proxy to the container HTTP listener.
Configure the proxy to pass the original `Host` header.
A request whose host and port match no configured origin is refused with `PUBLIC_ORIGIN_MISMATCH`.
Restrict direct access to that listener.
Add the proxy's actual CIDR only when forwarded client-address handling is required.

## JVM memory

| Variable | Default | Meaning |
| --- | --- | --- |
| `CRASHPLAN_SRV_MAX_MEM` | Saved heap; otherwise `1024M` launcher fallback | Explicit maximum heap: digits and one unit `G`, `M` or `K`, such as `4G` or `8192M`. Minimum 1024 MiB. An explicit value updates saved `jvm_args`. |
| `CPH_GC` | `g1` | Garbage collector: `g1`, `shenandoah`, `zgc`. Keep the default unless tuning a measured workload. |
| `CPH_G1_PERIODIC_GC_MS` | `300000` | G1 periodic collection interval in milliseconds. |
| `CPH_UNCOMMIT_DELAY_MS` | `300000` | Shenandoah/ZGC uncommit delay in milliseconds. ZGC receives whole seconds. |
| `CPH_MAX_HEAP_FREE_RATIO` | `30` | JVM maximum free-heap percentage. |
| `CPH_MIN_HEAP_FREE_RATIO` | `15` | JVM minimum free-heap percentage. Keep it at or below the maximum. |
| `CPH_TRIM_NATIVE_MS` | `60000` | JVM native-heap trim interval in milliseconds; `0` disables trimming. |
| `CPH_MALLOC_ARENA_MAX` | `2` | glibc allocation-arena limit passed to the native process. |

These options control memory behavior; they do not replace the container memory limit.
Preserve sufficient headroom for native allocations and host filesystem caches.
An empty heap override preserves an existing installation.
A malformed override or one below the minimum is refused before the saved value changes.
The saved heap is the first complete `-Xmx<number><unit>` line in `/config/conf/jvm_args`.
If that file contains `-Xmx` in another form, startup stops and the private native console names the file.
It does not fall back to 1024 MiB; set `CRASHPLAN_SRV_MAX_MEM` explicitly in that case.
Unsupported JVM options fail startup and appear in the private native console.

## Advanced integration and diagnostics

| Variable | Default | Meaning |
| --- | --- | --- |
| `CPH_PATCHES` | `no-file-watcher,no-watcher-walk` | Disable both inotify registration traversal and watches. Restore/query integration remains mandatory. `none` restores vendor watcher behavior. Unknown patch names fail startup. |
| `CPH_USER_INSTALL` | `1` | Native `user.install` property. It does not bypass Linux restore-helper admission. |
| `CPH_DIAG_DIR` | `/config/diag` | Writable output directory for explicitly enabled JVM diagnostics. |
| `CPH_DIAGNOSTICS` | `0` | Enable native-memory tracking and JVM attach support. |
| `CPH_JFR` | `0` | Enable JVM flight recording, bounded to 512 MiB and 24 hours. |
| `CPH_HEAPDUMP_ON_OOM` | `0` | Enable a heap dump after Java heap exhaustion. Reserve disk space at least as large as the heap. |
| `CPH_EXTRA_JAVA_OPTS` | Empty | Expert JVM overrides, split on whitespace without shell evaluation. Quoted values containing spaces are unsupported. |
| `SSL_CERT_FILE`, `SSL_CERT_DIR` | Unset | Forward these native-library certificate settings. They do not configure the Java trust store. |

Diagnostic booleans accept `1`, `true`, `yes` and `on`, case-insensitively; other values disable the option.
Recordings, dumps and native logs can contain private account data.
They stay on persistent storage and are not copied to routine product logs.
The required Java agent cannot be disabled with `CPH_AGENT`.
Retired test controls, gates and observers are not part of the image.

## Compose substitutions

These variables belong to the example files. They are not additional daemon settings.

| Variable | Meaning |
| --- | --- |
| `CPH_IMAGE` | Image tag; defaults to the pinned version named in the example. |
| `CPH_CONFIG_PATH` | Existing host configuration directory. |
| `CPH_DATA_PATH` | Existing host source directory. |
| `CPH_RESTORE_PATH` | Existing writable host restore directory. |
| `CPH_MEMORY_LIMIT` | Required container limit, for example `8g`. Set it above the saved Java heap. Swap is disabled by matching the memory-plus-swap limit. |
| `CPH_HEAP` | Optional value for `CRASHPLAN_SRV_MAX_MEM`. Empty preserves the saved heap. |
| `CPH_WEB_PASSWORD_PATH` | Host path of the private web password file. |
| `CPH_BIND_ADDRESS` | Web host bind address, default `127.0.0.1`. |
| `CPH_WEB_PORT` | Published web port, default `8080`. |

The example forwards the mode, source policy, source/restore arrays, logging and JVM options shown in its environment block.
For another option, add its name under `environment` using the table above.
Image-build inputs are pinned in `versions.env` and `Dockerfile`; they are not container environment settings.
