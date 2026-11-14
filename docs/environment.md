# Configuration

**Headless mode needs no environment variable.** Mount `/config` and your data below `/storage`, and the container starts.
Web mode has two required settings, listed below. Everything else is optional.

Settings are read at container start. Account preferences, backup sets and schedules are stored by CrashPlan in `/config`;
change them with `cphctl` or the web interface, without a restart.

An optional value that cannot be used is reported in the log and replaced by its default; the container still starts.
This applies to `CRASHPLAN_SRV_MAX_MEM`, `USER_ID`, `GROUP_ID`, `SUP_GROUP_IDS` and `UMASK`.
Other values that cannot be parsed stop the start with `INVALID_CONFIGURATION`, and the log names the variable.

## Volumes

| Path | Required | Purpose |
| --- | --- | --- |
| `/config` | yes | Device identity, settings, cache and logs. Must be a persistent volume. |
| `/storage` | yes (for backups) | Data to back up. Mount one folder here, or several below it (`/storage/photos`, `/storage/docs`). Read-only is fine. |
| restore folder | no | Writable folder for restores, declared in `CPH_RESTORE_ROOTS`, for example `/restore`. |

## Basics

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `CPH_MODE` | no | `headless` | `headless`: CLI only, no open port. `web`: also serve the web interface. |
| `TZ` | no | `UTC` | Time zone for schedules and logs, for example `Europe/Vienna`. |
| `CRASHPLAN_SRV_MAX_MEM` | no | saved value, else `1024M` | Maximum Java heap, for example `8G` or `8192M`, from 1024M to 4096G. Other values are ignored with a warning and the saved value is kept. Empty keeps the saved value. |
| `CPH_LOG_LEVEL` | no | `INFO` | `DEBUG`, `INFO`, `WARN` or `ERROR`. |
| `CPH_LOG_FORMAT` | no | `json` | `json` or `text` on container stdout. |
| `LANG`, `LC_ALL` | no | `C.UTF-8` | Keep a UTF-8 locale; file names depend on it. |

## User and permissions

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `USER_ID` | no | `0` (root) | User the engine runs as. A user without an account in the image is named by its number. |
| `GROUP_ID` | no | value of `USER_ID` | Group the engine runs as. |
| `SUP_GROUP_IDS` | no | none | Comma-separated extra group IDs, for example to read group-owned data. |
| `UMASK` | no | `0022` | Octal mask for new files, for example in restores. A mask that removes the owner's own access is ignored. |

The container starts as root, gives `/config` to `USER_ID:GROUP_ID` and then drops root rights; the engine never runs as root in that case.
The engine can only back up files that this user can read.
You can also start the container directly as a user (`docker run --user 1000:1000`, Compose `user:`). Then `/config` must already be writable by that user, and `USER_ID`/`GROUP_ID` are ignored.
With the default (root), files in `/config` that belong to another user, for example from an image that ran as UID 1000, are taken over automatically.

## Sources

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `CPH_SOURCE_ROOTS` | no | every mount at or below `/storage` | JSON array of source mount paths, for example `["/storage/docs","/storage/photos"]`. |
| `CPH_SOURCE_POLICY` | no | `any` | Allowed filesystems: `any` (every real filesystem, including NFS, SMB and FUSE), `mounted` (local filesystems, NFS, SMB), `local` (ZFS, ext4, XFS, Btrfs only). |
| `CPH_SOURCE_SENTINEL` | no | none | Name of a marker file that must exist in every source, for example `.cph-source-ready`. |

Before the engine starts, and before every manual scan, the container checks each source. The engine waits while:

- a source is not a mount, or is on a pseudo filesystem such as tmpfs or overlay;
- a source that is a subfolder of a filesystem is empty (without `CPH_SOURCE_SENTINEL`), or lacks the marker file (with it);
- a source comes from a different filesystem or path than at its first start. This catches a host share that is not mounted, because the bind then shows the parent filesystem. The identities are kept in `/config/cph/source-identities.json`; delete that file while the container is stopped if you move a source on purpose.

These checks cannot see everything. A network share whose server exports an empty folder, or a share mounted below another source that is missing at start, can still look valid.
For network shares, set `CPH_SOURCE_SENTINEL` and create the marker file in every source; this is the strongest protection.
On NFS, a subfolder of a share looks like the share's top folder to these checks, so only the marker file protects it.
Sources are detected at start: to add one, mount it and restart the container. Without any mount at or below `/storage`, the engine waits; the daemon and `cphctl` still run.

## Web interface

