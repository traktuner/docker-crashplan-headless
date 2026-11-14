import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronRight, Search } from 'lucide-react'
import { api } from '../../shared/api'
import type { DestinationProgress } from '../../shared/api'
import {
  Badge,
  ErrorNotice,
  Loading,
  Notice,
  Page,
  Row,
  Section,
  Segmented,
} from '../../shared/components'
import { StatusCard, useBackupStatus } from '../../shared/StatusCard'
import { completion } from '../../shared/backupStatus'
import {
  bytes,
  count,
  dateTime,
  percent,
  rate,
  relative,
  words,
} from '../../shared/format'
import { eventLevel, isBackupEvent, visibleAlert } from './events'

const pauseOptions = [
  { value: '15', label: '15 min' },
  { value: '60', label: '1 hour' },
  { value: '240', label: '4 hours' },
  { value: '1440', label: '1 day' },
]

function DestinationDetails({
  destination,
  setName,
  syncing,
}: {
  destination: DestinationProgress
  setName: (id: string) => string
  syncing: boolean
}) {
  return (
    <Section
      title={destination.name || 'Destination'}
      description={
        destination.validating
          ? 'Synchronizing with the destination'
          : destination.backingUp
            ? 'Backing up · ' + rate(destination.completedRateBytesPerSecond)
            : undefined
      }
      footer={
        syncing
          ? 'Selected and remaining amounts come from before the synchronization and are updated when it finishes.'
          : undefined
      }
    >
      {destination.sets.length === 0 && (
        <Row title="No backup set uses this destination" />
      )}
      {destination.sets.map((set) => {
        const done = completion(set)
        return (
          <div key={set.backupSetId}>
            <Row
              title={<strong>{setName(set.backupSetId)}</strong>}
              value={
                set.scanning ? (
                  <Badge tone="blue">Scanning</Badge>
                ) : done !== undefined ? (
                  percent(done) + ' backed up'
                ) : undefined
              }
            />
            <Row title="Selected" value={`${count(set.filesScanned)} files · ${bytes(set.bytesScanned)}`} />
            <Row title="To do" value={`${count(set.remainingFiles)} files · ${bytes(set.remainingBytes)}`} />
            <Row
              title="Last backup"
              value={
                Number(set.lastBackup) > 0
                  ? `${relative(set.lastBackup)} (${dateTime(set.lastBackup)})`
                  : 'Not reported yet'
              }
            />
            {Number(set.nextBackup) > 0 && (
              <Row title="Next backup" value={dateTime(set.nextBackup)} />
            )}
            {set.sendingPath && (
              <Row title="Current file" detail={set.sendingPath} />
            )}
          </div>
        )
      })}
    </Section>
  )
}

