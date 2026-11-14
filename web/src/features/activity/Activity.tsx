import { useMemo, useState } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronRight, Search } from 'lucide-react'
import { api } from '../../shared/api'
import type { Alert, BackupSet, Device, DestinationProgress, SetProgress } from '../../shared/api'
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
import { BackupActions } from '../../shared/BackupActions'
import { CurrentFile, StatusCard, useBackupStatus } from '../../shared/StatusCard'
import { pairStatus, progressText, selectionBytes, selectionFiles, shortLabel } from '../../shared/backupStatus'
import type { BackupStatus } from '../../shared/backupStatus'
import { bytes, compactCount, count, dateTime, percent, rate, relative, words } from '../../shared/format'
import { eventKeys, eventLevel, isBackupEvent, sortAlerts, visibleAlert } from './events'

const toneBadge: Record<BackupStatus['tone'], 'blue' | 'green' | 'orange' | 'red' | undefined> = {
  active: 'blue',
  good: 'green',
  warn: 'orange',
  bad: 'red',
  idle: undefined,
}
/** One backup set at one destination. */
function PairDetails({ d, s }: { d: DestinationProgress; s: SetProgress }) {
  const state = pairStatus(d, s)
  const share = percent(s.percent)
  const files = selectionFiles(s)
  return (
    <div className="pair">
      <Row
        title={<strong>{d.name || 'Destination'}</strong>}
        detail={state.kind === 'complete' ? undefined : state.detail}
        value={
          <span className="pair-state">
            <Badge tone={toneBadge[state.tone]}>{shortLabel[state.kind]}</Badge>
            {share && state.kind !== 'complete' && <span>{share}</span>}
          </span>
        }
      />
      <Row
        title="Selected"
        value={
          selectionBytes(s) > 0n
            ? `${count(files.toString())} files · ${bytes(selectionBytes(s).toString())}`
            : 'Not scanned yet'
        }
      />
      <Row title="Remaining" value={progressText(s)} />
      <Row
        title="Last backup"
        value={Number(s.lastBackup) > 0 ? <span title={dateTime(s.lastBackup)}>{relative(s.lastBackup)}</span> : 'Not reported yet'}
      />
      {Number(s.nextBackup) > 0 && <Row title="Next backup" value={dateTime(s.nextBackup)} />}
      {s.sendingPath && (
        <div className="row">
          <CurrentFile path={s.sendingPath} />
        </div>
      )}
      {(state.actions.run || state.actions.pause || state.actions.resume) && (
        <div className="row">
          <BackupActions
            actions={state.actions}
            backupSetId={s.backupSetId}
            destinationId={d.targetId}
            destinationName={d.name}
            systemPaused={state.kind === 'system-paused'}
            small
          />
        </div>
      )}
    </div>
  )
}

function connection(d: DestinationProgress) {
  if (d.connected === true) return { label: 'Connected', tone: 'green' as const }
  if (d.connected === false)
    return d.disconnectCode && d.disconnectCode !== 'DISCONNECTED'
      ? { label: 'Disconnected', tone: 'red' as const }
      : { label: 'Waiting for connection', tone: 'orange' as const }
  return undefined
}

