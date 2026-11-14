# CrashPlan Headless

The CrashPlan Linux engine in a small Docker image, without Electron, VNC or a desktop.
Control it with a CLI, or turn on the built-in web interface.
It can replace [jlesage/docker-crashplan-pro](https://github.com/jlesage/docker-crashplan-pro) as a drop-in: keep your `/config` and `/storage` mounts and change the image.

> Independent, unofficial project. Not affiliated with, endorsed by or sponsored by CrashPlan.
> CrashPlan and related names are trademarks of their owners.

## Quick start

```sh
docker run -d --name crashplan \
  -v /srv/crashplan/config:/config \
  -v /srv/data:/storage:ro \
  ghcr.io/traktuner/docker-crashplan-headless:latest

docker exec crashplan cphctl status
```

No setting is required. Without any environment variables the container:

- runs headless (private control socket, no open port),
- backs up whatever is mounted at or below `/storage` (the engine waits until something is mounted there),
- uses the heap saved in `/config`, or 1024 MiB on a new installation,
- runs the engine as root, or as `USER_ID`/`GROUP_ID` when you set them.

For the web interface, set `CPH_MODE=web`, `CPH_PUBLIC_ORIGINS` and a password.
See [Configuration](docs/environment.md) for every option, and which ones are required.

## What is different from the stock CrashPlan app

| | Stock CrashPlan Linux app | CrashPlan Headless |
|---|---|---|
| Interface | Electron desktop app (in Docker: VNC + browser) | CLI (`cphctl`) and an optional React web interface |
| Image | Desktop stack, X server, VNC | One small image, read-only root filesystem works |
| Memory | Heap only grows, little is returned | Idle GC, native-heap trimming and fewer malloc arenas return memory to the host |
| JVM options | Only `-Xmx` | GC choice and memory tuning through environment variables |
| File watching | inotify watch on every directory | Off by default; regular scans find changes. Saves memory and kernel resources on large data sets |
| Source safety | Engine starts even if a mount is missing | Waits while a source is not mounted, empty, or comes from a different filesystem than last time; an optional marker file gives the strongest check |
| Self-updates | Engine updates itself | Blocked; new versions come as tested image releases |
| Restore | Desktop dialog | CLI and web: browse, search, versions, restore to declared folders or the original location |
| Logs | Engine logs only | Structured JSON or text logs on stdout; engine logs stay private under `/config/log` |

## Features

- **Account:** password login, new registration, browser SSO/MFA, device inheritance and replacement.
- **Backup:** backup sets, file selection, exclusions, scan schedules, manual scans, pause/resume, typed settings.
- **Archives:** browsing and search, historical versions, local archives (create, attach, move).
- **Security:** archive protection, password recovery, passphrase keys and explicit private key export.
- **Operation:** health check, automatic engine restart, clean shutdown, rotated logs.
- **Web interface:** light and dark theme; follows the system setting or a choice per browser.
- **Memory diagnostics** on request: native memory tracking, flight recorder, heap dump.

## Moving from jlesage/docker-crashplan-pro

1. Stop the old container. Never run two engines on the same `/config`.
2. Back up `/config` while it is stopped.
3. Start this image with the same `/config` and the same `/storage/...` paths inside the container.
   Different paths look like new files to CrashPlan.

Your device identity, settings, backup sets, cache and heap are kept, and CrashPlan keeps backing up.
Sign in once with the same CrashPlan account: the web interface opens on the sign-in page
until this is done and uses the same browser sign-in as the desktop app (email, then password
and verification code on the CrashPlan page).
The device identity in `/config` is reused, so no device adoption should be needed.
`USER_ID`, `GROUP_ID`, `SUP_GROUP_IDS`, `UMASK` and `CRASHPLAN_SRV_MAX_MEM` work the same way.
One difference: without `USER_ID` the engine runs as root, so it can read every file you mount.
Files in `/config` that belong to another user are taken over automatically.

## Documentation

- [Configuration](docs/environment.md): all environment variables, required and optional
- [Operation](docs/production-runtime.md): storage, users, memory, logs, TrueNAS
- [Workflows](docs/production-workflows.md) and [restore](docs/production-restore.md)
- [Architecture](docs/architecture.md)
- Examples: [headless Compose](docker-compose.example.yml), [web overlay](docker-compose.web.example.yml), [TrueNAS Custom App](deploy/truenas/custom-app.yaml)

## Releases

Images are published at `ghcr.io/traktuner/docker-crashplan-headless`.
Tags look like `12.1.1-2-r18.1`: CrashPlan version `12.1.1`, vendor build `2`, project build `r18.1`.
`latest` and `12.1.1-2` move with new builds; pin a digest for reproducible deployments.
A daily job proposes new CrashPlan versions as pull requests; CI tests each one before release.

Not yet proven in real use: account registration/SSO/MFA, complete scheduled scans, cloud restores and
TrueNAS app lifecycle. The automated tests run without a CrashPlan account.

## License

Original code: [GNU AGPL v3.0 only](LICENSE). Copyright © 2026 traktuner.
Third-party code keeps its license, including the [jlesage MIT notice](LICENSES/jlesage-docker-crashplan-pro-MIT.txt).
CrashPlan software is not part of this repository; the image build downloads the pinned vendor release.
