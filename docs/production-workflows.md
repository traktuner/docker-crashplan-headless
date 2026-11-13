# Application workflows

The web interface and CLI use the same typed product API.
Current implementation is a release candidate.
Real account/provider, full-scan completion and cloud restore acceptance remain open.

## Accounts

Account controls support password login, registration, browser SSO/MFA, device inheritance and replacement.
Web sign-in protects the local interface; CrashPlan sign-in authorizes native account operations.
Private registration discovery remains bound to the account operation.
Browser SSO presents the authority's canonical account name and requires explicit confirmation.
This confirms the returned identity; the native callback does not identify its originating browser attempt.
Canceling or discarding an assertion does not authorize a different identity automatically.

Device replacement supports selection, start, saved-state display, finish and cancel.
Ordinary image replacement preserves identity and does not require inheritance.
The daemon serializes account completion with native operations.
An open browser page is invalidated after an account or native session change.

## Backup and settings

Backup controls cover backup sets, mounted source browsing, file selection, exclusions and destination attachment.
Settings include native scan scheduling, CPU/network preferences and user/interface preferences.
Native policy locks remain visible and prevent edits.
Readback reports settings that the engine did not apply.
Retention values that the native handler does not update remain read-only.
Supported native retention intervals display `-1` as Never without changing the policy.

The default image discovers filesystem changes through scans.
Manual full scans require native command authorization.
Scan records distinguish accepted, running, completed, stalled and indeterminate states.
Only native completion evidence marks a scan complete.
A disconnected caller does not stop already accepted work.
Uncertain commands are not automatically repeated.
An accepted scan that remains unobserved becomes stalled after 30 minutes.
It still blocks another submission until native terminal evidence or explicit acknowledgement.
Scan records retain private account, device and session attribution.
A same-JVM session change leaves unresolved work indeterminate; newer session counters cannot complete it.

Activity controls show transfer counters, alerts, history and pause/resume.
Pause/resume retains its selected scope.
Alert dismissal is followed by a native readback.
History displays native messages as text without forwarding them to routine logs.

## Devices and local archives

Controls support device/destination rename, local archive creation, attachment, movement, relocation and destination removal.
Native ownership and policy determine available actions.
The active native adoption workflow can permit owned-archive attachment where ordinary offering is disabled.
That exception does not grant unrelated archive operations.

Local archive writes require declared writable roots.
Normal movement requires the existing source archive.
Configuration-only relocation requires absent old and new archive children.
It changes the configured path without queueing a file move.
Removal changes the native destination; it does not silently delete restored data.

Each mutation retains a request ID and digest.
Repeating an ID retrieves its recorded outcome without dispatching again.
The same ID with a different payload is refused.
Maintenance is a native command; acceptance does not prove maintenance completion.

## Archive security

Controls support archive unlock, passwords, recovery questions, protection upgrades and custom-key replacement.
Custom keys can be entered as Base64, generated randomly or derived from an exact passphrase by the native engine.
Derivation uses the native account and algorithm context.
Blank passphrases are refused because native blank input chooses random generation.

Irreversible changes require explicit confirmation and native readback.
Custom-key replacement verifies the actual resulting key, rather than the protection type alone.
Private cached keys clear when the native session changes.
Keys, passwords and tokens do not appear in ordinary product responses or events.

Current and derived keys use dedicated authenticated downloads.
The CLI writes exports only to a new private file.
The product does not expose the native arbitrary-path key import.
Paste the key or use the typed key workflow instead.

## Restore

See [restore controls](production-restore.md) for archive selection, browsing, search, versions and jobs.
The interface supports historical/deleted files, exclusions, selection counts and original-location restore.
It preserves request IDs and reports uncertain submissions without automatic replay.

## Protocol limits

V1 archive search has no verified continuation cursor.
A saturated result is marked incomplete; narrow the query instead of assuming all matches were returned.
V3 search is archive-wide because its native request has no folder filter.
Versions are filtered to the selected cutoff after native retrieval.
Native selection statistics are counts and byte totals, not archived-file content downloads.

The private native transport can match some results by message class.
After a dispatched timeout, a barrier prevents a delayed result from being attributed to a new request.
V3 queries retain their original identity in the native request registry.
Ordered WebSocket termination is required before a count/search stream is released.
A restart clears native workers; it does not prove cloud-side work completed.

## Headless operation

Run `cphctl help` for the command catalog.
Secret-bearing account/security operations read JSON from standard input.
No command accepts an arbitrary native URL, HTTP token or raw proxy operation.
Use [container configuration](environment.md) for mode, logging and deployment settings.
