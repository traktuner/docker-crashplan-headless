import { useQuery } from '@tanstack/react-query'
import {
  AlertTriangle,
  CheckCircle2,
  CloudUpload,
  LogIn,
  PauseCircle,
  RefreshCw,
  ServerOff,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { api } from './api'
import { backupStatus } from './backupStatus'
import type { BackupStatus } from './backupStatus'
import { engineReady } from '../features/overview/presentation'
import { Progress, Spinner } from './components'

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
      query.state.data?.some((d) => d.validating || d.backingUp || d.maintenance || d.sets.some((s) => s.scanning))
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
  sync: <RefreshCw size={22} className="spinner" />,
  maintenance: <RefreshCw size={22} className="spinner" />,
  scanning: <RefreshCw size={22} className="spinner" />,
  backup: <CloudUpload size={22} />,
  blocked: <AlertTriangle size={22} />,
  paused: <PauseCircle size={22} />,
  waiting: <PauseCircle size={22} />,
  complete: <CheckCircle2 size={22} />,
  'no-destination': <AlertTriangle size={22} />,
}
const symbolTone = {
  active: '',
  good: 'good',
  warn: 'warn',
  bad: 'bad',
  idle: 'idle',
}

export function StatusCard({
  status,
  footer,
  children,
}: {
  status: BackupStatus
  footer?: ReactNode
  children?: ReactNode
}) {
  return (
    <section className="group status-hero" aria-label="Current backup status" aria-live="polite">
      <div className="status-top">
        <span className={'status-symbol ' + symbolTone[status.tone]}>
          {icons[status.kind]}
        </span>
        <div className="row-main">
          <div className="status-title">{status.title}</div>
          {status.detail && <div className="status-sub">{status.detail}</div>}
        </div>
      </div>
      {status.progress && (
        <div style={{ display: 'grid', gap: 6 }}>
          <Progress
            value={status.progress.value}
            label={status.title}
            tone={status.kind === 'waiting' ? 'good' : undefined}
          />
          <div className="progress-line">
            <span>{status.progress.text}</span>
            {status.rate && status.rate !== '—' && <span>{status.rate}</span>}
          </div>
        </div>
      )}
      {status.currentFile && (
        <div className="row-detail truncate" title={status.currentFile}>
          Current file: {status.currentFile}
        </div>
      )}
      {children}
      {footer && <div className="status-meta">{footer}</div>}
    </section>
  )
}
