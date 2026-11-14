// What is CrashPlan doing right now? The same decisions and wording as the
// desktop app (backup_state.js), computed from GET /api/v1/backup/progress.
import type { DestinationProgress, SetProgress } from './api.ts'
import { bytes, compactCount, duration, percent as pct, rate } from './format.ts'

export type StatusTone = 'active' | 'good' | 'warn' | 'bad' | 'idle'
export interface BackupStatus {
  kind:
    | 'checking'
    | 'offline'
    | 'sign-in'
    | 'sync'
    | 'maintenance'
    | 'scanning'
    | 'backup'
    | 'blocked'
    | 'paused'
    | 'waiting'
    | 'complete'
    | 'no-destination'
  title: string
  detail: string
  tone: StatusTone
  /** Shown as a progress bar; undefined = busy without a known percentage. */
  progress?: { value?: number; text: string }
  destination?: string
  currentFile?: string
  rate?: string
  busy: boolean
}

const notReady: Record<string, string> = {
  BATTERY_BLOCKED: 'Low battery on the destination. Backup resumes automatically.',
  BUSY: 'The destination is busy. Backup resumes automatically.',
  CAPACITY_REDUCED: 'Storage quota reached. Contact your administrator.',
  DISABLED: 'Outside the destination’s backup schedule. Backup resumes automatically.',
  EXCEPTION: 'Unknown error at the destination. Contact support.',
  INVALID_BLOCK_NUMBER: 'Legacy archive. Contact support.',
  LICENSE_LIMIT_REACHED: 'Not licensed. Contact your administrator.',
  MANIFEST_MAINTENANCE: 'Routine maintenance. Backup resumes when it completes.',
  MANIFEST_MIGRATION_NEEDED: 'Routine maintenance. Backup resumes when it completes.',
  MANIFEST_MOVED: 'The backup location is moving. Backup resumes when the move completes.',
  MANIFEST_NOT_INITIALIZED: 'Archive missing. The backup location is not accessible.',
  NETWORK_BLOCKED: 'The destination’s network is blocked. Backup resumes on approved networks.',
  NOT_AVAILABLE: 'Preparing to back up.',
  PAUSED: 'The destination paused backup. It resumes when the destination is ready.',
}
const maintenanceSteps: Record<string, string> = {
  verifying_blocks: 'Verifying blocks',
  verifying_files: 'Verifying files',
  queued: 'Queued',
  off: 'Completed',
  step_1_prune_files: 'Pruning versions',
  step_2_prune_blocks: 'Pruning data',
  step_3_compact_files: 'Compacting versions',
  step_4_compact_blocks: 'Compacting data',
  step_5_verify_files: 'Verifying files',
}

/** Share of the scanned selection already at the destination, 0..100. */
export function completion(set: SetProgress): number | undefined {
  const scanned = BigInt(set.bytesScanned || '0')
  if (scanned <= 0n) return undefined
  const remaining = BigInt(set.remainingBytes || '0')
  const done = scanned > remaining ? scanned - remaining : 0n
  return Number((done * 10000n) / scanned) / 100
}

function sum(values: string[]) {
  return values.reduce((total, v) => total + BigInt(v || '0'), 0n).toString()
}

