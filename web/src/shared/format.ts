// Human-readable values for everything the engine reports as raw numbers.
// Sizes use decimal units (1 TB = 1000 GB) like macOS Finder and CrashPlan.
import { isLosslessNumber } from 'lossless-json'

function digits(value: unknown): string | undefined {
  if (isLosslessNumber(value)) value = value.toString()
  if (typeof value === 'number' && Number.isFinite(value))
    return String(Math.trunc(value))
  if (typeof value === 'string' && /^-?\d+$/.test(value.trim()))
    return value.trim()
  return undefined
}

const sizeUnits = ['bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB'] as const

/** 2470567748275 → "2.47 TB". Unknown values give "—". */
export function bytes(value: unknown): string {
  const d = digits(value)
  if (d === undefined || d.startsWith('-')) return '—'
  const n = BigInt(d)
  if (n < 1000n) return `${n} ${n === 1n ? 'byte' : 'bytes'}`
  let unit = 0
  while (n >= 1000n ** BigInt(unit + 1) && unit < sizeUnits.length - 1) unit++
  const divisor = 1000n ** BigInt(unit)
  const whole = n / divisor
  const decimals = whole >= 100n ? 0 : whole >= 10n ? 1 : 2
  const scale = 10n ** BigInt(decimals)
  const rounded = (n * scale + divisor / 2n) / divisor
  const integer = rounded / scale
  const fraction = decimals
    ? '.' + (rounded % scale).toString().padStart(decimals, '0')
    : ''
  return `${integer.toLocaleString('en-US')}${fraction} ${sizeUnits[unit]}`
}

/** Transfer rate: "0 B/s" → "—" when nothing moves. */
export function rate(value: unknown): string {
  const d = digits(value)
  if (d === undefined || d === '0') return '—'
  return bytes(d).replace('bytes', 'B') + '/s'
}

/** 725748 → "725,748". */
export function count(value: unknown): string {
  const d = digits(value)
  if (d === undefined) return '—'
  return BigInt(d).toLocaleString('en-US')
}

/** 60800000 → "60.8 million". */
export function compactCount(value: unknown): string {
  const d = digits(value)
  if (d === undefined) return '—'
  const n = Number(d)
  if (n < 10_000) return n.toLocaleString('en-US')
  return new Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumFractionDigits: 1,
  })
    .format(n)
    .replace('K', ' thousand')
    .replace('M', ' million')
    .replace('B', ' billion')
}

export function plural(n: number, one: string, many = one + 's') {
  return `${n.toLocaleString('en-US')} ${n === 1 ? one : many}`
}

/** Milliseconds → "7 days", "16 hours 39 minutes", "45 seconds". */
export function duration(msValue: unknown): string {
  const d = digits(msValue)
  if (d === undefined || d.startsWith('-')) return '—'
  let seconds = Math.round(Number(d) / 1000)
  if (seconds < 60) return plural(seconds, 'second')
  const units: [number, string][] = [
    [86400, 'day'],
    [3600, 'hour'],
    [60, 'minute'],
  ]
  const parts: string[] = []
  for (const [size, name] of units) {
    const amount = Math.floor(seconds / size)
    if (amount > 0) {
      parts.push(plural(amount, name))
      seconds -= amount * size
    }
    if (parts.length === 2) break
  }
  return parts.join(' ')
}

function toDate(value: unknown): Date | undefined {
  const d = digits(value)
  if (d === undefined || d.startsWith('-') || d === '0') return undefined
  const date = new Date(Number(d))
  return Number.isNaN(date.getTime()) ? undefined : date
}

/** "14 Nov 2026, 09:48" in the viewer's locale and time zone. */
export function dateTime(value: unknown, fallback = '—'): string {
  const date = toDate(value)
  if (!date) return fallback
  return date.toLocaleString(undefined, {
    day: 'numeric',
    month: 'short',
    year: date.getFullYear() === new Date().getFullYear() ? undefined : 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** "3 minutes ago", "yesterday", "in 2 hours". */
export function relative(value: unknown, fallback = '—'): string {
  const date = toDate(value)
  if (!date) return fallback
  const diff = (date.getTime() - Date.now()) / 1000
  const abs = Math.abs(diff)
  const format = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
  if (abs < 45) return 'just now'
  if (abs < 3600) return format.format(Math.round(diff / 60), 'minute')
  if (abs < 86400) return format.format(Math.round(diff / 3600), 'hour')
  if (abs < 86400 * 30) return format.format(Math.round(diff / 86400), 'day')
  return dateTime(value)
}

/** Percentage with one decimal, or undefined when the engine gave none. */
export function percent(value: number | undefined): string | undefined {
  if (value === undefined || value < 0 || !Number.isFinite(value)) return undefined
  return (value >= 99.95 && value < 100 ? 99.9 : Math.round(value * 10) / 10).toFixed(1).replace(/\.0$/, '') + '%'
}

/** Last path segment for display; the root stays "/". */
export function baseName(path: string): string {
  if (!path || path === '/') return path || '/'
  const trimmed = path.replace(/[\\/]+$/, '')
  const index = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
  return index >= 0 ? trimmed.slice(index + 1) || trimmed : trimmed
}

/** CamelCase identifiers into words: "HelpIntroAlert" → "Help intro alert". */
export function words(id: string): string {
  const spaced = id
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_.-]+/g, ' ')
    .trim()
    .toLowerCase()
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

/** Duration units for editing millisecond settings. */
export const durationUnits = [
  { id: 'seconds', label: 'seconds', ms: 1_000 },
  { id: 'minutes', label: 'minutes', ms: 60_000 },
  { id: 'hours', label: 'hours', ms: 3_600_000 },
  { id: 'days', label: 'days', ms: 86_400_000 },
] as const
export type DurationUnit = (typeof durationUnits)[number]['id']

/** Picks the largest unit that represents the value exactly. */
export function splitDuration(msValue: unknown): { amount: string; unit: DurationUnit } {
  const d = digits(msValue)
  const ms = d === undefined ? 0 : Number(d)
  for (const unit of [...durationUnits].reverse()) {
    if (ms > 0 && ms % unit.ms === 0) return { amount: String(ms / unit.ms), unit: unit.id }
  }
  return { amount: String(ms / 1000), unit: 'seconds' }
}

export function joinDuration(amount: string, unit: DurationUnit): string | undefined {
  const n = Number(amount)
  if (!amount.trim() || !Number.isFinite(n) || n < 0) return undefined
  const ms = Math.round(n * durationUnits.find((u) => u.id === unit)!.ms)
  return Number.isSafeInteger(ms) ? String(ms) : undefined
}
