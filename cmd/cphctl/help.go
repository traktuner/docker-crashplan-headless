package main

const usage = `Usage: cphctl COMMAND

status | health | summary | devices
sets list|show|create|edit|delete|priority
select browse|set
settings get AREA | settings set AREA FIELD JSON
scan SET [--wait --timeout 24h] | scans | scan ack ID
pause [--set ID|--plan ID|--destination ID] [--minutes 60]
resume [--set ID|--plan ID|--destination ID]
alerts [dismiss ID] | history
account setup|configuration|login|sso|sso-status|sso-cancel
account sso-confirm ID ASSERTION_ID --confirm CANONICAL_USERNAME
account sso-discard ID ASSERTION_ID
account migration|candidates|replacement|migrate|inherit|replace
security status|unlock|backup-unlock|password|key-type
security export-key --output NEW_PRIVATE_FILE
restore roots|sets|browse|versions|search|start|jobs|operations|helper
restore stop|resume|remove ID
restore archive --confirm ARCHIVE REQUEST_ID...
engine restart

Account login/configuration/SSO and security mutations read a JSON object from stdin.
The legacy login USER SERVER command reads only its password from stdin.
Never put passwords, archive keys or native tokens in command arguments.

Devices: devices; devices rename ID --name NAME [--request-id HEX32];
  devices remove ID --confirm ID [--request-id HEX32]; devices status HEX32.
  devices maintenance ID --confirm ID [--request-id HEX32] submits native archive maintenance.
Local archives: destinations policy; destinations status HEX32; destinations create|attach|move|relocate --root ID
  [--relative DIRECTORY] [--source ID] [--destination ID] --confirm CREATE|ATTACH|MOVE|RELOCATE
  [--request-id HEX32]. Keep LOCAL_ARCHIVE_REQUEST_ID to retrieve the same submission.

Restore queries use --source ID --destination ID --set ID.
Use --at EPOCH_MILLISECONDS for a historical archive cutoff (0 means latest).
Search adds --pattern PATTERN and optionally --full-path.
Start adds --root ID [--relative EXISTING_DIRECTORY], or --original.
Select files with repeated --file PATH and --directory PATH.
Exclude descendants with --exclude-file PATH and --exclude-directory PATH.
Conflict strategy: --conflict RENAME_ORIGINAL|OVERWRITE_ORIGINAL|SKIP_ALL|SKIP_IF_NEWER|SKIP_IF_OLDER.
Permissions: --permissions current|original. Use --include-deleted when required.
Keep the printed RESTORE_REQUEST_ID; repeat it with the same payload using --request-id.
An unknown result is never retried automatically. Scan wait timeout does not stop the engine.

Settings areas: backup, network, general, ui, peer.
Migration needs an offered device ID and --confirm ID. account migrate new registers a new device.
account replacement finish|cancel --confirm "FINISH REPLACEMENT"|"CANCEL REPLACEMENT" closes saved setup state.
CPH_RUN_DIR selects the private control socket directory (default /run/cph).
`