export function backupStatus(input: {
  engineReady: boolean | undefined
  signedIn: boolean | undefined
  progress: DestinationProgress[] | undefined
}): BackupStatus {
  const base = { busy: false }
  if (input.engineReady === false)
    return {
      ...base,
      kind: 'offline',
      title: 'Backup engine unavailable',
      detail: 'The CrashPlan engine is not responding. Check the container.',
      tone: 'bad',
    }
  if (input.signedIn === false)
    return {
      ...base,
      kind: 'sign-in',
      title: 'Sign in to CrashPlan',
      detail: 'Backups resume after you sign in to your CrashPlan account.',
      tone: 'warn',
    }
  const all = input.progress
  if (!all || input.engineReady === undefined)
    return { ...base, kind: 'checking', title: 'Checking status…', detail: '', tone: 'idle', busy: true }
  if (all.length === 0)
    return {
      ...base,
      kind: 'no-destination',
      title: 'No backup destination',
      detail: 'Add a destination to a backup set to start backing up.',
      tone: 'warn',
    }

  const sync = all.find((d) => d.validating)
  if (sync) {
    const counter = sync.sync
    const what = counter?.files ? 'file' : 'block'
    const known = counter && counter.percent >= 0 ? counter.percent : undefined
    return {
      ...base,
      busy: true,
      kind: 'sync',
      title: `Synchronizing ${what} information`,
      detail:
        'Comparing the files on this device with the files at the destination. Backup continues afterwards.',
      tone: 'active',
      destination: sync.name,
      progress: {
        value: known,
        text: counter
          ? known !== undefined
            ? `${pct(known)} · ${compactCount(counter.current)} of ${compactCount(counter.total)} ${what}s`
            : `${compactCount(counter.current)} ${what}s checked`
          : 'This may take a few moments',
      },
    }
  }

  const maint = all.find((d) => d.maintenance)
  if (maint?.maintenance) {
    const m = maint.maintenance
    const step = maintenanceSteps[m.step] ?? 'Maintaining archive'
    return {
      ...base,
      busy: true,
      kind: 'maintenance',
      title: (m.cache ? 'Cache maintenance' : 'Archive maintenance') + (m.deep ? ' (deep)' : ''),
      detail: 'Routine maintenance. Backup resumes automatically when it completes.',
      tone: 'active',
      destination: maint.name,
      progress: { value: m.percent >= 0 ? m.percent : undefined, text: m.percent >= 0 ? `${step} · ${pct(m.percent)}` : step },
    }
  }

  const sets = all.flatMap((d) => d.sets.map((s) => ({ d, s })))
  const scanning = sets.find(({ s }) => s.scanning)
  if (scanning) {
    return {
      ...base,
      busy: true,
      kind: 'scanning',
      title: 'Scanning backup selection',
      detail: 'Looking for new and changed files.',
      tone: 'active',
      destination: scanning.d.name,
      progress: {
        text: `${compactCount(scanning.s.filesScanned)} files · ${bytes(scanning.s.bytesScanned)} found so far`,
      },
    }
  }

  const sending = all.find((d) => d.backingUp)
  if (sending) {
    const active = sending.sets.find((s) => s.sendingPath) ?? sending.sets[0]
    const value = active ? completion(active) : undefined
    const remainingBytes = sum(sending.sets.map((s) => s.remainingBytes))
    const remainingFiles = sum(sending.sets.map((s) => s.remainingFiles))
    const speed = BigInt(sending.completedRateBytesPerSecond || '0')
    const eta = speed > 0n ? duration(((BigInt(remainingBytes) / speed) * 1000n).toString()) : undefined
    return {
      ...base,
      busy: true,
      kind: 'backup',
      title: 'Backing up',
      detail: eta ? `About ${eta} remaining` : 'Sending changed files to the destination.',
      tone: 'active',
      destination: sending.name,
      currentFile: active?.sendingPath,
      rate: rate(sending.completedRateBytesPerSecond),
      progress: {
        value,
        text: `${compactCount(remainingFiles)} files · ${bytes(remainingBytes)} to do`,
      },
    }
  }

  const blocked = all.find((d) => d.notReadyCode || d.outOfSpace)
  if (blocked) {
    return {
      ...base,
      kind: 'blocked',
      title: blocked.outOfSpace ? 'Destination out of space' : 'Destination unavailable',
      detail: blocked.outOfSpace
        ? 'The destination has no space left for this backup.'
        : (notReady[blocked.notReadyCode ?? ''] ?? 'Backup resumes automatically when the destination is ready.'),
      tone: blocked.notReadyCode === 'NOT_AVAILABLE' ? 'idle' : 'warn',
      destination: blocked.name,
    }
  }

  const paused = sets.find(({ s }) => Number(s.resumeInMs) > 0)
  if (paused) {
    return {
      ...base,
      kind: 'paused',
      title: 'Backup paused',
      detail: `Backup resumes automatically in ${duration(paused.s.resumeInMs)}.`,
      tone: 'warn',
      destination: paused.d.name,
    }
  }

  const waiting = sets.find(({ s }) => BigInt(s.remainingFiles || '0') > 0n)
  if (waiting) {
    return {
      ...base,
      kind: 'waiting',
      title: 'Waiting for backup',
      detail: `${compactCount(waiting.s.remainingFiles)} files (${bytes(waiting.s.remainingBytes)}) are queued. Backup resumes automatically.`,
      tone: 'idle',
      destination: waiting.d.name,
      progress: { value: completion(waiting.s), text: `${bytes(waiting.s.remainingBytes)} to do` },
    }
  }

  return {
    ...base,
    kind: 'complete',
    title: 'Backup complete',
    detail: 'All selected files are backed up.',
    tone: 'good',
    destination: all[0]?.name,
  }
}

/** Most recent completed backup over all sets and destinations. */
export function lastBackup(progress: DestinationProgress[] | undefined): string | undefined {
  let latest = 0
  for (const d of progress ?? [])
    for (const s of d.sets) {
      const t = Number(s.lastBackup)
      if (t > latest) latest = t
    }
  return latest > 0 ? String(latest) : undefined
}
