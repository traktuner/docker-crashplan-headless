import { LocalArchives } from '../destinations/LocalArchives'
import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowLeft, Folder, File, ChevronRight, RefreshCw } from 'lucide-react'
import { api, APIError } from '../../shared/api'
import type { BackupSet, SelectionPath, SetResult } from '../../shared/api'
import { PatternControl, SettingControl } from '../settings/Settings'

function Notice({ error }: { error: unknown }) {
  return error ? (
    <div className="notice error" role="alert">
      {error instanceof APIError
        ? error.message
        : 'The operation failed. Check the service logs.'}
    </div>
  ) : null
}
function backupSetName(set: BackupSet) {
  return String(set.fields.find((f) => f.name === 'name')?.value ?? set.id)
}
function Result({ result }: { result: SetResult | undefined }) {
  return result ? (
    <div
      className={'notice ' + (result.complete ? 'success' : 'error')}
      role="status"
    >
      {result.complete
        ? 'The native engine confirmed the change.'
        : 'Some changes were not applied. Current native values were read back; no automatic retry occurred.'}
    </div>
  ) : null
}

function SourceSelection({
  set,
  changed,
}: {
  set: BackupSet
  changed: (result: SetResult) => void
}) {
  const [parent, setParent] = useState('')
  const [offset, setOffset] = useState(0)
  const [draft, setDraft] = useState<Record<string, SelectionPath>>({})
  const query = useQuery({
    queryKey: ['source-browser', set.id, parent, offset],
    queryFn: () => api.browseSource(set.id, parent, offset),
  })
  const save = useMutation({
    mutationFn: () => api.changeSelection(set.id, Object.values(draft)),
    onSuccess: (result) => {
      changed(result)
      if (result.complete) setDraft({})
    },
  })
  const scroll = useRef<HTMLDivElement>(null)
  const items = query.data?.items ?? []
  const virtual = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroll.current,
    estimateSize: () => 52,
    overscan: 6,
  })
  function navigate(path: string) {
    setParent(path)
    setOffset(0)
    scroll.current?.scrollTo({ top: 0 })
  }
  return (
    <>
      <div className="restore-paging">
        <button
          className="secondary"
          disabled={parent === '' || query.isFetching}
          onClick={() =>
            navigate(
              parent === '/'
                ? ''
                : parent.slice(0, parent.lastIndexOf('/')) || '/',
            )
          }
        >
          <ArrowLeft size={16} /> Parent
        </button>
        <span>{parent || 'Filesystem roots'}</span>
      </div>
      <Notice error={query.error ?? save.error} />
      {query.isFetching ? (
        <p role="status">Reading directory…</p>
      ) : (
        <div
          className="archive-list"
          ref={scroll}
          aria-label="Backup file selection"
        >
          <div style={{ height: virtual.getTotalSize(), position: 'relative' }}>
            {virtual.getVirtualItems().map((row) => {
              const item = items[row.index]!
              return (
                <div
                  className="archive-row"
                  key={item.path}
                  style={{
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    width: '100%',
                    height: row.size,
                    transform: `translateY(${row.start}px)`,
                  }}
                >
                  <input
                    aria-label={'Back up ' + item.path}
                    type="checkbox"
                    disabled={
                      set.pathsLocked ||
                      item.selectionCode === 'EXCLUDED' ||
                      save.isPending
                    }
                    checked={
                      draft[item.path]?.selected ??
                      item.selectionCode === 'SELECTED'
                    }
                    onChange={(e) =>
                      setDraft((old) => ({
                        ...old,
                        [item.path]: {
                          path: item.path,
                          directory: item.directory,
                          selected: e.target.checked,
                        },
                      }))
                    }
                  />
                  {item.directory ? <Folder size={18} /> : <File size={18} />}
                  <button
                    className="archive-name"
                    disabled={!item.directory || !item.exists}
                    onClick={() => navigate(item.path)}
                    title={item.path}
                  >
                    {item.path}
                  </button>
                  {!item.exists && <span className="badge">Missing</span>}
                  {item.selectionCode === 'EXCLUDED' && (
                    <span className="badge">Excluded by policy or pattern</span>
                  )}
                  {item.directory && <ChevronRight size={16} />}
                </div>
              )
            })}
          </div>
        </div>
      )}
      <div className="restore-paging">
        <button
          className="secondary"
          disabled={offset === 0 || query.isFetching}
          onClick={() => setOffset((n) => Math.max(0, n - 200))}
        >
          Previous page
        </button>
        <button
          className="secondary"
          disabled={
            query.isFetching ||
            offset + items.length >= (query.data?.total ?? 0)
          }
          onClick={() => setOffset((n) => n + 200)}
        >
          Next page
        </button>
        <button
          disabled={
            save.isPending || Object.keys(draft).length === 0 || set.pathsLocked
          }
          onClick={() => save.mutate()}
        >
          Save selection changes
        </button>
      </div>
      <p className="muted">
        Changing a folder also changes the selection of its descendants. Other
        configured paths remain unchanged.
      </p>
      <Result result={save.data} />
      <details>
        <summary>Explicitly configured paths</summary>
        {set.paths.map((path) => (
          <p key={path.path}>
            {path.selected ? 'Included' : 'Excluded'} · {path.path}
          </p>
        ))}
      </details>
    </>
  )
}
function Excludes({
  set,
  changed,
}: {
  set: BackupSet
  changed: (result: SetResult) => void
}) {
  const [patterns, setPatterns] = useState(set.excludes)
  const save = useMutation({
    mutationFn: () => api.setExcludes(set.id, patterns),
    onSuccess: changed,
  })
  return (
    <>
      <p className="muted">
        User exclusions supplement the native system policy. Regular expressions
        use the native Java syntax.
      </p>
      <PatternControl
        value={patterns}
        disabled={set.excludesLocked || save.isPending}
        change={(value) => setPatterns(value as BackupSet['excludes'])}
      />
      <div className="restore-paging">
        <button
          disabled={set.excludesLocked || save.isPending}
          onClick={() => save.mutate()}
        >
          Save exclusions
        </button>
      </div>
      <Notice error={save.error} />
      <Result result={save.data} />
    </>
  )
}
function Destinations({
  set,
  changed,
}: {
  set: BackupSet
  changed: (result: SetResult) => void
}) {
  const devices = useQuery({
    queryKey: ['devices'],
    queryFn: () => api.devices(),
  })
  const save = useMutation({
    mutationFn: ({ id, attached }: { id: string; attached: boolean }) =>
      api.setBackupDestination(set.id, id, attached),
    onSuccess: changed,
  })
  return (
    <>
      <Notice error={devices.error ?? save.error} />
      {devices.data
        ?.filter(
          (d) =>
            d.target ||
            d.availableTarget ||
            set.destinations.some((s) => s.id === d.id),
        )
        .map((device) => {
          const current = set.destinations.find((d) => d.id === device.id)
          return (
            <label className="checkbox destination-option" key={device.id}>
              <input
                type="checkbox"
                checked={!!current}
                disabled={
                  set.destinationsLocked || !!current?.locked || save.isPending
                }
                onChange={(e) => {
                  if (
                    !e.target.checked &&
                    !confirm(
                      'Detach this backup destination from this set? This changes where future backups are sent.',
                    )
                  )
                    return
                  save.mutate({ id: device.id, attached: e.target.checked })
                }}
              />
              {device.name || device.id}
              {device.blocked ? ' (blocked)' : ''}
            </label>
          )
        })}
      <Result result={save.data} />
    </>
  )
}
function SetEditor({
  set,
  changed,
}: {
  set: BackupSet
  changed: (result: SetResult) => void
}) {
  const [tab, setTab] = useState('schedule')
  const [draft, setDraft] = useState<Record<string, unknown>>({})
  const save = useMutation({
    mutationFn: () => api.saveBackupSet(set.id, draft),
    onSuccess: (result) => {
      changed(result)
      if (result.complete) setDraft({})
    },
  })
  return (
    <>
      <div className="restore-paging">
        {[
          'schedule',
          'selection',
          'destinations',
          'exclusions',
          'retention',
        ].map((name) => (
          <button
            className={tab === name ? '' : 'secondary'}
            key={name}
            onClick={() => setTab(name)}
          >
            {name[0]!.toUpperCase() + name.slice(1)}
          </button>
        ))}
      </div>
      {tab === 'schedule' ? (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            save.mutate()
          }}
        >
          <div className="settings-grid">
            {set.fields.map((field) => (
              <div className="setting-field" key={field.name}>
                <div>
                  <label>
                    {field.label}
                    {field.unit ? ' (' + field.unit + ')' : ''}
                  </label>
                  {field.locked && (
                    <span className="badge">Locked by native policy</span>
                  )}
                </div>
                <SettingControl
                  field={field}
                  value={
                    Object.hasOwn(draft, field.name)
                      ? draft[field.name]
                      : field.value
                  }
                  disabled={!field.available || field.locked || save.isPending}
                  change={(value) =>
                    setDraft((old) => ({ ...old, [field.name]: value }))
                  }
                />
              </div>
            ))}
          </div>
          <button disabled={Object.keys(draft).length === 0 || save.isPending}>
            Save set settings
          </button>
          <Notice error={save.error} />
          <Result result={save.data} />
        </form>
      ) : tab === 'selection' ? (
        <SourceSelection set={set} changed={changed} />
      ) : tab === 'destinations' ? (
        <Destinations set={set} changed={changed} />
      ) : tab === 'exclusions' ? (
        <Excludes set={set} changed={changed} />
      ) : (
        <>
          <p className="notice">
            The native REST handler does not apply retention changes. These
            values are read-only; manage retention in the CrashPlan console.
          </p>
          {set.retention
            .filter((f) => f.available)
            .map((field) => (
              <div className="setting-field" key={field.name}>
                <label>
                  {field.label}
                  {field.unit ? ' (' + field.unit + ')' : ''}
                </label>
                <SettingControl
                  field={field}
                  value={field.value}
                  disabled
                  change={() => {}}
                />
              </div>
            ))}
        </>
      )}
    </>
  )
}