These settings apply only with `CPH_MODE=web`.

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `CPH_PUBLIC_ORIGINS` | **yes, in web mode** | none | JSON array of the exact browser addresses, for example `["http://192.168.1.10:8080"]` or `["https://backup.example.org"]`. Up to eight. |
| `CPH_WEB_PASSWORD_FILE` or `CPH_WEB_PASSWORD` | **yes, in web mode** (one of them) | none | Web password, at least 12 characters. The file variant reads a mounted file; prefer it. A final newline is ignored. |
| `CPH_WEB_USERNAME` | no | `admin` | Web user name. Separate from the CrashPlan account. |
| `CPH_LISTEN` | no | `:8080` | Listen address inside the container. |
| `CPH_TLS_CERT_FILE`, `CPH_TLS_KEY_FILE` | no | none | PEM certificate and key for direct HTTPS. Set both or neither. |
| `CPH_TRUSTED_PROXIES` | no | none | JSON array of reverse-proxy CIDRs whose forwarded client address is trusted. |

The browser address must match one of `CPH_PUBLIC_ORIGINS` exactly, including scheme and port; otherwise requests are refused with `PUBLIC_ORIGIN_MISMATCH`.
Behind a reverse proxy, use the public HTTPS address and pass the original `Host` header.
Do not publish port 4243; it is the engine's internal port.

Direct HTTPS example:

```yaml
environment:
  CPH_MODE: web
  CPH_PUBLIC_ORIGINS: '["https://backup.example.org:8443"]'
  CPH_WEB_PASSWORD_FILE: /run/secrets/web-password
  CPH_TLS_CERT_FILE: /run/secrets/web-cert.pem
  CPH_TLS_KEY_FILE: /run/secrets/web-key.pem
ports:
  - "8443:8080"
```

## Restore and local archives

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `CPH_RESTORE_ROOTS` | no | `[]` (restores off) | JSON array of writable folders for restores and local archives, for example `["/restore"]`. |

Restores only write inside these folders. For a restore to the original location, mount that source writable and list it here.

## Memory tuning

The defaults return unused memory to the host. Change them only for a measured reason.

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `CPH_GC` | no | `g1` | Garbage collector: `g1`, `shenandoah` or `zgc`. |
| `CPH_G1_PERIODIC_GC_MS` | no | `300000` | G1: collect after this idle time and shrink the heap. |
| `CPH_UNCOMMIT_DELAY_MS` | no | `300000` | Shenandoah/ZGC: return memory after this delay. |
| `CPH_MAX_HEAP_FREE_RATIO` | no | `30` | Shrink the heap when more than this percentage is free. |
| `CPH_MIN_HEAP_FREE_RATIO` | no | `15` | Grow the heap when less than this percentage is free. |
| `CPH_TRIM_NATIVE_MS` | no | `60000` | Return freed native memory at this interval; `0` turns it off. |
| `CPH_MALLOC_ARENA_MAX` | no | `2` | Limit of native allocation arenas; fewer means less fragmentation. |

Give the container a memory limit above the heap, with room for native memory.
The saved heap is the first `-Xmx<size>` in `/config/conf/jvm_args`; lines starting with `#` are skipped.

## Advanced and diagnostics

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `CPH_PATCHES` | no | `no-file-watcher,no-watcher-walk` | Engine patches. The default turns off inotify file watching; `none` restores CrashPlan's watcher. Unknown names stop the start. |
| `CPH_STOP_GRACE_SECONDS` | no | `120` | Time the engine gets to stop, 1–240 seconds. Give Docker more (`stop_grace_period`). |
| `CPH_DIAGNOSTICS` | no | `0` | `1`: native memory tracking and JVM attach. |
| `CPH_JFR` | no | `0` | `1`: flight recording, at most 512 MiB and 24 hours. |
| `CPH_HEAPDUMP_ON_OOM` | no | `0` | `1`: heap dump when the heap runs out. Needs disk space as large as the heap. |
| `CPH_DIAG_DIR` | no | `/config/diag` | Output folder for diagnostics. |
| `CPH_EXTRA_JAVA_OPTS` | no | none | Extra JVM options, separated by spaces. Invalid options stop the engine; the private console log shows why. |
| `CPH_USER_INSTALL` | no | `1` | CrashPlan's `user.install` property. Leave it. |
| `CPH_RUN_DIR` | no | `/run/cph` | Folder of the private control socket. |
| `SSL_CERT_FILE`, `SSL_CERT_DIR` | no | none | Certificate settings for the native libraries. |

Boolean options accept `1`, `true`, `yes` and `on`.
Recordings and dumps can contain private account data; they stay in `/config` and never go to the container log.

## Compose example variables

These belong to the example Compose files, not to the container.

| Variable | Meaning |
| --- | --- |
| `CPH_IMAGE` | Image tag. |
| `CPH_CONFIG_PATH` | Host folder for `/config`. |
| `CPH_DATA_PATH` | Host folder mounted at `/storage/data`. |
| `CPH_RESTORE_PATH` | Writable host folder for restores. |
| `CPH_MEMORY_LIMIT` | Container memory limit, for example `8g`. |
| `CPH_HEAP` | Value for `CRASHPLAN_SRV_MAX_MEM`; empty keeps the saved heap. |
| `CPH_WEB_PASSWORD_PATH` | Host path of the web password file. |
| `CPH_BIND_ADDRESS`, `CPH_WEB_PORT` | Web port binding, default `127.0.0.1:8080`. |
