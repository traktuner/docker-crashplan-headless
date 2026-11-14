// Human wording for backup-set values the engine reports in native units
// (milliseconds, bytes, enum codes, -1 sentinels). Pure functions, tested in
// test/backup-presentation.test.ts.
import type { ScanOperation, Setting } from '../../shared/api'
import { bytes, duration } from '../../shared/format'

export type RunWindow = {
  alwaysRun: boolean
  startTimeOfDay: string
  endTimeOfDay: string
  days: string[]
}

export const weekDays = [
  { id: 'MONDAY', short: 'Mon', label: 'Monday' },
  { id: 'TUESDAY', short: 'Tue', label: 'Tuesday' },
  { id: 'WEDNESDAY', short: 'Wed', label: 'Wednesday' },
  { id: 'THURSDAY', short: 'Thu', label: 'Thursday' },
  { id: 'FRIDAY', short: 'Fri', label: 'Friday' },
  { id: 'SATURDAY', short: 'Sat', label: 'Saturday' },
  { id: 'SUNDAY', short: 'Sun', label: 'Sunday' },
] as const

const allDays = weekDays.map((d) => d.id) as string[]

/** Reads the native window value, filling gaps so the editor always works. */
export function runWindow(value: unknown): RunWindow {
  const v = (value && typeof value === 'object' ? value : {}) as Partial<RunWindow>
  return {
    alwaysRun: v.alwaysRun !== false,
    startTimeOfDay: typeof v.startTimeOfDay === 'string' ? v.startTimeOfDay : '',
    endTimeOfDay: typeof v.endTimeOfDay === 'string' ? v.endTimeOfDay : '',
    days: Array.isArray(v.days) ? v.days.filter((d) => allDays.includes(d)) : [],
  }
}

/** ["MONDAY",…,"FRIDAY"] → "weekdays"; ["MONDAY","FRIDAY"] → "Mon, Fri". */
export function daysSummary(days: string[]): string {
  const set = new Set(days)
  const ordered = weekDays.filter((d) => set.has(d.id))
  if (ordered.length === 7) return 'every day'
  if (ordered.length === 0) return 'no days'
  const weekdays = ['MONDAY', 'TUESDAY', 'WEDNESDAY', 'THURSDAY', 'FRIDAY']
  if (ordered.length === 5 && weekdays.every((d) => set.has(d))) return 'weekdays'
  if (ordered.length === 2 && set.has('SATURDAY') && set.has('SUNDAY')) return 'weekends'
  if (ordered.length === 1) return ordered[0]!.label + 's'
  return ordered.map((d) => d.short).join(', ')
}

/** "Always" or "01:00–06:00, weekdays". An always-on window ignores its times. */
export function scheduleSummary(value: unknown): string {
  const w = runWindow(value)
  if (w.alwaysRun) return 'Always'
  const times =
    w.startTimeOfDay && w.endTimeOfDay
      ? `${w.startTimeOfDay}–${w.endTimeOfDay}`
      : 'Limited hours'
  return `${times}, ${daysSummary(w.days)}`
}

/** Problems that would make the engine reject the window, worded for people. */
export function windowProblem(w: RunWindow): string | undefined {
  if (w.alwaysRun) return undefined
  if (!/^\d\d:\d\d$/.test(w.startTimeOfDay) || !/^\d\d:\d\d$/.test(w.endTimeOfDay))
    return 'Enter a start and an end time.'
  if (w.days.length === 0) return 'Choose at least one day.'
  return undefined
}

const periodNames: [number, string][] = [
  [365 * 86_400_000, 'year'],
  [30 * 86_400_000, 'month'],
  [7 * 86_400_000, 'week'],
  [86_400_000, 'day'],
  [3_600_000, 'hour'],
  [60_000, 'minute'],
]

/** 604800000 → "every week", 1209600000 → "every 2 weeks", 900000 → "every 15 minutes". */
export function every(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—'
  for (const [size, name] of periodNames) {
    if (ms % size === 0) {
      const n = ms / size
      return n === 1 ? `every ${name}` : `every ${n.toLocaleString('en-US')} ${name}s`
    }
  }
  return 'every ' + duration(ms)
}

export function capitalize(text: string) {
  return text.charAt(0).toUpperCase() + text.slice(1)
}

function numeric(value: unknown): number | undefined {
  if (value === null || value === undefined || value === '') return undefined
  const n = Number(String(value))
  return Number.isFinite(n) ? n : undefined
}

