# Restore controls

Restore uses the selected source device, destination and backup set.
The interface provides archive browsing, historical cutoff, deleted-file inclusion, search and file versions.
Large lists are virtualized.
Selection statistics report file count, directory count and bytes.

## Destinations and output

Declare writable roots in `CPH_RESTORE_ROOTS`.
Choose an existing directory within a root for a folder restore.
Original-location restore admits each original parent before submitting the batch.
Read-only backup source mounts must be made explicitly writable before restoring into them.

The product supports native conflict strategies and current/original permissions.
Ordinary folder selection uses the requested archive cutoff, preserving newer descendants.
Only explicit file-version selection uses that version's timestamp.
Archived symlink text is preserved as a link object.
Regular writes and parent-directory traversal reject symlinks.
The boundary applies to direct restores, helper IPC and inherited decompression.

Restore path admission assumes operator-controlled writable directories.
It is not a sandbox against another process changing those directories concurrently.
Checksum, metadata and platform-specific restore behavior still require real archived-data acceptance.

## Submissions and recovery

A restore submission includes a permanent request ID.
The daemon records the request before native mutation.
Repeating the same ID and payload retrieves the saved outcome without dispatching again.
Changing the payload under an existing ID is refused, including after daemon restart.
A lost response or failed final record remains indeterminate.

Accepted means the engine accepted the submission.
It does not mean files were restored successfully.
The job view reports native counters, destination, completion/cancellation and imported-job admission.
Each job has a state like the desktop app (`running`, `waiting`, `stopped`, `completed`, `canceled`), the receive rate and the estimated time left while running, and `blockedReason` `STOPPED` (stopped by a user) or `RETRY_DELAYED` (the engine waits five minutes after an error).
Resume rechecks writable-root admission.
Start, stop and remove are sent once; when the answer is lost, a fresh job list decides whether it took effect, otherwise the result is indeterminate.
Clear completed removes finished jobs from the queue, like the desktop app's "Clear all".
Removing a native job does not delete restored files.

The active history holds up to 1024 submissions.
Explicit history archival retains permanent replay records.
Batches are archived with all children.
Active jobs and unresolved submissions cannot be archived.
A definite native rejection (no space, read-only destination, missing path, permission check) is recorded as rejected.
An indeterminate submission can be acknowledged after review (`POST /api/v1/restore/operations/{id}/acknowledge`); it stays a permanent replay record and can then be archived.
A damaged journal disables new restore submissions while backup supervision continues.

## CLI examples

Replace these example identifiers and paths with the actual archive context.

```sh
cphctl restore roots
cphctl restore browse --source 7 --destination 8 --set 1
cphctl restore versions --source 7 --destination 8 --set 1 --file-id AQAAAAAAAAAAAAAAAAAAAA==
cphctl restore contents --source 7 --destination 8 --set 1 --file-id AQAAAAAAAAAAAAAAAAAAAA==
cphctl restore start --source 7 --destination 8 --set 1 --root 0 --path /storage/data/file
cphctl restore jobs
cphctl restore operations
cphctl restore helper
cphctl restore stop 99
cphctl restore resume 99
cphctl restore remove 99
cphctl restore clear-completed
cphctl restore acknowledge aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

The start command prints its request ID before transport.
Reuse `--request-id` with the same payload to retrieve that submission.
Use `--relative` for an existing destination subdirectory.
Use `--at` for an epoch-millisecond cutoff.
Repeated `--file`, `--directory`, `--exclude-file` and `--exclude-directory` support mixed selections.
Run `cphctl help` for the complete command syntax.

## Product API

Authenticated routes use the `/api/v1/restore` prefix.
They cover roots, archive sets, browse, search, versions, selection statistics, jobs and submission history.
`POST /api/v1/restore/jobs/clear-completed` answers `{"removed": n}`.
Web mutations require the local session, CSRF token and the original page's engine-context precondition.
The private Unix API uses the same typed operations.
Neither interface exposes arbitrary native commands or native credentials.
