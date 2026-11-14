import { useQuery } from '@tanstack/react-query'
import { ChevronRight, FolderClosed, History } from 'lucide-react'
import { api } from '../../shared/api'
import { ErrorNotice, Loading, Page, Section } from '../../shared/components'
import { StatusCard, useBackupStatus } from '../../shared/StatusCard'
import { lastBackup, pairStatus, progressText, shortLabel } from '../../shared/backupStatus'
import { dateTime, percent, plural, relative } from '../../shared/format'
import { eventKeys, eventLevel } from '../activity/events'
import type { Page as PageId } from '../../app/navigation'

const health = {
  SAFE: 'On schedule',
  WARNING: 'Backup overdue',
  SEVERE: 'Backup critically overdue',
} as const

export function Overview({
  navigate,
  signedIn,
}: {
  navigate: (page: PageId) => void
  signedIn: boolean | undefined
}) {
  const { status, progress } = useBackupStatus(signedIn)
  const ready = signedIn === true
  const summary = useQuery({
    queryKey: ['backup-summary'],
    queryFn: () => api.backupSummary(),
    refetchInterval: 30_000,
    enabled: ready,
  })
  const sets = useQuery({
    queryKey: ['set-details'],
    queryFn: () => api.backupSetDetails(),
    enabled: ready,
  })
  const devices = useQuery({
    queryKey: ['devices'],
    queryFn: () => api.devices(),
    enabled: ready,
  })
  const history = useQuery({
    queryKey: ['history', 5],
    queryFn: () => api.history(5),
    enabled: ready,
    refetchInterval: 60_000,
  })
  const last = lastBackup(progress.data)
  const syncing = status.kind === 'sync' || status.kind === 'checking'
  const name = (id: string) =>
    devices.data?.find((d) => String(d.id) === String(id))?.name || 'Destination'
  const newest = (history.data ?? []).slice(-5)
  const keys = eventKeys(newest).toReversed()
  const recent = newest.toReversed()

  return (
    <Page title="Overview" subtitle="What CrashPlan is doing right now.">
      <ErrorNotice error={progress.error} />
      <StatusCard
        status={status}
        controls={ready}
        footer={
          ready && (
            <>
              {status.destination && (
                <span>
                  Destination <strong>{status.destination}</strong>
                </span>
              )}
              <span>
                Last backup{' '}
                <strong title={dateTime(last)}>
                  {last ? relative(last) : 'not reported yet'}
                </strong>
              </span>
              {summary.data && (
                <span>
                  Status <strong>{health[summary.data.status]}</strong>
                </span>
              )}
            </>
          )
        }
      >
        {ready && (
          <button className="btn btn-plain" onClick={() => navigate('activity')}>
            Show details
          </button>
        )}
      </StatusCard>

      {ready && (
        <Section
          title="Backup sets"
          actions={
            <button className="btn btn-plain btn-sm" onClick={() => navigate('sets')}>
              Manage
            </button>
          }
          footer={
            syncing
              ? 'Remaining amounts are recalculated after the synchronization finishes.'
              : undefined
          }
        >
          {sets.isPending ? (
            <Loading />
          ) : (
            <>
              <ErrorNotice error={sets.error} />
              {sets.data?.length === 0 && (
                <div className="row">
                  <div className="row-main">
                    <div className="row-title">No backup sets</div>
                    <div className="row-detail">Choose folders to back up in Backup sets.</div>
                  </div>
                </div>
              )}
              {sets.data?.map((set) => {
                const id = String(set.id)
                const setName = String(set.fields.find((f) => f.name === 'name')?.value ?? 'Backup set')
                const folders = set.paths.filter((p) => p.selected).length
                const perDestination =
                  progress.data?.flatMap((d) =>
                    d.error ? [] : d.sets.filter((s) => s.backupSetId === id).map((s) => ({ d, s })),
                  ) ?? []
                // One short value per set; the longer explanation goes below the name.
                const states = perDestination.map(({ d, s }) => ({ d, s, state: pairStatus(d, s) }))
                const worst = states.find(({ state }) => state.tone === 'bad' || state.tone === 'warn') ?? states.find(({ state }) => state.kind !== 'complete') ?? states[0]
                const allComplete = states.length > 0 && states.every(({ state }) => state.kind === 'complete')
                const value = syncing || !worst
                  ? ''
                  : allComplete
                    ? 'Complete'
                    : worst.state.kind === 'backup' || worst.state.kind === 'waiting' || worst.state.kind === 'unconfirmed'
                      ? (percent(worst.s.percent) ?? shortLabel[worst.state.kind])
                      : shortLabel[worst.state.kind]
                const detail =
                  syncing || !worst || allComplete
                    ? undefined
                    : worst.state.kind === 'backup' || worst.state.kind === 'waiting'
                      ? progressText(worst.s)
                      : `${worst.state.title} · ${worst.d.name || 'Destination'}`
                return (
                  <button key={id} className="row row-button" onClick={() => navigate('sets')}>
                    <span className="row-icon">
                      <FolderClosed size={16} />
                    </span>
                    <span className="row-main">
                      <span className="row-title">{setName}</span>
                      <span className="row-detail">
                        {plural(folders, 'folder')} ·{' '}
                        {set.destinations.map((d) => name(String(d.id))).join(', ') || 'No destination'}
                      </span>
                      {detail && <span className="row-detail">{detail}</span>}
                    </span>
                    {value && <span className="row-value">{value}</span>}
                    <ChevronRight size={16} className="row-chevron" />
                  </button>
                )
              })}
            </>
          )}
        </Section>
      )}

      {ready && (
        <Section
          title="Recent events"
          actions={
            <button className="btn btn-plain btn-sm" onClick={() => navigate('activity')}>
              Show all
            </button>
          }
        >
          {history.isPending ? (
            <Loading />
          ) : recent.length === 0 ? (
            <div className="row">
              <div className="row-main row-detail">No events reported yet.</div>
            </div>
          ) : (
            recent.map((entry, index) => {
              const level = eventLevel(entry.level)
              return (
                <div className="row" key={keys[index]}>
                  <span className={'dot ' + level.dot} aria-hidden="true" />
                  <span className="visually-hidden">{level.label}:</span>
                  <div className="row-main">
                    <div className="row-title truncate" title={entry.message}>
                      {entry.message}
                    </div>
                  </div>
                  <div className="row-value small" title={dateTime(entry.timestamp)}>
                    {relative(entry.timestamp)}
                  </div>
                </div>
              )
            })
          )}
        </Section>
      )}

      {ready && (
        <Section title="Restore">
          <button className="row row-button" onClick={() => navigate('restore')}>
            <span className="row-icon green">
              <History size={16} />
            </span>
            <span className="row-main">
              <span className="row-title">Restore files</span>
              <span className="row-detail">
                Get back files, folders or earlier versions from a backup.
              </span>
            </span>
            <ChevronRight size={16} className="row-chevron" />
          </button>
        </Section>
      )}
    </Page>
  )
}