export function fullScanSummary(interval: unknown, time: unknown): string {
  const ms = numeric(interval)
  const base = ms && ms > 0 ? capitalize(every(ms)) : 'Not scheduled'
  return ms && ms > 0 && typeof time === 'string' && time ? `${base} at ${time}` : base
}

export const compressionOptions = [
  { value: 'AUTOMATIC', label: 'Automatic' },
  { value: 'ON', label: 'On' },
  { value: 'OFF', label: 'Off' },
]
export const dedupOptions = [
  { value: 'AUTOMATIC', label: 'Automatic' },
  { value: 'FULL', label: 'Full' },
  { value: 'MINIMAL', label: 'Minimal' },
]
export function optionLabel(options: { value: string; label: string }[], value: unknown) {
  return options.find((o) => o.value === String(value ?? ''))?.label ?? 'Not set'
}

/** Decimal byte units, matching bytes() and the macOS Finder. */
export const byteUnits = [
  { id: 'KB', size: 1_000 },
  { id: 'MB', size: 1_000_000 },
  { id: 'GB', size: 1_000_000_000 },
  { id: 'TB', size: 1_000_000_000_000 },
] as const
export type ByteUnit = (typeof byteUnits)[number]['id']

/** 1000000000 → { amount: "1", unit: "GB" }. Falls back to MB with decimals. */
export function splitBytes(value: unknown): { amount: string; unit: ByteUnit } {
  const n = numeric(value) ?? 0
  for (const unit of [...byteUnits].reverse()) {
    if (n > 0 && n % unit.size === 0) return { amount: String(n / unit.size), unit: unit.id }
  }
  return { amount: String(Math.round((n / 1_000_000) * 1000) / 1000), unit: 'MB' }
}
export function joinBytes(amount: string, unit: ByteUnit): string | undefined {
  const n = Number(amount)
  if (!amount.trim() || !Number.isFinite(n) || n < 0) return undefined
  const value = Math.round(n * byteUnits.find((u) => u.id === unit)!.size)
  return Number.isSafeInteger(value) ? String(value) : undefined
}

/** Read-only retention value in words. -1 is the native "Never" sentinel. */
export function retentionValue(field: Setting): string {
  const raw = String(field.value ?? '')
  if (field.kind === 'boolean') return field.value === true ? 'Yes' : 'No'
  const n = numeric(raw)
  if (n === undefined) return '—'
  if (field.kind === 'retention-interval' && n === -1) return 'Never'
  if (field.name === 'keepDeletedMinutes') return n <= 0 ? 'Never' : 'After ' + duration(n * 60_000)
  const ms = field.unit === 'minutes' ? n * 60_000 : field.unit === 'ms' ? n : undefined
  if (ms !== undefined) return n <= 0 ? 'Never' : capitalize(every(ms))
  if (field.unit === 'bytes') return bytes(raw)
  return raw
}

/** Retention labels in plain language, keyed by native field name. */
export const retentionLabels: Record<string, string> = {
  backupFrequency: 'Back up changes',
  keepDeleted: 'Keep deleted files',
  keepDeletedMinutes: 'Remove deleted files',
  versionLastWeekInterval: 'Versions from the last week',
  versionLastNinetyDaysInterval: 'Versions from the last 90 days',
  versionLastYearInterval: 'Versions from the last year',
  versionPrevYearsInterval: 'Versions from previous years',
}

export const scanStates: Record<
  ScanOperation['state'],
  { label: string; tone?: 'blue' | 'green' | 'orange' | 'red' }
> = {
  accepted: { label: 'Starting', tone: 'blue' },
  running: { label: 'Scanning', tone: 'blue' },
  completed: { label: 'Completed', tone: 'green' },
  indeterminate: { label: 'Unconfirmed', tone: 'orange' },
  stalled: { label: 'Stalled', tone: 'orange' },
  interrupted: { label: 'Interrupted', tone: 'orange' },
  acknowledged: { label: 'Acknowledged' },
  failed: { label: 'Failed', tone: 'red' },
}
export const pendingScanStates: ScanOperation['state'][] = [
  'accepted',
  'running',
  'indeterminate',
  'stalled',
]
export const unresolvedScanStates: ScanOperation['state'][] = ['indeterminate', 'stalled']

/** ISO timestamp → epoch milliseconds for dateTime()/relative(). */
export function epoch(iso: string | undefined): number | undefined {
  if (!iso) return undefined
  const t = Date.parse(iso)
  return Number.isNaN(t) ? undefined : t
}
