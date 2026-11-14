import { useQuery } from '@tanstack/react-query'
import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  CloudUpload,
  LogIn,
  PauseCircle,
  RefreshCw,
  ServerOff,
  WifiOff,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { api } from './api'
import { backupStatus } from './backupStatus'
import type { BackupStatus } from './backupStatus'
import { engineReady } from '../features/overview/presentation'
import { Progress, Spinner } from './components'
import { BackupActions } from './BackupActions'
import { baseName } from './format'

/** Polls fast while CrashPlan is busy so progress moves visibly. */
export function useBackupStatus(signedIn: boolean | undefined) {
  const health = useQuery({
    queryKey: ['health'],
    queryFn: () => api.health(),
    refetchInterval: 10_000,
  })
  const progress = useQuery({
    queryKey: ['backup-progress'],
    queryFn: () => api.backupProgress(),
    enabled: signedIn === true,
    refetchInterval: (query) =>
      query.state.data?.some(
        (d) => d.error || d.validating || d.backingUp || d.maintenance || d.sets.some((s) => s.scanning || s.checkingForDeletes),
      )
        ? 3_000
        : 15_000,
  })
  const ready = health.error ? false : engineReady(health.data)
  const status = backupStatus({
    engineReady: ready,
    signedIn,
    progress: progress.data,
  })
  return { status, progress, health }
}

const icons: Record<BackupStatus['kind'], ReactNode> = {
  checking: <Spinner size={22} />,
  offline: <ServerOff size={22} />,
  'sign-in': <LogIn size={22} />,
  'no-destination': <AlertTriangle size={22} />,
  'system-paused': <PauseCircle size={22} />,
  disconnected: <WifiOff size={22} />,
  connecting: <WifiOff size={22} />,
  blocked: <AlertTriangle size={22} />,
  sync: <RefreshCw size={22} className="spinner" />,
  maintenance: <RefreshCw size={22} className="spinner" />,
  scanning: <RefreshCw size={22} className="spinner" />,
  backup: <CloudUpload size={22} />,
  deletes: <RefreshCw size={22} className="spinner" />,
  paused: <PauseCircle size={22} />,
  waiting: <Clock size={22} />,
  preparing: <Clock size={22} />,
  unconfirmed: <Clock size={22} />,
  complete: <CheckCircle2 size={22} />,
}
const symbolTone = {
  active: '',
  good: 'good',
  warn: 'warn',
  bad: 'bad',
  idle: 'idle',
}

/** The file being sent: its name first, the folder below in a muted line. */
export function CurrentFile({ path }: { path: string }) {
  const name = baseName(path)
  const folder = path.slice(0, Math.max(0, path.length - name.length)).replace(/[\\/]+$/, '') || '/'
  return (
    <div className="current-file">
      <span className="current-file-label">Current file</span>
      <span className="current-file-name">{name}</span>
      <span className="current-file-folder">{folder}</span>
    </div>
  )
}

export function StatusCard({
  status,
  footer,
  controls,
  children,
}: {
  status: BackupStatus
  footer?: ReactNode
  /** Show the contextual Back up now / Pause / Resume control. */
  controls?: boolean
  children?: ReactNode
}) {
  return (
    <section className="group status-hero" aria-label="Current backup status">
      <div className="status-top">
        <span className={'status-symbol ' + symbolTone[status.tone]} aria-hidden="true">
          {icons[status.kind]}
        </span>
        <div className="row-main">
          <div className="status-title" aria-live="polite">
            {status.title}
          </div>
          {status.detail && <div className="status-sub">{status.detail}</div>}
        </div>
      </div>
      {status.progress && (
        <div className="status-progress">
          <Progress
            value={status.progress.value}
            label={status.title}
            valueText={status.progress.text}
            tone={status.kind === 'waiting' || status.kind === 'unconfirmed' ? 'good' : undefined}
          />
          <div className="progress-line">
            <span>{status.progress.text}</span>
            {status.rate && status.rate !== '—' && <span>{status.rate}</span>}
          </div>
        </div>
      )}
      {status.currentFile && <CurrentFile path={status.currentFile} />}
      {(controls || children) && (
        <div className="status-actions">
          {controls && <BackupActions actions={status.actions} />}
          {children}
        </div>
      )}
      {footer && <div className="status-meta">{footer}</div>}
    </section>
  )
}
