// Restore choices without React, so they can be tested directly.
import type { Device, RestoreJob, RestoreJobState, RestoreSelection } from '../../shared/api'
import { words } from '../../shared/format.ts'

/** Computers whose archives can be restored: this device and the other
 * computers of the account (property OWN), as in the desktop app. Cloud
 * servers, destinations and friends' computers are not. */
export function restoreSources(devices: Device[] | undefined) {
  return (devices ?? [])
    .filter((d) => d.self || d.source || (d.owned && !d.cloud && !d.target && !d.availableTarget))
    .toSorted((a, b) => Number(b.self) - Number(a.self) || a.name.localeCompare(b.name))
}

/** Destinations that hold archives. Unused duplicates are left out. */
export function restoreDestinations(devices: Device[] | undefined) {
  return (devices ?? []).filter((d) => !d.self && (d.target || d.availableTarget))
}

export function deviceLabel(d: Device) {
  return (d.name || 'Unnamed device') + (d.self ? ' (this device)' : d.deleted ? ' (deactivated)' : '')
}

/** Backup sets of an archive. Archives of other devices may list none; the
 * engine then uses the default set, as the desktop app does. */
export function restoreSets(sets: { id: string; name: string }[] | undefined) {
  if (!sets) return undefined
  return sets.length ? sets : [{ id: '1', name: 'All files' }]
}

export function insideSelected(path: string, selection: Record<string, RestoreSelection>) {
  return Object.values(selection).some(
    (parent) => parent.isFolder && path.startsWith(parent.path.replace(/\/$/, '') + '/'),
  )
}

/** Toggling an item drops exclusions that no longer sit inside a selection. */
export function toggleSelection(
  selection: Record<string, RestoreSelection>,
  excluded: Record<string, RestoreSelection>,
  id: string,
  entry: RestoreSelection,
) {
  const next = { ...selection }
  if (next[id]) delete next[id]
  else next[id] = entry
  const keep = Object.fromEntries(
    Object.entries(excluded).filter(([key, item]) => !next[key] && insideSelected(item.path, next)),
  )
  return { selection: next, excluded: keep }
}

export type JobView = {
  state: RestoreJobState | 'unknown'
  label: string
  tone?: 'blue' | 'green' | 'orange'
  /** Which single control applies; both only when the engine gives no state. */
  canStop: boolean
  canResume: boolean
  finished: boolean
  blocked?: string
}

const blockedReasons: Record<string, string> = {
  TARGET_NOT_AVAILABLE: 'The backup destination is not available.',
  DESTINATION_NOT_AVAILABLE: 'The backup destination is not available.',
  NOT_CONNECTED: 'Waiting for the connection to the backup destination.',
  ARCHIVE_LOCKED: 'The backup is locked. Unlock it under Security.',
  INSUFFICIENT_SPACE: 'Not enough free space at the restore location.',
}

/** What a restore job is doing, from the engine's state or, on older
 * backends, from its completed/canceled flags. */
export function restoreJobView(job: RestoreJob): JobView {
  const known = ['running', 'waiting', 'stopped', 'completed', 'canceled'] as const
  const state: JobView['state'] =
    job.state && (known as readonly string[]).includes(job.state)
      ? job.state
      : job.completed
        ? 'completed'
        : job.canceled
          ? 'canceled'
          : 'unknown'
  const admissible = job.admissible !== false
  const blocked = job.blockedReason
    ? (blockedReasons[job.blockedReason] ?? words(job.blockedReason) + '.')
    : undefined
  switch (state) {
    case 'running':
      return { state, label: 'Running', tone: 'blue', canStop: true, canResume: false, finished: false, blocked }
    case 'waiting':
      return { state, label: 'Waiting', tone: 'orange', canStop: true, canResume: false, finished: false, blocked }
    case 'stopped':
      return { state, label: 'Stopped', canStop: false, canResume: admissible, finished: false, blocked }
    case 'completed':
      return { state, label: 'Completed', tone: 'green', canStop: false, canResume: false, finished: true }
    case 'canceled':
      return { state, label: 'Canceled', canStop: false, canResume: false, finished: true }
    default:
      return { state, label: 'In progress', tone: 'blue', canStop: true, canResume: admissible, finished: false, blocked }
  }
}
