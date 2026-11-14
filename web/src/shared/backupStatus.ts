// What is CrashPlan doing right now? The decisions and wording follow the
// desktop app (backup_state.js), computed from GET /api/v1/backup/progress:
// a system-wide pause first, then every destination × backup set pair, the
// most severe state first. "Backup complete" appears only when CrashPlan says
// so for every pair; it is never a fallback.
import type { DestinationProgress, SetProgress } from './api.ts'
import { bytes, compactCount, duration, percent as pct, rate } from './format.ts'

export type StatusTone = 'active' | 'good' | 'warn' | 'bad' | 'idle'
export type StatusKind =
  | 'checking'
  | 'offline'
  | 'sign-in'
  | 'no-destination'
  | 'system-paused'
  | 'disconnected'
  | 'connecting'
  | 'blocked'
  | 'sync'
  | 'maintenance'
  | 'scanning'
  | 'backup'
  | 'deletes'
  | 'paused'
  | 'waiting'
  | 'preparing'
  | 'unconfirmed'
  | 'complete'

export interface BackupStatus {
  kind: StatusKind
  title: string
  detail: string
  tone: StatusTone
  /** Shown as a progress bar; value undefined = busy without a percentage. */
  progress?: { value?: number; text: string }
  destination?: string
  targetId?: string
  backupSetId?: string
  currentFile?: string
  rate?: string
  busy: boolean
  /** Which one contextual control fits: Back up now, Pause or Resume. */
  actions: { run: boolean; pause: boolean; resume: boolean }
}

