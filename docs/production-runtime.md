# Container operation

The supported deployment is one Linux Docker container. Docker Compose and the TrueNAS Custom App template use the same image.
No host service or sidecar is required. Real account, restore and deployment acceptance remain open for this release candidate.

## Persistent storage

Mount the complete configuration at `/config` as a persistent volume.
A local filesystem (ZFS, ext4, XFS, Btrfs) is recommended for the native databases; other filesystems work and are reported in the log.
The image preserves existing identity, settings, cache and metadata. A version change does not clear the cache.
An empty configuration receives the bundled defaults.
Keep the same source paths inside the container when replacing an existing image.
Run only one engine against a configuration volume.

## Users and file ownership

By default the engine runs as root, so it can read every mounted file.
Set `USER_ID` and `GROUP_ID` (optionally `SUP_GROUP_IDS` and `UMASK`) to run it as another user.
The container then gives `/config` to that user and drops root rights before anything else starts.
A container started directly as a non-root user (`--user`, Compose `user:`) keeps that user; `/config` must be writable by it.

Files in `/config` that belong to another user are taken over at every start when the container starts as root.
Volumes mounted below `/config` are left alone, and nothing changes while another engine holds the volume.
This makes a configuration from another image usable, for example one that ran as UID 1000.
Entries that cannot be changed, for example on a share that maps root to another user, are reported as `configuration_ownership_incomplete`.
Group and other write access is removed from `/config/var`, where the restore helper keeps private pipes.
Only a symlink or a directory in place of `machine-id`, `cp_version`, `conf/jvm_args` or `conf/my.service.xml` stops the start;
the log entry `configuration_item_refused` names the item.

Before replacement, stop the existing engine and take a consistent configuration backup.
Preserve its source mappings and saved heap limit.
An ordinary replacement keeps the device identity; inheritance is for deliberately replacing a device.
After moving an existing `/config`, for example from jlesage/docker-crashplan-pro, CrashPlan keeps backing up, but this container
has no session of its own yet: sign in once with the same account. The log entry `native_sign_in_required` says so at start;
the web interface opens on the sign-in page, and `cphctl login <username> <server>` reads the password from stdin.
The session is then saved privately in `/config/cph` and restored at each start.

Mount your data at `/storage`, or several folders below it.
Without `CPH_SOURCE_ROOTS`, every mount at or below `/storage` is a source; the log entry `sources_detected` lists them.
The examples mount sources read-only.
The daemon checks every source before starting the engine or a manual scan: it must be mounted, a subfolder bind must not be empty
(or must contain the marker file named by `CPH_SOURCE_SENTINEL`), and it must come from the same filesystem and path as at its first start.
If a check fails, the engine waits and the log entry `source_not_ready` names the reason.
See [Sources](environment.md#sources) for what these checks cannot detect; use a marker file for network shares.
The daemon does not create host directories or mount network shares.

`CPH_SOURCE_POLICY=any` (default) accepts every real filesystem, including NFS, SMB and FUSE filesystems such as mergerfs.
`mounted` limits sources to local filesystems, NFS and SMB; `local` to ZFS, ext4, XFS and Btrfs.
Declare dedicated writable destinations in `CPH_RESTORE_ROOTS` for restores and local archives.
Original-location restore requires a writable declared root containing each destination.

## Compose

1. Create the configuration, source and restore directories.
2. Set `CPH_CONFIG_PATH`, `CPH_DATA_PATH`, `CPH_RESTORE_PATH` and `CPH_MEMORY_LIMIT`.
3. Run `docker compose -f docker-compose.example.yml up -d`.

The base example starts headless.
Add `-f docker-compose.web.example.yml` for the web interface.
Set `CPH_PUBLIC_ORIGINS` to a JSON array of exact browser origins, including scheme and port.
Set `CPH_WEB_PASSWORD_PATH` to a private file containing at least 12 password characters.
The web overlay binds to loopback by default.
Change `CPH_BIND_ADDRESS` for direct network access.

For multiple sources, add one bind per source below `/storage`; they are detected automatically.
For multiple restore roots, add writable mounts and list their paths in `CPH_RESTORE_ROOTS`.
See [all environment options](environment.md) for formats, defaults and advanced settings.

## TrueNAS

Use Apps > Install via YAML with [the Custom App template](../deploy/truenas/custom-app.yaml).
Replace the example pool paths, browser address, memory limit and password-file path.
Use existing local datasets for configuration, sources and restores.
The template needs no privileged mode, Docker socket, host PID namespace or external watcher.
The engine runs as root by default to read selected files and preserve restore permissions; set `USER_ID`/`GROUP_ID` for another user.
This template is a Custom App, rather than a catalog listing.
Native installation, upgrade and lifecycle acceptance remain open until exercised on TrueNAS.

## Memory and scan scheduling

Set the container memory limit above the Java heap.
Allow additional memory for native libraries, filesystem cache and the control service.
Host filesystem caches also consume memory outside the JVM.
The Compose example requires an explicit limit instead of assuming a dataset size.
`CRASHPLAN_SRV_MAX_MEM` overrides the saved heap.
Leave it empty to preserve the existing value.
For a new configuration, the launcher uses 1024 MiB.
An unusable value is reported and ignored; the start never fails because of the heap setting.

The default image disables inotify registration and watches.
Normal scans discover changed files.
Configure scan times and native CPU/network preferences through the interface or CLI.
A container CPU limit restricts the entire engine; it cannot guarantee scan completion within a fixed period.
Manual scans use native authorization.
Submission acceptance does not mean scan completion.

## Headless control and logs

`CPH_MODE=headless` opens the private Unix socket only.
`docker exec CONTAINER cphctl help` lists account, settings, scan, activity, archive and restore commands.
Secret-bearing operations read JSON from standard input.
Native credentials stay inside the daemon.
`CPH_MODE=web` also serves the embedded React interface.
No Node runtime is installed.
Do not publish the native engine port 4243.

`CPH_LOG_LEVEL` selects product DEBUG, INFO, WARN or ERROR messages.
`CPH_LOG_FORMAT` selects JSON or text on container stdout.
Product logs identify operations, outcomes and durations without copying native bodies or credentials.
The private console uses `/config/log/cph-console/engine.log`, with 10 MiB files and three backups.
Native file logs remain under `/config/log` with vendor rotation.
Product DEBUG does not enable native request-body logging.

The daemon owns the JVM and restore helper.
`CPH_STOP_GRACE_SECONDS` defaults to 120.
The examples allow 300 seconds for complete container shutdown.
Healthy operation has no duration cutoff.

## Access and updates

Web access uses a separate local password, same-origin checks and CSRF protection.
TLS can terminate at a reverse proxy or directly in the container.
Trust only explicitly configured proxy CIDRs.
Native account login is separate from web login.
A native session change invalidates an open page; reload to clear its forms.

The image blocks vendor self-updates.
Update the image through the pinned build and CI workflow.
CI checks native contracts, Java integration, native libraries, Go/React behavior and account-free startup.
These checks cannot guarantee compatibility with every future vendor release.
Real login/SSO/MFA, scan completion, restored data and retained-cache replacement require runtime acceptance.