export function Activity({ signedIn }: { signedIn: boolean | undefined }) {
  const client = useQueryClient()
  const { status, progress } = useBackupStatus(signedIn)
  const ready = signedIn === true
  const sets = useQuery({
    queryKey: ['set-details'],
    queryFn: () => api.backupSetDetails(),
    enabled: ready,
  })
  const alerts = useQuery({
    queryKey: ['alerts'],
    queryFn: () => api.alerts(),
    refetchInterval: 30_000,
    enabled: ready,
  })
  const history = useQuery({
    queryKey: ['history'],
    queryFn: () => api.history(),
    refetchInterval: 30_000,
    enabled: ready,
  })
  const [scope, setScope] = useState('')
  const [minutes, setMinutes] = useState('60')
  const control = useMutation({
    mutationFn: (action: 'pause' | 'resume') =>
      api.backupControl(action, {
        ...(scope ? { backupSetId: scope } : {}),
        ...(action === 'pause' ? { durationMinutes: minutes } : {}),
      }),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['backup-progress'] })
      void client.invalidateQueries({ queryKey: ['backup-summary'] })
    },
  })
  const dismiss = useMutation({
    mutationFn: (id: string) => api.dismissAlert(id),
    onSettled: () => client.invalidateQueries({ queryKey: ['alerts'] }),
  })
  const [filter, setFilter] = useState<'all' | 'backup' | 'problems'>('all')
  const [query, setQuery] = useState('')
  const [shown, setShown] = useState(100)
  const events = useMemo(() => {
    const term = query.trim().toLowerCase()
    return (history.data ?? [])
      .toReversed()
      .filter((e) =>
        filter === 'backup'
          ? isBackupEvent(e.message)
          : filter === 'problems'
            ? e.level !== 'I'
            : true,
      )
      .filter((e) => !term || e.message.toLowerCase().includes(term))
  }, [history.data, filter, query])
  const setName = (id: string) =>
    String(
      sets.data
        ?.find((s) => String(s.id) === id)
        ?.fields.find((f) => f.name === 'name')?.value ?? 'Backup set ' + id,
    )
  const visibleAlerts = alerts.data?.filter((a) => visibleAlert(a.name)) ?? []

  return (
    <Page title="Activity" subtitle="Live progress, alerts and the CrashPlan event log.">
      <StatusCard status={status} />
      <ErrorNotice error={progress.error} />

      {progress.data?.map((destination) => (
        <DestinationDetails
          key={destination.targetId}
          destination={destination}
          setName={setName}
          syncing={destination.validating}
        />
      ))}

      {visibleAlerts.length > 0 && (
        <Section title="Alerts">
          <ErrorNotice error={alerts.error ?? dismiss.error} />
          {visibleAlerts.map((alert) => (
            <Row
              key={alert.id}
              title={alert.title || words(alert.name)}
              detail={alert.message && alert.message !== alert.name ? alert.message : undefined}
            >
              <button
                className="btn btn-sm"
                disabled={dismiss.isPending}
                onClick={() => dismiss.mutate(alert.id)}
              >
                Dismiss
              </button>
            </Row>
          ))}
        </Section>
      )}

      {ready && (
        <Section
          title="Pause backup"
          description="Backup resumes automatically after the chosen time."
        >
          {(sets.data?.length ?? 0) > 1 && (
            <Row title="Backup set">
              <select className="select" value={scope} onChange={(e) => setScope(e.target.value)}>
                <option value="">All backup sets</option>
                {sets.data?.map((item) => (
                  <option key={String(item.id)} value={String(item.id)}>
                    {setName(String(item.id))}
                  </option>
                ))}
              </select>
            </Row>
          )}
          <Row title="Pause for">
            <Segmented label="Pause duration" value={minutes} options={pauseOptions} onChange={setMinutes} />
          </Row>
          <div className="row">
            <div className="row-main">
              {control.isSuccess && (
                <span className="row-detail">
                  {control.variables === 'pause' ? 'Backup paused.' : 'Backup resumed.'}
                </span>
              )}
              <ErrorNotice error={control.error} />
            </div>
            <div className="button-row">
              <button className="btn" disabled={control.isPending} onClick={() => control.mutate('resume')}>
                Resume now
              </button>
              <button className="btn btn-primary" disabled={control.isPending} onClick={() => control.mutate('pause')}>
                Pause
              </button>
            </div>
          </div>
        </Section>
      )}

      {ready && (
        <Section
          title="Event log"
          description="Everything CrashPlan reported, newest first. Select an entry to read all of it."
          actions={
            <Segmented
              label="Show events"
              value={filter}
              onChange={(v) => {
                setFilter(v)
                setShown(100)
              }}
              options={[
                { value: 'all', label: 'All' },
                { value: 'backup', label: 'Backup' },
                { value: 'problems', label: 'Problems' },
              ]}
            />
          }
        >
          <div className="row">
            <Search size={15} className="row-chevron" />
            <input
              className="input"
              style={{ border: 0, boxShadow: 'none', padding: 0, minHeight: 24 }}
              placeholder="Search events"
              aria-label="Search events"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {history.isPending ? (
            <Loading />
          ) : (
            <div className="list-scroll" style={{ borderTop: '1px solid var(--separator)' }}>
              <ErrorNotice error={history.error} />
              {events.length === 0 && (
                <div className="row row-detail">No events match.</div>
              )}
              {events.slice(0, shown).map((entry, index) => {
                const level = eventLevel(entry.level)
                return (
                  <details className="log-row" key={entry.timestamp + ':' + index}>
                    <summary>
                      <time dateTime={new Date(Number(entry.timestamp)).toISOString()}>
                        {dateTime(entry.timestamp)}
                      </time>
                      <span>
                        <span className={'badge ' + (level.tone ?? '')}>{level.label}</span>
                      </span>
                      <span className="truncate">{entry.message}</span>
                      <ChevronRight size={14} className="chev" />
                    </summary>
                    <div className="log-body">{entry.message}</div>
                  </details>
                )
              })}
              {events.length > shown && (
                <div className="row">
                  <button className="btn btn-plain btn-sm" onClick={() => setShown(shown + 200)}>
                    Show older events ({count(events.length - shown)} more)
                  </button>
                </div>
              )}
            </div>
          )}
        </Section>
      )}
      {signedIn === false && (
        <Notice tone="warning">Sign in to CrashPlan to see backup activity.</Notice>
      )}
    </Page>
  )
}
