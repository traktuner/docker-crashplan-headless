import { isLosslessNumber } from 'lossless-json'
import type { BackupSummary } from '../../shared/api'

export type BackupPhase =
  'unknown' | 'sending' | 'scanning' | 'validating' | 'waiting' | 'idle'
export function backupPhase(summary: BackupSummary | undefined): BackupPhase {
  if (!summary) return 'unknown'
  if (summary.activities & 1) return 'sending'
  if (summary.activities & 16) return 'scanning'
  if (summary.activities & 32) return 'validating'
  if (summary.remainingFiles > 0) return 'waiting'
  return 'idle'
}
export function formatBytes(input: string | undefined, suffix = ''): string {
  if (input === undefined || !/^\d+$/.test(input)) return '—'
  const bytes = BigInt(input)
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB'] as const
  let unit = 1n,
    index = 0
  while (bytes >= unit * 1024n && index < units.length - 1) {
    unit *= 1024n
    index++
  }
  const decimals = index === 0 ? 0 : bytes < unit * 10n ? 2 : 1
  const scale = decimals === 0 ? 1n : decimals === 1 ? 10n : 100n
  const rounded = (bytes * scale + unit / 2n) / unit
  const integer = (rounded / scale).toLocaleString('en-US')
  const fraction = decimals
    ? '.' + (rounded % scale).toString().padStart(decimals, '0')
    : ''
  return `${integer}${fraction} ${units[index]}${suffix}`
}
export function displayTime(input: string | undefined): string {
  if (
    !input ||
    !/^\d+$/.test(input) ||
    BigInt(input) > BigInt(Number.MAX_SAFE_INTEGER)
  )
    return 'Not reported'
  const date = new Date(Number(input))
  if (date.getTime() <= 0 || Number.isNaN(date.getTime())) return 'Not reported'
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

const phases = {
  checking: {
    key: 'checking',
    label: 'Checking status',
    headline: 'Checking service status',
    description: 'Waiting for current engine and account status.',
    tone: 'neutral',
  },
  offline: {
    key: 'offline',
    label: 'Service unavailable',
    headline: 'Backup service unavailable',
    description:
      'Check the engine status and source mounts before resuming work.',
    tone: 'warning',
  },
  'sign-in': {
    key: 'sign-in',
    label: 'Sign-in required',
    headline: 'CrashPlan sign-in required',
    description: 'Sign in to access your backup sets and archived files.',
    tone: 'warning',
  },
  sending: {
    key: 'sending',
    label: 'Sending',
    headline: 'Backup transfer active',
    description: 'CrashPlan is sending files to the backup destination.',
    tone: 'good',
  },
  scanning: {
    key: 'scanning',
    label: 'Scanning',
    headline: 'Scanning selected files',
    description:
      'CrashPlan is checking selected files and folders for changes.',
    tone: 'good',
  },
  validating: {
    key: 'validating',
    label: 'Validating',
    headline: 'Validating archive metadata',
    description: 'CrashPlan is validating backup metadata before continuing.',
    tone: 'neutral',
  },
  waiting: {
    key: 'waiting',
    label: 'Waiting',
    headline: 'Files queued for backup',
    description:
      'Files are queued. Check Activity for the latest engine status.',
    tone: 'neutral',
  },
  idle: {
    key: 'idle',
    label: 'Idle',
    headline: 'No files queued',
    description:
      'The engine reports no files in its transfer queue. New changes are found during scans.',
    tone: 'neutral',
  },
} as const
export function backupPresentation(
  healthy: boolean | undefined,
  authorized: boolean | undefined,
  summary: BackupSummary | undefined,
) {
  if (healthy === false) return phases.offline
  if (healthy === undefined || authorized === undefined) return phases.checking
  if (!authorized) return phases['sign-in']
  const phase = backupPhase(summary)
  return phase === 'unknown' ? phases.checking : phases[phase]
}

export function formatCount(value: unknown): string {
  const decimal = isLosslessNumber(value)
    ? value.toString()
    : typeof value === 'string'
      ? value
      : typeof value === 'number' && Number.isSafeInteger(value)
        ? String(value)
        : ''
  return /^\d+$/.test(decimal) ? BigInt(decimal).toLocaleString('en-US') : '—'
}

export function engineReady(value: unknown): boolean | undefined {
  if (value === undefined) return undefined
  if (
    !value ||
    typeof value !== 'object' ||
    !('status' in value) ||
    value.status !== 'running' ||
    !('native' in value)
  )
    return false
  const native = value.native
  return (
    !!native &&
    typeof native === 'object' &&
    'nativeReady' in native &&
    native.nativeReady === true &&
    'eventsReady' in native &&
    native.eventsReady === true
  )
}