function DestinationRow({ d, device }: { d: DestinationProgress; device?: Device }) {
  const client = useQueryClient()
  const reconnect = useMutation({
    mutationFn: () => api.reconnectDestination(d.targetId),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['backup-progress'] })
      void client.invalidateQueries({ queryKey: ['devices'] })
    },
  })
  const state = connection(d)
  const activity = d.validating
    ? 'Syncing backup index' + (d.sync && d.sync.percent >= 0 ? ` · ${percent(d.sync.percent)}` : '') + (d.sync ? ` · ${compactCount(d.sync.current)} archive entries read` : '')
    : d.maintenance
      ? 'Archive maintenance'
      : d.backingUp
        ? 'Backing up · ' + rate(d.completedRateBytesPerSecond)
        : undefined
  const used = device?.usedBytes
  const quota = device?.quotaBytes
  const usage = used ? (quota && quota !== '0' ? `${bytes(used)} of ${bytes(quota)} used` : `${bytes(used)} used`) : undefined
  return (
    <>
      <Row
        title={d.name || 'Destination'}
        detail={[activity, usage, device?.location].filter(Boolean).join(' · ') || undefined}
        value={state && <Badge tone={state.tone}>{state.label}</Badge>}
      >
        {d.connected === false && (
          <button className="btn btn-sm" disabled={reconnect.isPending} onClick={() => reconnect.mutate()}>
            {reconnect.isPending ? 'Reconnecting…' : 'Reconnect'}
          </button>
        )}
      </Row>
      {d.error && (
        <div className="row-block">
          <Notice tone="warning">CrashPlan did not report the state of this destination. It is asked again shortly.</Notice>
        </div>
      )}
      {reconnect.error && (
        <div className="row-block">
          <ErrorNotice error={reconnect.error} />
        </div>
      )}
      {reconnect.isSuccess && (
        <div className="row-block">
          <span className="row-detail" role="status">Reconnect requested.</span>
        </div>
      )}
    </>
  )
}

const alertActions = { resume: 'Resume backup', retry: 'Try again', reconnect: 'Reconnect' } as const

function Alerts({ alerts, progress }: { alerts: Alert[]; progress: DestinationProgress[] | undefined }) {
  const client = useQueryClient()
  const dismiss = useMutation({
    mutationFn: (id: string) => api.dismissAlert(id),
    onSettled: () => client.invalidateQueries({ queryKey: ['alerts'] }),
  })
  const offline = (progress ?? []).filter((d) => d.connected === false)
  const act = useMutation({
    mutationFn: async (action: NonNullable<Alert['action']>) => {
      if (action === 'resume') await api.backupControl('resume', {})
      else if (action === 'retry') await api.runBackup({})
      else for (const d of offline) await api.reconnectDestination(d.targetId)
    },
    onSettled: () => {
      for (const key of ['alerts', 'backup-progress', 'backup-summary'])
        void client.invalidateQueries({ queryKey: [key] })
    },
  })
  return (
    <Section title="Alerts">
      <ErrorNotice error={dismiss.error ?? act.error} />
      {alerts.map((alert) => {
        const action = alert.action && (alert.action !== 'reconnect' || offline.length > 0) ? alert.action : undefined
        return (
          <Row
            key={alert.id}
            title={alert.title || words(alert.name)}
            detail={alert.message && alert.message !== alert.name ? alert.message : undefined}
          >
            {action && (
              <button className="btn btn-sm btn-primary" disabled={act.isPending} onClick={() => act.mutate(action)}>
                {alertActions[action]}
              </button>
            )}
            {alert.closable !== false && (
              <button className="btn btn-sm" disabled={dismiss.isPending} onClick={() => dismiss.mutate(alert.id)}>
                Dismiss
              </button>
            )}
          </Row>
        )
      })}
    </Section>
  )
}

function setLabel(sets: BackupSet[] | undefined, id: string) {
  const name = sets?.find((s) => String(s.id) === id)?.fields.find((f) => f.name === 'name')?.value
  return typeof name === 'string' && name.trim() ? name : 'Backup set'
}

const firstLoad = 500