const noActions = { run: false, pause: false, resume: false }

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
/** Not-ready reasons that clear by themselves; shown after active work. */
const transientNotReady = new Set([
  'BATTERY_BLOCKED',
  'BUSY',
  'DISABLED',
  'MANIFEST_MAINTENANCE',
  'MANIFEST_MIGRATION_NEEDED',
  'MANIFEST_MOVED',
  'NETWORK_BLOCKED',
  'PAUSED',
])
const disconnected: Record<string, { title: string; detail: string }> = {
  NOT_AUTHORIZED: {
    title: 'Destination disconnected',
    detail: 'This computer is not authorized at the destination. Check the account on the destination.',
  },
  NEW_VERSION: {
    title: 'Destination disconnected',
    detail: 'The destination runs a newer CrashPlan version. Update this container to continue.',
  },
  OLD_VERSION: {
    title: 'Destination disconnected',
    detail: 'The destination runs an older CrashPlan version. Update CrashPlan on the destination to continue.',
  },
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

/** A word or two for lists and badges. */
export const shortLabel: Record<StatusKind, string> = {
  checking: 'Checking',
  offline: 'Unavailable',
  'sign-in': 'Signed out',
  'no-destination': 'No destination',
  'system-paused': 'Paused',
  disconnected: 'Disconnected',
  connecting: 'Not connected',
  blocked: 'Unavailable',
  sync: 'Syncing index',
  maintenance: 'Maintenance',
  scanning: 'Scanning',
  backup: 'Backing up',
  deletes: 'Checking deletions',
  paused: 'Paused',
  waiting: 'Waiting',
  preparing: 'Preparing',
  unconfirmed: 'Waiting',
  complete: 'Complete',
}

/** The engine's own completion for a set, or undefined when unknown. */
export function completion(set: SetProgress): number | undefined {
  const value = set.percent
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? Math.min(100, value) : undefined
}

function big(value: string | undefined): bigint {
  try {
    return value && /^-?\d+$/.test(value) ? BigInt(value) : 0n
  } catch {
    return 0n
  }
}

/** Size of the whole selection; the scan result when the engine gives no total. */
export function selectionBytes(set: SetProgress): bigint {
  const total = big(set.totalBytes)
  return total > 0n ? total : big(set.bytesScanned)
}
export function selectionFiles(set: SetProgress): bigint {
  const total = big(set.totalFiles)
  return total > 0n ? total : big(set.filesScanned)
}

/** "92.5% · 184 GB of 2.47 TB left", or "Nothing left" for a finished set. */
export function progressText(set: SetProgress): string {
  const remaining = big(set.remainingBytes)
  const total = selectionBytes(set)
  const share = pct(completion(set))
  if (remaining <= 0n && big(set.remainingFiles) <= 0n)
    return share && set.complete === true ? `${share} · Nothing left` : 'Nothing left'
  const left =
    total > 0n ? `${bytes(remaining.toString())} of ${bytes(total.toString())} left` : `${bytes(remaining.toString())} left`
  return share ? `${share} · ${left}` : left
}

/** No scan result yet: progress cannot be judged. */
function noStats(set: SetProgress) {
  return completion(set) === undefined && selectionBytes(set) <= 0n && big(set.remainingBytes) <= 0n && big(set.remainingFiles) <= 0n
}

/** Transfers slower than this give no useful time estimate. */
const minEtaRate = 10_000n

interface Candidate {
  rank: number
  status: BackupStatus
}

function destinationCandidates(d: DestinationProgress, now: number): Candidate[] {
  const out: Candidate[] = []
  const at = { destination: d.name || 'Destination', targetId: d.targetId }
  if (Number(d.systemResumeTime) > now)
    out.push({
      rank: 1,
      status: {
        ...at,
        kind: 'system-paused',
        title: 'Backup paused',
        detail: `All backups are paused. They resume automatically in ${duration(String(Number(d.systemResumeTime) - now))}.`,
        tone: 'warn',
        busy: false,
        actions: { run: false, pause: false, resume: true },
      },
    })
  if (d.validating) {
    const counter = d.sync
    // The engine's counters count entries of the archive index (all versions), not your files;
    // the stock app shows only the percentage, and only when it is meaningful.
    const known = counter && counter.percent >= 0 ? counter.percent : undefined
    out.push({
      rank: 3,
      status: {
        ...at,
        kind: 'sync',
        title: 'Syncing backup index',
        detail:
          'CrashPlan is loading the list of already backed-up data from the destination, so that later only changes are sent. Backup continues afterwards.',
        tone: 'active',
        busy: true,
        actions: { run: false, pause: true, resume: false },
        progress: {
          value: known,
          text: pct(known) ?? 'This can take a while for large backups',
        },
      },
    })
  }
  if (d.maintenance) {
    const m = d.maintenance
    const step = maintenanceSteps[m.step] ?? 'Maintaining archive'
    out.push({
      rank: 4,
      status: {
        ...at,
        kind: 'maintenance',
        title: (m.cache ? 'Cache maintenance' : 'Archive maintenance') + (m.deep ? ' (deep)' : ''),
        detail: 'Routine maintenance. Backup resumes automatically when it completes.',
        tone: 'active',
        busy: true,
        actions: noActions,
        progress: { value: m.percent >= 0 ? m.percent : undefined, text: m.percent >= 0 ? `${step} · ${pct(m.percent)}` : step },
      },
    })
  }
  if ((d.sets ?? []).length === 0) return out
  if (d.connected === false) {
    const known = disconnected[d.disconnectCode ?? '']
    out.push({
      rank: known ? 2 : 8,
      status: known
        ? { ...at, kind: 'disconnected', ...known, tone: 'bad', busy: false, actions: noActions }
        : {
            ...at,
            kind: 'connecting',
            title: 'Waiting for connection',
            detail: `CrashPlan is trying to reach ${at.destination}. Backup continues when it connects.`,
            tone: 'warn',
            busy: false,
            actions: noActions,
          },
    })
  }
  if (d.outOfSpace)
    out.push({
      rank: 2,
      status: {
        ...at,
        kind: 'blocked',
        title: 'Destination out of space',
        detail: 'The destination has no space left for this backup.',
        tone: 'bad',
        busy: false,
        actions: noActions,
      },
    })
  const code = d.notReadyCode
  if (code && code !== 'NOT_AVAILABLE')
    out.push({
      rank: transientNotReady.has(code) ? 9 : 2,
      status: {
        ...at,
        kind: 'blocked',
        title: 'Destination unavailable',
        detail: notReady[code] ?? 'Backup resumes automatically when the destination is ready.',
        tone: transientNotReady.has(code) ? 'warn' : 'bad',
        busy: false,
        actions: noActions,
      },
    })
  return out
}

function setCandidates(d: DestinationProgress, s: SetProgress, now: number): Candidate[] {
  const out: Candidate[] = []
  const at = { destination: d.name || 'Destination', targetId: d.targetId, backupSetId: s.backupSetId }
  const value = completion(s)
  // The set CrashPlan is sending now; without a file path, the first set with work.
  const active = d.backingUp
    ? (d.sets.find((x) => x.sendingPath) ?? d.sets.find((x) => big(x.remainingFiles) > 0n) ?? d.sets[0])
    : undefined
  const sending = active === s
  if (s.scanning)
    out.push({
      rank: 5,
      status: {
        ...at,
        kind: 'scanning',
        title: 'Scanning backup selection',
        detail: 'Looking for new and changed files.',
        tone: 'active',
        busy: true,
        actions: { run: false, pause: true, resume: false },
        progress: { text: `${compactCount(s.filesScanned)} files · ${bytes(s.bytesScanned)} found so far` },
      },
    })
  if (sending) {
    const speed = big(d.completedRateBytesPerSecond)
    const remaining = big(s.remainingBytes)
    const eta = speed >= minEtaRate && remaining > 0n ? duration(((remaining / speed) * 1000n).toString()) : undefined
    out.push({
      rank: 6,
      status: {
        ...at,
        kind: 'backup',
        title: 'Backing up',
        detail: eta ? `About ${eta} remaining` : `Sending changed files to ${at.destination}.`,
        tone: 'active',
        busy: true,
        currentFile: s.sendingPath,
        rate: rate(d.completedRateBytesPerSecond),
        actions: { run: false, pause: true, resume: false },
        progress: { value, text: progressText(s) },
      },
    })
  }
  if (s.checkingForDeletes)
    out.push({
      rank: 7,
      status: {
        ...at,
        kind: 'deletes',
        title: 'Checking for deleted files',
        detail: 'Finding files that were removed since the last backup.',
        tone: 'active',
        busy: true,
        actions: { run: false, pause: true, resume: false },
        progress: { value, text: progressText(s) },
      },
    })
  if (Number(s.resumeTime) > now)
    out.push({
      rank: 10,
      status: {
        ...at,
        kind: 'paused',
        title: 'Backup paused',
        detail: `Backup resumes automatically in ${duration(String(Number(s.resumeTime) - now))}.`,
        tone: 'warn',
        busy: false,
        actions: { run: false, pause: false, resume: true },
      },
    })
  if (s.complete === true) {
    out.push({
      rank: 14,
      status: {
        ...at,
        kind: 'complete',
        title: 'Backup complete',
        detail: 'All selected files are backed up.',
        tone: 'good',
        busy: false,
        actions: { run: true, pause: false, resume: false },
      },
    })
  } else if (noStats(s) || d.notReadyCode === 'NOT_AVAILABLE') {
    out.push({
      rank: 12,
      status: {
        ...at,
        kind: 'preparing',
        title: 'Preparing…',
        detail: 'CrashPlan is preparing the backup. Progress appears after the first scan.',
        tone: 'idle',
        busy: false,
        actions: { run: true, pause: true, resume: false },
      },
    })
  } else if (big(s.remainingFiles) > 0n || big(s.remainingBytes) > 0n) {
    out.push({
      rank: 11,
      status: {
        ...at,
        kind: 'waiting',
        title: 'Waiting for backup',
        detail: `${compactCount(s.remainingFiles)} files (${bytes(s.remainingBytes)}) are waiting. Backup starts automatically.`,
        tone: 'idle',
        busy: false,
        actions: { run: true, pause: true, resume: false },
        progress: { value, text: progressText(s) },
      },
    })
  } else {
    out.push({
      rank: 13,
      status: {
        ...at,
        kind: 'unconfirmed',
        title: 'Waiting for backup',
        detail: 'Nothing is waiting, but CrashPlan has not reported this backup as complete yet.',
        tone: 'idle',
        busy: false,
        actions: { run: true, pause: false, resume: false },
        ...(value !== undefined ? { progress: { value, text: progressText(s) } } : {}),
      },
    })
  }
  return out
}

function mostSevere(candidates: Candidate[]): BackupStatus | undefined {
  let best: Candidate | undefined
  for (const c of candidates) if (!best || c.rank < best.rank) best = c
  return best?.status
}

/** State of one backup set at one destination (Activity page rows). */
export function pairStatus(d: DestinationProgress, s: SetProgress, now = Date.now()): BackupStatus {
  return mostSevere([...destinationCandidates(d, now), ...setCandidates(d, s, now)])!
}

export function backupStatus(input: {
  engineReady: boolean | undefined
  signedIn: boolean | undefined
  progress: DestinationProgress[] | undefined
  now?: number
}): BackupStatus {
  const base = { busy: false, actions: noActions }
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
  const read = input.progress
  if (!read || input.engineReady === undefined)
    return { ...base, kind: 'checking', title: 'Checking status…', detail: '', tone: 'idle', busy: true }
  const all = read.filter((d) => !d.error)
  if (all.length === 0 && read.length > 0)
    return {
      ...base,
      kind: 'checking',
      title: 'Status temporarily unavailable',
      detail: 'CrashPlan did not answer the status request. It is asked again in a few seconds.',
      tone: 'idle',
      busy: true,
    }
  // A destination without a reading must never let the rest read "complete".
  const unknown = read.length > all.length
  const now = input.now ?? Date.now()
  const candidates = all.flatMap((d) => [
    ...destinationCandidates(d, now),
    ...(d.sets ?? []).flatMap((s) => setCandidates(d, s, now)),
  ])
  const status = mostSevere(candidates)
  if (!status)
    return {
      ...base,
      kind: 'no-destination',
      title: 'No backup destination',
      detail: 'Add a destination to a backup set to start backing up.',
      tone: 'warn',
    }
  if (status.kind === 'complete' && unknown)
    return {
      ...base,
      kind: 'checking',
      title: 'Status partly unavailable',
      detail: 'One destination did not report its status. It is asked again in a few seconds.',
      tone: 'idle',
      busy: true,
    }
  // The overall controls act on everything, not on one pair.
  const scoped = { ...status, backupSetId: undefined, targetId: undefined }
  if (status.kind === 'complete' && all.filter((d) => d.sets?.length).length > 1)
    return { ...scoped, destination: undefined }
  return scoped
}

/** Most recent completed backup over all sets and destinations. */
export function lastBackup(progress: DestinationProgress[] | undefined): string | undefined {
  let latest = 0
  for (const d of progress ?? [])
    for (const s of d.sets ?? []) {
      const t = Number(s.lastBackup)
      if (t > latest) latest = t
    }
  return latest > 0 ? String(latest) : undefined
}
