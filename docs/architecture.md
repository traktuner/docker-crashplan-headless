# Architecture

One container runs `tini → cphd → CrashPlan JVM`.
The native restore helper runs when needed.
CrashPlan owns archive formats, persistent metadata and the backup cache.
The Go modules share one process.

## Backend modules

| Directory | Responsibility |
| --- | --- |
| `cmd/cphd` | Assemble the runtime and start the daemon. |
| `cmd/cphctl` | Named headless commands over the private Unix socket. |
| `internal/app` | Typed product routes, browser authorization and public errors. |
| `internal/engine` | Native protocol, account sessions and operation contracts. |
| `internal/lifecycle` | Engine generations and serialized native operations. |
| `internal/supervise` | Owned JVM startup, restart and shutdown. |
| `internal/sources` | Source mount and identity validation. |
| `internal/restore` | Writable roots, helper supervision and permanent submission records. |
| `internal/destinations` | Device changes, local archives and maintenance records. |
| `internal/auth`, `internal/controlsocket` | Web sessions and private local transport. |
| `internal/statefile`, `internal/initcfg` | Durable records and configuration preservation. |
| `internal/config`, `internal/logx` | Environment validation and bounded console logging. |
| `internal/ui` | Embedded React assets. |

Domain files within `engine` share private session and generation state.
The lifecycle owner serializes account completion and native mutations.
Each admitted operation retains its session revision through dispatch and readback.
Browser pages also retain their original context; a session change requires a reload.
The CLI uses the same typed operations through a separate Unix trust boundary.

One native event socket serves status, SSO, restore helper requests and archive results.
Archive requests retain their original query in the native request registry.
Response processing verifies request, session, source, destination and plan identity.
An unresolved dispatched action retains its uncertainty barrier until the engine restarts.
Read-only requests always wait for their answer, even after the browser leaves.
A read class that lost an answer recovers with the next answer that verifiably belongs to its request (echoed query, source, target, backup set or a parameterless request); classes whose answers prove nothing, like backup selection browsing, keep the strict barrier; a foreign answer is rejected, never shown.
Submission records survive restart and prevent automatic duplicate scan, restore and local archive actions.

## Web modules

| Directory | Responsibility |
| --- | --- |
| `web/src/app` | Shell, navigation, session lifecycle and query policy. |
| `web/src/features/account` | Web/native login, SSO, inheritance and replacement. |
| `web/src/features/backup` | Backup sets, source browsing and selection. |
| `web/src/features/destinations` | Device and local archive controls. |
| `web/src/features/restore` | Archive browsing, search, versions and restore jobs. |
| `web/src/features/settings`, `security` | Settings and archive protection. |
| `web/src/features/activity`, `overview`, `system` | Progress, history and runtime controls. |
| `web/src/shared` | Product API client, invalidation and common components. |

The UI uses React and TypeScript.
Vite produces static assets embedded in the daemon.
Pages load as separate chunks.
Large lists use virtualized rows.
Status notifications refresh status queries without replacing source or archive browsing requests.
Browser code does not receive native HTTP tokens or access arbitrary native endpoints.

## Image and build

`Dockerfile` downloads the hash-pinned vendor distribution during the build.
`agent/src` contains original Java integration; `agent/test` contains compatibility checks.
`src/libwrapper` supplies update blocking and native compatibility integration.
`rootfs` supplies the launcher.
Runtime images contain no compiler, test runner or development evidence.
`scripts` owns maintained build and verification commands.
Compose and TrueNAS templates deploy the same image and persistence layout.
