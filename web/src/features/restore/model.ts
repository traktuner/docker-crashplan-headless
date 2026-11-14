// Restore choices without React, so they can be tested directly.
import type { Device, RestoreSelection } from '../../shared/api'

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