export function BackupSets() {
  const client = useQueryClient()
  const [open, setOpen] = useState('')
  const [name, setName] = useState('')
  const sets = useQuery({
    queryKey: ['set-details'],
    queryFn: () => api.backupSetDetails(),
  })
  const scans = useQuery({
    queryKey: ['scans'],
    queryFn: () => api.scans(),
    refetchInterval: 10000,
  })
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['set-details'] })
    void client.invalidateQueries({ queryKey: ['source-browser'] })
  }
  const create = useMutation({
    mutationFn: () => api.createBackupSet(name),
    onSuccess: (set) => {
      setName('')
      setOpen(set.id)
      refresh()
    },
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteBackupSet(id),
    onSuccess: refresh,
  })
  const scan = useMutation({
    mutationFn: (id: string) => api.scan(id),
    onSettled: () => client.invalidateQueries({ queryKey: ['scans'] }),
  })
  const ack = useMutation({
    mutationFn: (id: string) => api.acknowledgeScan(id),
    onSuccess: () => client.invalidateQueries({ queryKey: ['scans'] }),
  })
  function changed(result: SetResult) {
    client.setQueryData<BackupSet[]>(['set-details'], (old) =>
      old?.map((s) => (s.id === result.set.id ? result.set : s)),
    )
    void client.invalidateQueries({ queryKey: ['source-browser'] })
  }
  return (
    <>
      <header className="page-title">
        <h1>Backup sets</h1>
        <p className="muted">
          Choose files, destinations and schedules, and run full filesystem
          scans.
        </p>
      </header>
      <details className="advanced-panel">
        <summary>
          <Folder size={17} />
          <span>Local archive tools</span>
          <span>Create, attach or relocate a local archive</span>
          <ChevronRight size={16} />
        </summary>
        <LocalArchives />
      </details>
      <Notice
        error={
          sets.error ??
          scans.error ??
          create.error ??
          remove.error ??
          scan.error ??
          ack.error
        }
      />
      <section className="card">
        <form
          className="create-set"
          onSubmit={(e) => {
            e.preventDefault()
            create.mutate()
          }}
        >
          <label>
            New backup set
            <input
              value={name}
              maxLength={256}
              required
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <button disabled={create.isPending || !name.trim()}>
            Create set
          </button>
        </form>
      </section>
      {sets.isPending ? (
        <p role="status">Loading backup sets…</p>
      ) : (
        sets.data?.map((set) => {
          const current = scans.data
            ?.filter((s) => s.backupSetId === set.id)
            .at(-1)
          const pending =
            current &&
            ['accepted', 'running', 'indeterminate', 'stalled'].includes(
              current.state,
            )
          return (
            <section className="card" key={set.id}>
              <div className="set-heading">
                <div>
                  <h2>{backupSetName(set)}</h2>
                  <p className="muted">
                    {set.paths.filter((p) => p.selected).length} explicit paths
                    · {set.destinations.length} destinations
                  </p>
                </div>
                <button
                  className="secondary"
                  disabled={scan.isPending || !!pending}
                  onClick={() => scan.mutate(set.id)}
                >
                  <RefreshCw size={16} /> Full scan
                </button>
                <button
                  className="secondary"
                  onClick={() => setOpen(open === set.id ? '' : set.id)}
                >
                  {open === set.id ? 'Close' : 'Configure'}
                </button>
                <button
                  className="secondary"
                  disabled={set.id === '1' || set.locked || remove.isPending}
                  onClick={() => {
                    if (
                      confirm(
                        'Delete backup set ' +
                          backupSetName(set) +
                          '? This changes the backup configuration and can affect its stored backup history.',
                      )
                    )
                      remove.mutate(set.id)
                  }}
                >
                  Delete set
                </button>
              </div>
              {current && (
                <div className="scan-status">
                  <span className="badge">
                    {current.state === 'completed'
                      ? 'Native-recorded completion'
                      : current.state}
                  </span>
                  <span>{new Date(current.updated).toLocaleString()}</span>
                  {['indeterminate', 'stalled'].includes(current.state) && (
                    <button
                      className="secondary"
                      disabled={ack.isPending}
                      onClick={() => {
                        if (
                          confirm(
                            'Acknowledge this unresolved scan? No new scan will be sent.',
                          )
                        )
                          ack.mutate(current.id)
                      }}
                    >
                      Acknowledge
                    </button>
                  )}
                </div>
              )}
              {open === set.id && (
                <SetEditor key={set.id} set={set} changed={changed} />
              )}
            </section>
          )
        })
      )}
    </>
  )
}