export function Activity({ signedIn }: { signedIn: boolean | undefined }) {
  const { status, progress } = useBackupStatus(signedIn)
  const ready = signedIn === true
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
  const alerts = useQuery({
    queryKey: ['alerts'],
    queryFn: () => api.alerts(),
    refetchInterval: 30_000,
    enabled: ready,
  })
  const [limit, setLimit] = useState(firstLoad)
  const history = useQuery({
    queryKey: ['history', limit],
    queryFn: () => api.history(limit),
    refetchInterval: 30_000,
    enabled: ready,
    placeholderData: keepPreviousData,
  })
  const [filter, setFilter] = useState<'all' | 'backup' | 'problems'>('all')
  const [query, setQuery] = useState('')
  const [shown, setShown] = useState(100)
  const loaded = history.data ?? []
  const events = useMemo(() => {
    const term = query.trim().toLowerCase()
    const keys = eventKeys(loaded)
    return loaded
      .map((entry, i) => ({ entry, key: keys[i]! }))
      .toReversed()
      .filter(({ entry: e }) =>
        filter === 'backup' ? isBackupEvent(e.message) : filter === 'problems' ? e.level !== 'I' : true,
      )
      .filter(({ entry: e }) => !term || e.message.toLowerCase().includes(term))
  }, [loaded, filter, query])
  // The server returns at most `limit` entries; a full page means older ones exist.
  const moreOnServer = loaded.length === limit && limit < 50_000
  const visibleAlerts = sortAlerts(alerts.data?.filter((a) => visibleAlert(a.name)) ?? [])
  const destinations = progress.data ?? []
  const setIds = [
    ...(sets.data?.map((s) => String(s.id)) ?? []),
    ...destinations.flatMap((d) => d.sets.map((s) => s.backupSetId)),
  ].filter((id, i, all) => all.indexOf(id) === i)

  return (
    <Page title="Activity" subtitle="Live progress, alerts and the CrashPlan event log.">
      <StatusCard status={status} controls={ready} />
      <ErrorNotice error={progress.error} />

      {visibleAlerts.length > 0 && <Alerts alerts={visibleAlerts} progress={progress.data} />}
      {alerts.error && <ErrorNotice error={alerts.error} />}

      {setIds.map((id) => {
        const pairs = destinations.flatMap((d) => (d.error ? [] : d.sets.filter((s) => s.backupSetId === id).map((s) => ({ d, s }))))
        if (pairs.length === 0) return null
        return (
          <Section
            key={id}
            title={setLabel(sets.data, id)}
            footer={
              pairs.some(({ d }) => d.validating)
                ? 'Selected and remaining amounts come from before the synchronization and are updated when it finishes.'
                : undefined
            }
          >
            {pairs.map(({ d, s }) => (
              <PairDetails key={d.targetId} d={d} s={s} />
            ))}
          </Section>
        )
      })}

      {destinations.length > 0 && (
        <Section title="Destinations">
          {destinations.map((d) => (
            <DestinationRow key={d.targetId} d={d} device={devices.data?.find((x) => String(x.id) === String(d.targetId))} />
          ))}
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
          <div className="row search-row">
            <Search size={15} className="row-chevron" aria-hidden="true" />
            <input
              className="input input-bare"
              type="search"
              enterKeyHint="search"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              placeholder="Search events"
              aria-label="Search events"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {history.isPending ? (
            <Loading />
          ) : (
            <div className="list-scroll log-list">
              <ErrorNotice error={history.error} />
              {events.length === 0 && <div className="row row-detail">No events match.</div>}
              {events.slice(0, shown).map(({ entry, key }) => {
                const level = eventLevel(entry.level)
                return (
                  <details className="log-row" key={key}>
                    <summary>
                      <time dateTime={new Date(Number(entry.timestamp)).toISOString()}>{dateTime(entry.timestamp)}</time>
                      <span className="log-level">
                        <span className={'badge ' + (level.tone ?? '')}>{level.label}</span>
                      </span>
                      <span className="truncate log-msg">{entry.message}</span>
                      <ChevronRight size={14} className="chev" aria-hidden="true" />
                    </summary>
                    <div className="log-body">{entry.message}</div>
                  </details>
                )
              })}
              {(events.length > shown || moreOnServer) && (
                <div className="row">
                  <button
                    className="btn btn-plain btn-sm"
                    disabled={history.isFetching}
                    onClick={() => {
                      if (events.length > shown) setShown(shown + 200)
                      else {
                        setLimit((n) => Math.min(50_000, n * 4))
                        setShown(shown + 200)
                      }
                    }}
                  >
                    {history.isFetching && events.length <= shown
                      ? 'Loading older events…'
                      : events.length > shown
                        ? `Show older events (${count(events.length - shown)} more loaded)`
                        : 'Show older events'}
                  </button>
                </div>
              )}
            </div>
          )}
        </Section>
      )}
      {signedIn === false && <Notice tone="warning">Sign in to CrashPlan to see backup activity.</Notice>}
    </Page>
  )
}
