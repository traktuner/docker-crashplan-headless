import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { api, APIError } from '../../shared/api'
import type { HistoryEntry } from '../../shared/api'

function Failure({ error }: { error: unknown }) {
  return error ? (
    <p className="notice error" role="alert">
      {error instanceof APIError ? error.message : 'Activity is unavailable.'}
    </p>
  ) : null
}
export function Activity() {
  const client = useQueryClient()
  const summary = useQuery({
    queryKey: ['backup-summary'],
    queryFn: () => api.backupSummary(),
    refetchInterval: 15000,
  })
  const sets = useQuery({
    queryKey: ['set-details'],
    queryFn: () => api.backupSetDetails(),
  })
  const alerts = useQuery({ queryKey: ['alerts'], queryFn: () => api.alerts() })
  const history = useQuery({
    queryKey: ['history'],
    queryFn: () => api.history(),
  })
  const [set, setSet] = useState('')
  const [minutes, setMinutes] = useState('60')
  const [entry, setEntry] = useState<HistoryEntry>()
  const control = useMutation({
    mutationFn: (action: 'pause' | 'resume') =>
      api.backupControl(action, {
        ...(set ? { backupSetId: set } : {}),
        ...(action === 'pause' ? { durationMinutes: minutes } : {}),
      }),
    onSettled: () => client.invalidateQueries({ queryKey: ['backup-summary'] }),
  })
  const dismiss = useMutation({
    mutationFn: (id: string) => api.dismissAlert(id),
    onSettled: () => client.invalidateQueries({ queryKey: ['alerts'] }),
  })
  const scroll = useRef<HTMLDivElement>(null)
  const entries = (history.data ?? []).toReversed()
  const virtual = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scroll.current,
    estimateSize: () => 72,
    overscan: 6,
  })
  return (
    <>
      <header className="page-title">
        <h1>Backup activity</h1>
        <p className="muted">
          Current transfer, backup controls, alerts and native history.
        </p>
      </header>
      <Failure error={summary.error ?? sets.error ?? control.error} />
      {summary.data && (
        <div className="metric-grid">
          <section className="card">
            <h2>{summary.data.status}</h2>
            <p>{summary.data.sendRateBytesPerSecond} B/s</p>
            <p className="muted">
              {summary.data.systemBusy
                ? 'System busy'
                : summary.data.batteryBlocked
                  ? 'Blocked by battery policy'
                  : 'Native backup status'}
            </p>
          </section>
          <section className="card">
            <h2>{summary.data.remainingFiles.toLocaleString()} files</h2>
            <p className="muted">
              {summary.data.remainingBytes} bytes remaining
            </p>
          </section>
          <section className="card">
            <h2>
              {summary.data.filesBackedUp.toLocaleString()} files backed up
            </h2>
            <p className="muted">
              Last backup:{' '}
              {Number(summary.data.lastBackup) > 0
                ? new Date(Number(summary.data.lastBackup)).toLocaleString()
                : 'Not available'}
            </p>
          </section>
        </div>
      )}
      <section className="card form-card">
        <h2>Pause or resume backup</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            control.mutate('pause')
          }}
        >
          <label>
            Backup scope
            <select value={set} onChange={(e) => setSet(e.target.value)}>
              <option value="">All backup sets</option>
              {sets.data?.map((item) => (
                <option key={item.id} value={item.id}>
                  {String(
                    item.fields.find((f) => f.name === 'name')?.value ??
                      item.id,
                  )}
                </option>
              ))}
            </select>
          </label>
          <label>
            Pause duration (minutes)
            <input
              required
              inputMode="numeric"
              pattern="[1-9][0-9]*"
              value={minutes}
              onChange={(e) => setMinutes(e.target.value)}
            />
          </label>
          <div className="restore-paging">
            <button disabled={control.isPending}>Pause backup</button>
            <button
              className="secondary"
              type="button"
              disabled={control.isPending}
              onClick={() => control.mutate('resume')}
            >
              Resume backup
            </button>
          </div>
        </form>
        {control.isSuccess && (
          <p className="notice success">
            The native engine accepted the request for{' '}
            {control.data.backupSetIds.length} backup sets.
          </p>
        )}
      </section>
      <section className="card">
        <h2>Alerts</h2>
        <Failure error={alerts.error ?? dismiss.error} />
        {alerts.data?.length === 0 && (
          <p className="muted">No native alerts.</p>
        )}
        {alerts.data?.map((alert) => (
          <article className="restore-job" key={alert.id}>
            <h3>{alert.title || alert.name}</h3>
            <p>{alert.message || alert.name}</p>
            <button
              className="secondary"
              disabled={dismiss.isPending}
              onClick={() => dismiss.mutate(alert.id)}
            >
              Dismiss
            </button>
          </article>
        ))}
      </section>
      <section className="card">
        <h2>Backup history</h2>
        <Failure error={history.error} />
        <button
          className="secondary"
          onClick={() => void history.refetch()}
          disabled={history.isFetching}
        >
          Refresh history
        </button>
        <div
          ref={scroll}
          className="history-list"
          role="list"
          aria-label="Backup history"
        >
          <div style={{ height: virtual.getTotalSize(), position: 'relative' }}>
            {virtual.getVirtualItems().map((row) => {
              const item = entries[row.index]!
              return (
                <button
                  role="listitem"
                  className="history-row"
                  key={row.index}
                  onClick={() => setEntry(item)}
                  style={{
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    width: '100%',
                    height: row.size,
                    transform: `translateY(${row.start}px)`,
                  }}
                >
                  <span className="muted">
                    {new Date(Number(item.timestamp)).toLocaleString()} ·{' '}
                    {item.level}
                  </span>
                  <span>{item.message}</span>
                </button>
              )
            })}
          </div>
        </div>
        {entry && (
          <section className="version-panel">
            <h3>History entry</h3>
            <pre className="history-detail">{entry.message}</pre>
            <button className="secondary" onClick={() => setEntry(undefined)}>
              Close entry
            </button>
          </section>
        )}
      </section>
    </>
  )
}
