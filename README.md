# CrashPlan Headless

CrashPlan Linux in one Docker image, with an optional React web interface and a Go CLI.
No Electron, VNC, desktop session, Node runtime, host watchdog or sidecar is required.
CrashPlan owns the backup engine, archive formats and persistent cache.

> CrashPlan Headless is an independent, unofficial project. It is not affiliated
> with, endorsed by, or sponsored by CrashPlan. CrashPlan and related product names
> are trademarks of their respective owners.

**Status:** development candidate under independent review. No production deployment acceptance is claimed.
The source repository is public. Vendor binaries are downloaded and verified during the image build.

## Features

- Headless mode or an interactive React/TypeScript interface, selected with `CPH_MODE`.
- Password login, new registration, browser SSO/MFA, device inheritance and replacement.
- Backup sets, source trees, exclusions, scan schedules, manual full scans and pause/resume.
- Typed settings, device names, local archive creation, attachment, movement and maintenance.
- Archive browsing/search, historical versions, selected and original-location restores.
- Archive protection, password recovery, native passphrase keys and explicit private key export.
- Structured logs with `CPH_LOG_LEVEL` and `CPH_LOG_FORMAT`.
- Runtime update blocking; dependency and vendor updates are reviewed through CI.

File changes are discovered by normal scans. The default image disables inotify registration and watches.
A requested scan uses native authorization and normal metadata/deletion behavior.
A submitted scan or restore is never described as completed without native completion evidence.

## Deployment

Use [headless Compose](docker-compose.example.yml), [web Compose](docker-compose.web.example.yml),
or the [TrueNAS Custom App YAML](deploy/truenas/custom-app.yaml).
Use Apps > Install via YAML for the TrueNAS template. Adjust host paths and the browser origin.
Native TrueNAS lifecycle acceptance remains pending; no host changes are part of these templates.

Read [container operation](docs/production-runtime.md) and [all environment options](docs/environment.md) before starting an engine.
Declare each source mount in `CPH_SOURCE_ROOTS`. Restore and local archive writes require
explicit `CPH_RESTORE_ROOTS`. Configuration must use a real local filesystem mount.
`CPH_SOURCE_POLICY=local` is the default. `mounted` also admits declared NFS/NFS4/SMB source mounts,
with the same source identity checks. It does not mount shares or change firewall/export settings.
Web mode requires a password and the exact browser origin. Headless mode opens no public listener.

## Existing configuration

Keep the existing `/config` volume and every source path inside the container unchanged.
The jlesage configuration layout, device identity, cache and explicit heap setting are preserved.
An ordinary replacement keeps the device identity, so backup inheritance is unnecessary.
Native sign-in can still be required when saved automatic login is ineligible.
Stop the old engine before using its configuration with this image. Never run both against one configuration.
Take a consistent stopped backup before switching. Do not use an older backup after new metadata advances.
Changing the image version never clears the cache. Actual replacement acceptance is still pending.

## Controls and logging

`docker exec crashplan cphctl help` lists the CLI. Web and CLI use the same typed product operations.
Secrets are supplied through protected files or standard input, rather than command arguments.
`CPH_LOG_LEVEL` controls product logs; it does not enable permanent native DEBUG body logging.
Vendor logs remain private and rotated under `/config/log`.

See [workflows](docs/production-workflows.md), [restore behavior](docs/production-restore.md)
and [module ownership](docs/architecture.md).

## Build and compatibility

`scripts/build.sh` builds the pinned release. CI checks native method and wire contracts,
Java patches, native libraries, Go tests/race checks, React tests/build and account-free image startup.
Renovate manages dependency updates. The upstream workflow proposes new CrashPlan releases.
It checks the vendor download daily at 04:17 UTC and supports manual runs.
New releases become a pull request with a pinned checksum; CI must pass before promotion.
Each successful main build publishes an image and a GitHub release with generated change notes.
Image tags use `VERSION-VENDOR_BUILD-rRUN.ATTEMPT`, for example `12.1.1-2-r18.1`.
`2` is the vendor's build number. `18.1` identifies this project's workflow run and attempt.
Build numbers can have gaps because PRs also run CI. A rebuilt job attempt receives a new image tag.
The matching Git tag is `v12.1.1-2-r18.1`. Release notes include the exact source commit and image digest.
The `VERSION-VENDOR_BUILD-SOURCE_COMMIT` image alias remains available, along with the floating
`VERSION-VENDOR_BUILD` and `latest` aliases. Pin the digest for a reproducible deployment.
PR builds never publish images or releases. A failed check or image push prevents release creation.
An existing release is not overwritten.
A changed contract blocks promotion until the integration is adapted. Existing cache is never rebuilt by CI.

Real account registration/SSO/MFA, completed manual and scheduled scans, cloud restore checksums/metadata,
account-backed browser workflows and native TrueNAS lifecycle still require runtime acceptance.
Local fixtures and independent source reviews do not establish those outcomes.

## License

Original code in this repository is licensed under the
[GNU Affero General Public License v3.0 only](LICENSE) (`AGPL-3.0-only`).
Copyright © 2026 traktuner.

Third-party code retains its own license and attribution, including the
[jlesage MIT notice](LICENSES/jlesage-docker-crashplan-pro-MIT.txt).
CrashPlan software is not licensed by this project and is not included in the source repository.
The image build downloads the pinned vendor distribution and processes it under its separate terms.
