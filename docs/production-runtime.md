# Container operation

The supported deployment is one Linux Docker container. Docker Compose and the TrueNAS Custom App template use the same image.
No host service or sidecar is required. Real account, restore and deployment acceptance remain open for this release candidate.

## Persistent storage

Mount the complete configuration at `/config` on ZFS, ext4, XFS or Btrfs.
The image preserves existing identity, settings, cache and metadata. A version change does not clear the cache.
An empty configuration receives the bundled defaults.
Keep the same source paths inside the container when replacing an existing image.
Run only one engine against a configuration volume.

The daemon runs as UID 0 and replaces only files that it owns.
In an existing configuration, `machine-id`, `cp_version` and `conf/my.service.xml` must be regular files owned by UID 0 with one hard link.
The same applies to `conf/jvm_args` when `CRASHPLAN_SRV_MAX_MEM` is set.
The `var` directory must be owned by UID 0 and must not be writable by group or others; the restore helper uses private pipes there.
An image that ran its engine under another user ID can leave these items with that owner.
Initialization then stops with `CONFIG_INITIALIZATION_FAILED` or `RESTORE_HELPER_UNAVAILABLE`; the refused item remains unchanged.
Earlier initialization steps may already have created directories or updated other configuration files.
The preceding `configuration_item_refused` or `restore_helper_directory_refused` log entry names the reason, for example `foreign_owner`.
Correct the ownership while every engine is stopped, then start again.

Before replacement, stop the existing engine and take a consistent configuration backup.
Preserve its source mappings and saved heap limit.
An ordinary replacement keeps the device identity; inheritance is for deliberately replacing a device.
Native sign-in can still be required when saved automatic login is ineligible.

Mount each source separately and declare its container path in `CPH_SOURCE_ROOTS`.
The examples mount sources read-only.
A source that is a subdirectory of a filesystem requires the configured marker file.
Create that marker inside each source before startup.
The daemon checks mounts and identities before starting the engine or a manual scan.
It does not create host directories or mount network shares.

`CPH_SOURCE_POLICY=local` accepts ZFS, ext4, XFS and Btrfs.
`mounted` also permits existing NFS, NFS4 and SMB mounts; configuration still requires local storage.
Declare dedicated writable destinations in `CPH_RESTORE_ROOTS` for restores and local archives.
Original-location restore requires a writable declared root containing each destination.

## Compose

1. Create the configuration, source and restore directories.
2. Set `CPH_CONFIG_PATH`, `CPH_DATA_PATH`, `CPH_RESTORE_PATH` and `CPH_MEMORY_LIMIT`.
3. Create `.cph-source-ready` in each source, or choose a different marker name.
4. Run `docker compose -f docker-compose.example.yml up -d`.

The base example starts headless.
Add `-f docker-compose.web.example.yml` for the web interface.
Set `CPH_PUBLIC_ORIGINS` to a JSON array of exact browser origins, including scheme and port.
Set `CPH_WEB_PASSWORD_PATH` to a private file containing at least 12 password characters.
The web overlay binds to loopback by default.
Change `CPH_BIND_ADDRESS` for direct network access.

For multiple sources, add one bind per source and include every container path in the JSON array.
For multiple restore roots, add writable mounts and list their paths in `CPH_RESTORE_ROOTS`.
See [all environment options](environment.md) for formats, defaults and advanced settings.

## TrueNAS

Use Apps > Install via YAML with [the Custom App template](../deploy/truenas/custom-app.yaml).
Replace the example pool paths, browser address, memory limit and password-file path.
Use existing local datasets for configuration, sources and restores.
The template needs no privileged mode, Docker socket, host PID namespace or external watcher.
The process runs as container UID 0 to read selected files and preserve restore permissions.
This template is a Custom App, rather than a catalog listing.
Native installation, upgrade and lifecycle acceptance remain open until exercised on TrueNAS.

## Memory and scan scheduling

Set the container memory limit above the Java heap.
Allow additional memory for native libraries, filesystem cache and the control service.
Host filesystem caches also consume memory outside the JVM.
The Compose example requires an explicit limit instead of assuming a dataset size.
`CRASHPLAN_SRV_MAX_MEM` overrides the saved heap.
Leave it empty to preserve the existing value.
For a new configuration, the bundled heap setting applies; the launcher fallback is 1024 MiB.

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
