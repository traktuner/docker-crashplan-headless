import { useQuery } from '@tanstack/react-query'
import {
  ArrowDownToLine,
  ArrowRight,
  Check,
  ChevronRight,
  CloudUpload,
  FileCheck2,
  FolderClosed,
  HardDrive,
  History,
  Info,
  Radio,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import { api } from '../../shared/api'
import { ErrorNotice, Loading } from '../../shared/components'
import type { Page } from '../../app/navigation'
import {
  backupPresentation,
  displayTime,
  formatBytes,
  formatCount,
  engineReady,
} from './presentation'

export function Overview({ navigate }: { navigate: (page: Page) => void }) {
  const status = useQuery({
    queryKey: ['state'],
    queryFn: () => api.status(),
    refetchInterval: 15000,
  })
  const health = useQuery({
    queryKey: ['health'],
    queryFn: () => api.health(),
    refetchInterval: 15000,
  })
  const authorized = status.data?.authorized === true
  const summary = useQuery({
    queryKey: ['backup-summary'],
    queryFn: () => api.backupSummary(),
    refetchInterval: 15000,
    enabled: authorized,
  })
  const sets = useQuery({
    queryKey: ['set-details'],
    queryFn: () => api.backupSetDetails(),
    enabled: authorized,
  })
  const scans = useQuery({
    queryKey: ['scans'],
    queryFn: () => api.scans(),
    refetchInterval: 15000,
    enabled: authorized,
  })
  const history = useQuery({
    queryKey: ['history'],
    queryFn: () => api.history(),
    enabled: authorized,
  })
  const alerts = useQuery({
    queryKey: ['alerts'],
    queryFn: () => api.alerts(),
    enabled: authorized,
  })
  const healthy = health.error ? false : engineReady(health.data)
  const view = backupPresentation(
    healthy,
    status.isRefetchError ? undefined : status.data ? authorized : undefined,
    summary.isRefetchError ? undefined : summary.data,
  )
  const fresh = summary.isSuccess && !summary.isRefetchError
  const count = formatCount
  return (
    <>
      <header className="dashboard-heading">
        <div>
          <p className="eyebrow">BACKUP STATUS</p>
          <h1>Backup overview</h1>
          <p className="muted">
            Backup status, transfer queue and recent activity.
          </p>
        </div>
        <span className={'connection-label ' + (healthy ? 'is-connected' : '')}>
          <span />
          {healthy
            ? 'Engine connected'
            : healthy === false
              ? 'Engine unavailable'
              : 'Connecting'}
        </span>
      </header>
      <ErrorNotice error={status.error ?? health.error ?? summary.error} />
      {summary.data && summary.data.status !== 'SAFE' && (
        <div className="notice" role="alert">
          CrashPlan reports {summary.data.status}. Review Activity for details.
        </div>
      )}
      <section
        className={'backup-hero tone-' + view.tone}
        aria-label="Current backup status"
      >
        <div className="backup-hero-content">
          <div className="status-line">
            <span className={'status-pill ' + view.tone}>
              <span className={view.key === 'sending' ? 'pulse-dot' : ''} />
              {view.label}
            </span>
            <span className="snapshot-label">
              {fresh ? 'Latest engine snapshot' : 'Waiting for current status'}
            </span>
          </div>
          <h2>{view.headline}</h2>
          <p>{view.description}</p>
          <button
            className="hero-link"
            onClick={() =>
              navigate(view.key === 'sign-in' ? 'account' : 'activity')
            }
          >
            {view.key === 'sign-in'
              ? 'Connect account'
              : 'View backup activity'}
            <ArrowRight size={16} />
          </button>
        </div>
        <div className="hero-art" aria-hidden="true">
          <div className="orbit orbit-outer" />
          <div className="orbit orbit-inner" />
          <div className="orbit-center">
            <CloudUpload size={46} strokeWidth={1.25} />
          </div>
          <span className="orbit-file">
            <FileCheck2 size={20} />
          </span>
          <span className="orbit-check">
            <Check size={18} />
          </span>
          <i className="orbit-spark spark-one" />
          <i className="orbit-spark spark-two" />
        </div>
        <div className="hero-measures">
          <div>
            <span>Current upload</span>
            <strong>
              {fresh
                ? formatBytes(summary.data.sendRateBytesPerSecond, '/s')
                : '—'}
            </strong>
          </div>
          <div>
            <span>Files in queue</span>
            <strong>{fresh ? count(summary.data.remainingFiles) : '—'}</strong>
          </div>
          <div>
            <span>Data remaining</span>
            <strong>
              {fresh ? formatBytes(summary.data.remainingBytes) : '—'}
            </strong>
          </div>
          <div>
            <span>Last backup activity</span>
            <strong className="timestamp-value">
              {fresh ? displayTime(summary.data.lastBackup) : '—'}
            </strong>
          </div>
        </div>
      </section>
      <div className="dashboard-stats">
        <div className="dashboard-stat">
          <span className="stat-icon lavender">
            <FileCheck2 size={20} />
          </span>
          <div>
            <span>Files backed up</span>
            <strong>{fresh ? count(summary.data.filesBackedUp) : '—'}</strong>
            <small>Reported by CrashPlan</small>
          </div>
        </div>
        <div className="dashboard-stat">
          <span className="stat-icon blue">
            <FolderClosed size={20} />
          </span>
          <div>
            <span>Backup sets</span>
            <strong>{sets.data ? count(sets.data.length) : '—'}</strong>
            <small>Configured selections</small>
          </div>
        </div>
        <div className="dashboard-stat">
          <span className="stat-icon mint">
            <RefreshCw size={20} />
          </span>
          <div>
            <span>File discovery</span>
            <strong className="stat-text">Scheduled scans</strong>
            <small>Discover new and changed files</small>
          </div>
        </div>
      </div>
      <div className="dashboard-columns">
        <section className="card dashboard-sets">
          <div className="section-heading">
            <div>
              <h2>Backup sets</h2>
              <p>Configured sources and destinations.</p>
            </div>
            <button className="text-button" onClick={() => navigate('sets')}>
              Manage sets
              <ArrowRight size={15} />
            </button>
          </div>
          <ErrorNotice error={sets.error ?? scans.error} />
          {sets.isPending && authorized ? (
            <Loading />
          ) : sets.data?.length === 0 ? (
            <div className="empty-state">
              <FolderClosed size={28} />
              <h3>No backup sets configured</h3>
              <p>Select the files and folders to back up.</p>
              <button onClick={() => navigate('sets')}>
                Choose files
                <ArrowRight size={15} />
              </button>
            </div>
          ) : (
            <div className="overview-set-list">
              {sets.data?.map((set, index) => {
                const scan = scans.data
                  ?.filter((s) => s.backupSetId === set.id)
                  .at(-1)
                return (
                  <button
                    className="overview-set-row"
                    key={set.id}
                    onClick={() => navigate('sets')}
                  >
                    <span className={'set-avatar set-color-' + (index % 3)}>
                      <FolderClosed size={22} />
                    </span>
                    <span className="set-row-content">
                      <strong>
                        {String(
                          set.fields.find((f) => f.name === 'name')?.value ??
                            set.id,
                        )}
                      </strong>
                      <span>
                        {set.paths.filter((p) => p.selected).length} selected{' '}
                        {set.paths.filter((p) => p.selected).length === 1
                          ? 'path'
                          : 'paths'}
                        <span className="inline-dot">·</span>
                        {set.destinations.length}{' '}
                        {set.destinations.length === 1
                          ? 'destination'
                          : 'destinations'}
                      </span>
                    </span>
                    <span
                      className={
                        'scan-pill ' +
                        (scan?.state === 'completed' ? 'complete' : '')
                      }
                    >
                      {scan?.state === 'completed' ? (
                        <>
                          <Check size={13} />
                          Scanned
                        </>
                      ) : scan?.state === 'running' ? (
                        <>
                          <RefreshCw size={13} />
                          Scanning
                        </>
                      ) : scan?.state === 'accepted' ? (
                        'Scan queued'
                      ) : scan?.state === 'indeterminate' ? (
                        'Check scan'
                      ) : (
                        'View set'
                      )}
                    </span>
                    <ChevronRight size={17} />
                  </button>
                )
              })}
            </div>
          )}
          {!authorized && (
            <p className="empty-copy">
              Sign in to CrashPlan to view backup sets.
            </p>
          )}
          <div className="scan-explainer">
            <Info size={16} />
            <span>
              Files are discovered during scans. Manage timing or start a full
              scan in Backup sets.
            </span>
          </div>
        </section>
        <section className="card restore-shortcut">
          <span className="restore-shortcut-icon">
            <History size={25} strokeWidth={1.5} />
          </span>
          <span className="eyebrow">RESTORE</span>
          <h2>Restore files</h2>
          <p>Restore files, folders or earlier versions from an archive.</p>
          <button onClick={() => navigate('restore')}>
            <ArrowDownToLine size={16} />
            Restore files
            <ArrowRight size={15} />
          </button>
          <div className="shortcut-foot">
            <ShieldCheck size={14} />
            <span>Requires a configured writable destination.</span>
          </div>
        </section>
      </div>
      <div className="dashboard-columns bottom-columns">
        <section className="card recent-activity">
          <div className="section-heading">
            <div>
              <h2>Recent activity</h2>
              <p>Recent events reported by CrashPlan.</p>
            </div>
            <button
              className="text-button"
              onClick={() => navigate('activity')}
            >
              View all
              <ArrowRight size={15} />
            </button>
          </div>
          <ErrorNotice error={history.error} />
          {history.isPending && authorized ? (
            <Loading />
          ) : (
            <ol className="activity-timeline">
              {history.data
                ?.slice(-3)
                .toReversed()
                .map((item, index) => (
                  <li key={item.timestamp + ':' + index}>
                    <span className="timeline-icon">
                      {item.level === 'W' ? (
                        <Info size={14} />
                      ) : (
                        <Check size={14} />
                      )}
                    </span>
                    <div>
                      <p>{item.message}</p>
                      <time>{displayTime(item.timestamp)}</time>
                    </div>
                  </li>
                ))}
            </ol>
          )}
          {history.data?.length === 0 && (
            <p className="empty-copy">
              Your activity will appear here as CrashPlan reports it.
            </p>
          )}
        </section>
        <section className="card attention-card">
          <div className="section-heading">
            <div>
              <h2>Service details</h2>
              <p>Connection and service details.</p>
            </div>
            <Radio size={18} />
          </div>
          <dl className="service-details">
            <div>
              <dt>
                <ShieldCheck size={15} />
                CrashPlan account
              </dt>
              <dd>
                {status.data
                  ? authorized
                    ? 'Connected'
                    : 'Sign-in required'
                  : 'Checking'}
              </dd>
            </div>
            <div>
              <dt>
                <HardDrive size={15} />
                Engine version
              </dt>
              <dd>{String(status.data?.productVersion ?? '—')}</dd>
            </div>
            <div>
              <dt>
                <Info size={15} />
                Active alerts
              </dt>
              <dd>
                {alerts.data
                  ? alerts.data.length === 0
                    ? 'None'
                    : `${alerts.data.length} to review`
                  : 'Checking'}
              </dd>
            </div>
          </dl>
          <ErrorNotice error={alerts.error} />
          {alerts.data && alerts.data.length > 0 && (
            <button
              className="text-button"
              onClick={() => navigate('activity')}
            >
              Review alerts
              <ArrowRight size={14} />
            </button>
          )}
        </section>
      </div>
    </>
  )
}
