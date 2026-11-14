import { newRequestId } from '../../shared/requestId'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ChevronLeft,
  ChevronRight,
  Download,
  File,
  Folder,
  HardDrive,
  Info,
  Search,
  X,
} from 'lucide-react'
import { api, identifier, archiveSelection, archiveSearchQuery } from '../../shared/api'
import type { ArchiveItem, ArchiveQuery, RestoreRequest, RestoreSelection } from '../../shared/api'
import {
  Badge,
  Empty,
  ErrorNotice,
  Field,
  Loading,
  Notice,
  Page,
  Progress,
  Row,
  Section,
  Segmented,
  Switch,
} from '../../shared/components'
import { baseName, bytes, count, dateTime, plural } from '../../shared/format'
import {
  deviceLabel,
  insideSelected,
  restoreDestinations,
  restoreSets,
  restoreSources,
  toggleSelection,
} from './model'

const existingOptions = [
  { value: 'RENAME_ORIGINAL', label: 'Keep both (rename the existing file)' },
  { value: 'OVERWRITE_ORIGINAL', label: 'Replace the existing file' },
  { value: 'SKIP_ALL', label: 'Skip the file' },
  { value: 'SKIP_IF_NEWER', label: 'Skip if the existing file is newer' },
  { value: 'SKIP_IF_OLDER', label: 'Skip if the existing file is older' },
]

function ItemDetails({
  item,
  query,
  selection,
  canExclude,
  disabled,
  onVersion,
  onExclude,
  onClose,
}: {
  item: ArchiveItem
  query: ArchiveQuery
  selection?: RestoreSelection
  canExclude: boolean
  disabled: boolean
  onVersion: (timestamp: string, isFolder: boolean) => void
  onExclude: () => void
  onClose: () => void
}) {
  const versions = useQuery({
    queryKey: ['archive-versions', query, item.fileId],
    queryFn: () => api.archiveVersions({ ...query, fileId: item.fileId, cursor: undefined }),
    enabled: !item.isFolder,
  })
  const contents = useMutation({
    mutationFn: () => api.archiveContents({ ...query, fileId: item.fileId, cursor: undefined }),
  })
  return (
    <div className="row-block" style={{ background: 'var(--surface-2)' }}>
      <div className="button-row" style={{ justifyContent: 'space-between' }}>
        <strong className="truncate" title={item.path}>
          {item.path}
        </strong>
        <button className="icon-btn" aria-label="Close details" onClick={onClose}>
          <X size={16} />
        </button>
      </div>
      {!item.isFolder && (
        <Field label="Version to restore">
          {versions.isPending ? (
            <Loading label="Loading versions…" />
          ) : (
            <select
              className="select"
              disabled={disabled}
              value={selection && selection.timestamp !== query.at ? selection.timestamp : ''}
              onChange={(e) => {
                if (!e.target.value) return onVersion(query.at, item.isFolder)
                const v = versions.data?.find((x) => x.timestamp === e.target.value)
                if (v) onVersion(v.timestamp, v.isFolder)
              }}
            >
              <option value="">Latest version</option>
              {versions.data?.map((v) => (
                <option key={v.timestamp} value={v.timestamp}>
                  {dateTime(v.timestamp)} · {bytes(v.size)}
                </option>
              ))}
            </select>
          )}
        </Field>
      )}
      <ErrorNotice error={versions.error} />
      <div className="button-row">
        {item.isFolder && (
          <button className="btn btn-sm" disabled={contents.isPending} onClick={() => contents.mutate()}>
            {contents.isPending ? 'Calculating…' : 'Calculate size'}
          </button>
        )}
        {canExclude && (
          <button className="btn btn-sm" disabled={disabled} onClick={onExclude}>
            Leave out of restore
          </button>
        )}
      </div>
      {contents.error && <Notice tone="error">The size could not be calculated.</Notice>}
      {contents.data && (
        <span className="row-detail">
          {plural(Number(contents.data.files), 'file')}, {plural(Number(contents.data.directories), 'folder')},{' '}
          {bytes(contents.data.bytes)}
          {contents.data.done ? '' : ' so far — CrashPlan is still counting. Calculate again for the full result.'}
        </span>
      )}
    </div>
  )
}

export function Restore() {
  const client = useQueryClient()
  const [source, setSource] = useState('')
  const [destination, setDestination] = useState('')
  const [set, setSet] = useState('')
  const [includeDeleted, setIncludeDeleted] = useState(false)
  const [when, setWhen] = useState<'latest' | 'date'>('latest')
  const [at, setAt] = useState('')
  const [pattern, setPattern] = useState('')
  const [searchPattern, setSearchPattern] = useState('')
  const [trail, setTrail] = useState<{ name: string; fileId: string }[]>([])
  const [cursors, setCursors] = useState<string[]>([])
  const [cursor, setCursor] = useState<string>()
  const [selection, setSelection] = useState<Record<string, RestoreSelection>>({})
  const [excluded, setExcluded] = useState<Record<string, RestoreSelection>>({})
  const [rootId, setRootId] = useState('')
  const [restoreMode, setRestoreMode] = useState<'folder' | 'original'>('folder')
  const [relative, setRelative] = useState('')
  const [existing, setExisting] = useState('RENAME_ORIGINAL')
  const [permissions, setPermissions] = useState<'current' | 'original'>('current')
  const [details, setDetails] = useState<ArchiveItem>()
  const [attempt, setAttempt] = useState<RestoreRequest>()
  const restoreSection = useRef<HTMLDivElement>(null)

  const devices = useQuery({ queryKey: ['devices'], queryFn: () => api.devices() })
  const sources = restoreSources(devices.data)
  const destinations = restoreDestinations(devices.data)
  // Preselect this device and a single destination, like the desktop app.
  useEffect(() => {
    if (!source && sources.length) setSource(String(sources[0]!.id))
    if (!destination && destinations.length === 1) setDestination(String(destinations[0]!.id))
  }, [devices.data]) // eslint-disable-line react-hooks/exhaustive-deps
  const archiveSets = useQuery({
    queryKey: ['archive-sets', source, destination],
    queryFn: () => api.archiveSets(source, destination),
    enabled: !!source && !!destination,
  })
  const sets = restoreSets(archiveSets.data?.map((s) => ({ id: String(s.id), name: s.name })))
  useEffect(() => {
    if (sets && !sets.some((s) => s.id === set)) setSet(sets[0]?.id ?? '')
  }, [archiveSets.data]) // eslint-disable-line react-hooks/exhaustive-deps

  const cutoff = when === 'date' && at ? new Date(at).getTime() : 0
  const badDate = when === 'date' && (!at || !Number.isSafeInteger(cutoff) || cutoff <= 0)
  const base = useMemo<ArchiveQuery | undefined>(() => {
    if (!source || !destination || !set || badDate) return undefined
    try {
      return {
        sourceId: identifier(source),
        targetId: identifier(destination),
        backupSetId: identifier(set),
        fileId: '',
        at: String(cutoff),
        includeDeleted,
        limit: 200,
      }
    } catch {
      return undefined
    }
  }, [source, destination, set, cutoff, includeDeleted, badDate])
  // A new archive view starts at its top level with nothing selected.
  const baseKey = JSON.stringify(base)
  useEffect(() => {
    setTrail([])
    setCursor(undefined)
    setCursors([])
    setSelection({})
    setExcluded({})
    setDetails(undefined)
    setSearchPattern('')
    setPattern('')
  }, [baseKey])
  const query = base && {
    ...base,
    fileId: trail.at(-1)?.fileId ?? '',
    cursor,
  }
  const archiveType = devices.data?.find((d) => String(d.id) === destination)?.archiveType
  const browse = useQuery({
    queryKey: ['archive', query],
    queryFn: () => api.browseArchive(query!),
    enabled: !!query && !searchPattern,
    staleTime: 60_000,
  })
  const search = useQuery({
    queryKey: ['archive-search', query, searchPattern],
    queryFn: () => api.searchArchive(archiveSearchQuery({ ...query!, pattern: searchPattern, fullPath: searchPattern.includes('/') }, archiveType)),
    enabled: !!query && !!searchPattern,
    staleTime: 60_000,
  })
  const page = searchPattern ? search : browse
  const items = page.data?.items ?? []

  const roots = useQuery({ queryKey: ['restore-roots'], queryFn: () => api.restoreRoots() })
  useEffect(() => {
    const writable = roots.data?.filter((r) => r.writable)
    if (!rootId && writable?.length === 1) setRootId(String(writable[0]!.id))
  }, [roots.data]) // eslint-disable-line react-hooks/exhaustive-deps
  const jobs = useQuery({
    queryKey: ['restore-jobs'],
    queryFn: () => api.restoreJobs(),
    refetchInterval: (q) => (q.state.data?.some((j) => !j.completed && !j.canceled) ? 3_000 : 15_000),
  })
  const operations = useQuery({ queryKey: ['restore-operations'], queryFn: () => api.restoreOperations(), refetchInterval: 15_000 })
  const helper = useQuery({ queryKey: ['restore-helper'], queryFn: () => api.restoreHelper(), refetchInterval: 15_000 })
  const archiveOperations = useMutation({
    mutationFn: (ids: string[]) => api.archiveRestoreOperations(ids),
    onSettled: () => client.invalidateQueries({ queryKey: ['restore-operations'] }),
  })
  const start = useMutation({
    mutationFn: (request: RestoreRequest) =>
      restoreMode === 'original' ? api.startOriginalRestore(request) : api.startRestore(request),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['restore-operations'] })
      void client.invalidateQueries({ queryKey: ['restore-jobs'] })
    },
  })
  const control = useMutation({
    mutationFn: ({ id, action }: { id: string; action: 'start' | 'stop' | 'remove' }) => api.controlRestore(id, action),
    onSettled: () => client.invalidateQueries({ queryKey: ['restore-jobs'] }),
  })

  function open(item: ArchiveItem) {
    setSearchPattern('')
    setTrail([...trail, { name: baseName(item.path), fileId: item.fileId }])
    setCursor(undefined)
    setCursors([])
    setDetails(undefined)
  }
  function goTo(index: number) {
    setSearchPattern('')
    setTrail(trail.slice(0, index))
    setCursor(undefined)
    setCursors([])
    setDetails(undefined)
  }
  function toggle(item: ArchiveItem) {
    const result = toggleSelection(selection, excluded, item.fileId, archiveSelection(item, query!.backupSetId, query!.at))
    setSelection(result.selection)
    setExcluded(result.excluded)
  }
  function submit(e: FormEvent) {
    e.preventDefault()
    if (!query || attempt || Object.keys(selection).length === 0 || (restoreMode === 'folder' && rootId === '')) return
    const request: RestoreRequest = {
      requestId: newRequestId(),
      sourceId: query.sourceId,
      targetId: query.targetId,
      target: { rootId, relative },
      existing,
      permissions,
      includeDeleted: query.includeDeleted,
      items: Object.values(selection),
      excluded: Object.values(excluded),
    }
    setAttempt(request)
    start.mutate(request)
  }
  const selected = Object.keys(selection).length
  const openJobs = jobs.data?.filter((j) => !j.completed && !j.canceled) ?? []
  const pastJobs = jobs.data?.filter((j) => j.completed || j.canceled) ?? []
  const helperProblem = helper.data && !['waiting', 'running', 'idle'].includes(helper.data.state)
  const writableRoots = roots.data?.filter((r) => r.writable) ?? []

  return (
    <Page title="Restore" subtitle="Choose files from a backup and put them back.">
      <ErrorNotice error={devices.error ?? archiveSets.error} />
      <Section title="Restore from">
        <Row title="Device" detail="Every device of your CrashPlan account can be restored here.">
          <select className="select" aria-label="Device" value={source} disabled={!!attempt} onChange={(e) => setSource(e.target.value)}>
            {!source && <option value="">Choose a device</option>}
            {sources.map((d) => (
              <option key={String(d.id)} value={String(d.id)}>
                {deviceLabel(d)}
              </option>
            ))}
          </select>
        </Row>
        <Row title="Backup destination">
          {destinations.length === 1 ? (
            <span className="row-value">{destinations[0]!.name}</span>
          ) : (
            <select className="select" aria-label="Backup destination" value={destination} disabled={!!attempt} onChange={(e) => setDestination(e.target.value)}>
              {!destination && <option value="">Choose a destination</option>}
              {destinations.map((d) => (
                <option key={String(d.id)} value={String(d.id)}>
                  {d.name || 'Destination'}
                </option>
              ))}
            </select>
          )}
        </Row>
        {sets && sets.length > 1 && (
          <Row title="Backup set">
            <select className="select" aria-label="Backup set" value={set} disabled={!!attempt} onChange={(e) => setSet(e.target.value)}>
              {sets.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name || 'Backup set'}
                </option>
              ))}
            </select>
          </Row>
        )}
        <Row title="Show files as of">
          <Segmented
            label="Show files as of"
            value={when}
            disabled={!!attempt}
            onChange={setWhen}
            options={[
              { value: 'latest', label: 'Latest backup' },
              { value: 'date', label: 'Date and time' },
            ]}
          />
          {when === 'date' && (
            <input className="input" type="datetime-local" aria-label="Date and time" value={at} disabled={!!attempt} onChange={(e) => setAt(e.target.value)} />
          )}
        </Row>
        <Row title="Include deleted files" detail="Also show files that were deleted from the device.">
          <Switch label="Include deleted files" checked={includeDeleted} disabled={!!attempt} onChange={setIncludeDeleted} />
        </Row>
      </Section>

      {query && (
        <Section
          title="Files"
          description="Open folders to find what you need, then tick the files and folders to restore."
          actions={
            selected > 0 && (
              <button className="btn btn-primary btn-sm" onClick={() => restoreSection.current?.scrollIntoView({ behavior: 'smooth' })}>
                Restore {plural(selected, 'item')}…
              </button>
            )
          }
        >
          <div className="row" style={{ flexWrap: 'wrap' }}>
            <nav className="breadcrumbs" aria-label="Folder">
              <button aria-current={trail.length === 0 && !searchPattern ? 'location' : undefined} onClick={() => goTo(0)}>
                <HardDrive size={14} style={{ verticalAlign: '-2px', marginRight: 4 }} />
                {devices.data?.find((d) => String(d.id) === source)?.name || 'Device'}
              </button>
              {trail.map((part, index) => (
                <span key={part.fileId} style={{ display: 'inline-flex', alignItems: 'center' }}>
                  <ChevronRight size={13} />
                  <button aria-current={index === trail.length - 1 && !searchPattern ? 'location' : undefined} onClick={() => goTo(index + 1)}>
                    {part.name}
                  </button>
                </span>
              ))}
              {searchPattern && (
                <span style={{ display: 'inline-flex', alignItems: 'center' }}>
                  <ChevronRight size={13} />
                  <button aria-current="location">Search: {searchPattern}</button>
                </span>
              )}
            </nav>
            <form
              noValidate
              style={{ marginLeft: 'auto', display: 'flex', gap: 6, alignItems: 'center' }}
              onSubmit={(e) => {
                e.preventDefault()
                setSearchPattern(pattern.trim())
                setCursor(undefined)
                setCursors([])
              }}
            >
              <Search size={14} className="row-chevron" />
              <input
                className="input"
                style={{ width: 200, minHeight: 28 }}
                placeholder="Search, e.g. *.pdf"
                aria-label="Search the backup"
                value={pattern}
                maxLength={4096}
                onChange={(e) => {
                  setPattern(e.target.value)
                  if (!e.target.value) setSearchPattern('')
                }}
              />
            </form>
          </div>
          <div className="file-row head" aria-hidden="true">
            <span />
            <span />
            <span>Name</span>
            <span style={{ textAlign: 'right' }}>Size</span>
            <span>Modified</span>
            <span />
          </div>
          {page.isFetching && !page.data ? (
            <Loading label="Loading files…" />
          ) : page.error ? (
            <div className="row-block">
              <ErrorNotice error={page.error} />
            </div>
          ) : items.length === 0 ? (
            <Empty icon={<Folder size={28} />} title={searchPattern ? 'No matches' : 'This folder is empty'}>
              {searchPattern ? 'Try another name or a wildcard such as *.jpg.' : undefined}
            </Empty>
          ) : (
            <div className="list-scroll">
              {items.map((item) => {
                const inside = insideSelected(item.path, selection)
                const checked = !!selection[item.fileId] || (inside && !excluded[item.fileId])
                return (
                  <div key={item.fileId}>
                    <div className="file-row">
                      <input
                        type="checkbox"
                        aria-label={'Select ' + item.path}
                        checked={checked}
                        disabled={!!attempt || (inside && !selection[item.fileId])}
                        onChange={() => toggle(item)}
                      />
                      {item.isFolder ? <Folder size={17} className="folder-icon" /> : <File size={17} className="file-icon" />}
                      <button
                        className={'name truncate ' + (item.isFolder ? 'folder' : '')}
                        title={item.path}
                        onClick={() => (item.isFolder ? open(item) : setDetails(item))}
                      >
                        {searchPattern ? item.path : baseName(item.path)}
                        {excluded[item.fileId] && <Badge>Left out</Badge>}
                        {selection[item.fileId] && selection[item.fileId]!.timestamp !== query.at && (
                          <Badge tone="blue">Version {dateTime(selection[item.fileId]!.timestamp)}</Badge>
                        )}
                      </button>
                      <span className="size">{item.isFolder ? '' : bytes(item.size)}</span>
                      <span className="date">{dateTime(item.modified)}</span>
                      <button
                        className="icon-btn"
                        aria-label={'Details for ' + item.path}
                        aria-expanded={details?.fileId === item.fileId}
                        onClick={() => setDetails(details?.fileId === item.fileId ? undefined : item)}
                      >
                        <Info size={15} />
                      </button>
                    </div>
                    {details?.fileId === item.fileId && (
                      <ItemDetails
                        item={item}
                        query={query}
                        selection={selection[item.fileId]}
                        canExclude={inside && !selection[item.fileId]}
                        disabled={!!attempt}
                        onClose={() => setDetails(undefined)}
                        onVersion={(timestamp, isFolder) =>
                          setSelection((old) => ({
                            ...old,
                            [item.fileId]: { path: item.path, backupSetId: query.backupSetId, timestamp, isFolder },
                          }))
                        }
                        onExclude={() => {
                          setExcluded((old) => ({ ...old, [item.fileId]: archiveSelection(item, query.backupSetId, query.at) }))
                          setDetails(undefined)
                        }}
                      />
                    )}
                  </div>
                )
              })}
            </div>
          )}
          {(page.data?.incomplete || cursors.length > 0 || page.data?.nextCursor) && (
            <div className="row">
              <span className="row-main row-detail">
                {page.data?.incomplete ? 'Showing the first matches only. Narrow the search to see more.' : ''}
              </span>
              <div className="button-row">
                <button
                  className="btn btn-sm"
                  disabled={cursors.length === 0 || page.isFetching}
                  onClick={() => {
                    setCursor(cursors.at(-1) || undefined)
                    setCursors((old) => old.slice(0, -1))
                  }}
                >
                  <ChevronLeft size={14} /> Previous
                </button>
                <button
                  className="btn btn-sm"
                  disabled={!page.data?.nextCursor || page.isFetching}
                  onClick={() => {
                    setCursors((old) => [...old, cursor ?? ''])
                    setCursor(page.data!.nextCursor)
                  }}
                >
                  Next <ChevronRight size={14} />
                </button>
              </div>
            </div>
          )}
          {Object.keys(excluded).length > 0 && (
            <div className="row-block">
              <span className="row-detail">Left out of the restore:</span>
              {Object.entries(excluded).map(([id, item]) => (
                <div className="button-row" key={id}>
                  <span className="truncate small">{item.path}</span>
                  <button
                    className="btn btn-plain btn-sm"
                    disabled={!!attempt}
                    onClick={() =>
                      setExcluded((old) => {
                        const next = { ...old }
                        delete next[id]
                        return next
                      })
                    }
                  >
                    Include again
                  </button>
                </div>
              ))}
            </div>
          )}
        </Section>
      )}

      {query && (selected > 0 || attempt) && (
        <div ref={restoreSection}>
          <Section title="Restore to">
            <form noValidate onSubmit={submit}>
              <Row title="Location">
                <Segmented
                  label="Location"
                  disabled={!!attempt}
                  value={restoreMode}
                  onChange={setRestoreMode}
                  options={[
                    { value: 'folder', label: 'Restore folder' },
                    { value: 'original', label: 'Original location' },
                  ]}
                />
              </Row>
              {restoreMode === 'original' ? (
                <div className="row-block">
                  <Notice>
                    Files go back to where they were. Each original folder must still exist, be writable and lie inside a
                    restore folder mounted into the container.
                  </Notice>
                </div>
              ) : (
                <>
                  <Row title="Restore folder" detail="A writable folder mounted into the container.">
                    {writableRoots.length === 1 ? (
                      <span className="row-value mono">{writableRoots[0]!.path}</span>
                    ) : (
                      <select className="select" aria-label="Restore folder" value={rootId} disabled={!!attempt} onChange={(e) => setRootId(e.target.value)}>
                        <option value="">Choose a folder</option>
                        {roots.data?.map((root) => (
                          <option key={String(root.id)} value={String(root.id)} disabled={!root.writable}>
                            {root.path}
                            {root.writable ? '' : ' (not writable)'}
                          </option>
                        ))}
                      </select>
                    )}
                  </Row>
                  <Row title="Subfolder" detail="Optional. Must already exist inside the restore folder.">
                    <input className="input" aria-label="Subfolder" value={relative} placeholder="e.g. 2026-11-14" disabled={!!attempt} onChange={(e) => setRelative(e.target.value)} />
                  </Row>
                </>
              )}
              <Row title="If a file already exists">
                <select className="select" aria-label="If a file already exists" value={existing} disabled={!!attempt} onChange={(e) => setExisting(e.target.value)}>
                  {existingOptions.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              </Row>
              <Row title="File permissions">
                <select
                  className="select"
                  aria-label="File permissions"
                  value={permissions}
                  disabled={!!attempt}
                  onChange={(e) => setPermissions(e.target.value as 'current' | 'original')}
                >
                  <option value="current">Owned by the container user</option>
                  <option value="original">Keep the original owner and permissions</option>
                </select>
              </Row>
              <div className="row">
                <div className="row-main">
                  {roots.data?.length === 0 && restoreMode === 'folder' && (
                    <Notice tone="warning">No restore folder is mounted. Add a writable volume at /restore to the container.</Notice>
                  )}
                  <ErrorNotice error={start.error} />
                  {attempt && (
                    <span className="row-detail">
                      Request <code>{attempt.requestId}</code>
                    </span>
                  )}
                  {attempt && !start.isPending && start.data?.state === 'accepted' && (
                    <Notice tone="success">CrashPlan started the restore. Follow it under Restores below.</Notice>
                  )}
                  {attempt && !start.isPending && start.data?.state !== 'accepted' && !start.error && (
                    <Notice tone="warning">CrashPlan did not confirm the restore. Check Restores below before trying again.</Notice>
                  )}
                </div>
                <div className="button-row">
                  {attempt && !start.isPending && (
                    <button
                      type="button"
                      className="btn"
                      onClick={() => {
                        if (!confirm('Start a new restore selection? The previous request stays listed under Restores.')) return
                        setAttempt(undefined)
                        start.reset()
                        setSelection({})
                        setExcluded({})
                      }}
                    >
                      Prepare another restore
                    </button>
                  )}
                  <button
                    className="btn btn-primary"
                    disabled={!!attempt || (restoreMode === 'folder' && rootId === '')}
                  >
                    <Download size={15} />
                    {start.isPending ? 'Starting…' : `Restore ${plural(selected, 'item')}`}
                  </button>
                </div>
              </div>
            </form>
          </Section>
        </div>
      )}

      <Section title="Restores">
        {helperProblem && (
          <div className="row-block">
            <Notice tone="warning">
              The restore helper is not ready{helper.data?.reason ? `: ${helper.data.reason}` : '.'}
            </Notice>
          </div>
        )}
        <ErrorNotice error={jobs.error ?? control.error} />
        {jobs.isPending ? (
          <Loading />
        ) : openJobs.length + pastJobs.length === 0 ? (
          <div className="row">
            <span className="row-main row-detail">No restores yet.</span>
          </div>
        ) : (
          [...openJobs, ...pastJobs].map((job) => {
            const total = BigInt(job.numBytes || '0')
            const done = BigInt(job.bytesRestored || '0')
            const value = total > 0n ? Number((done * 1000n) / total) / 10 : undefined
            const running = !job.completed && !job.canceled
            return (
              <div className="row-block" key={job.id}>
                <div className="button-row" style={{ justifyContent: 'space-between' }}>
                  <span className="truncate mono" title={job.restorePath}>
                    {job.restorePath}
                  </span>
                  <Badge tone={job.completed ? 'green' : job.canceled ? undefined : 'blue'}>
                    {job.completed ? 'Completed' : job.canceled ? 'Stopped' : 'Restoring'}
                  </Badge>
                </div>
                {running && <Progress value={value} label="Restore progress" />}
                <div className="progress-line">
                  <span>
                    {count(job.filesRestored)} of {count(job.numFiles)} files · {bytes(job.bytesRestored)} of {bytes(job.numBytes)}
                  </span>
                  <span className="button-row">
                    {running && (
                      <>
                        <button className="btn btn-sm" disabled={control.isPending} onClick={() => control.mutate({ id: job.id, action: 'stop' })}>
                          Stop
                        </button>
                        <button
                          className="btn btn-sm"
                          disabled={control.isPending || !job.admissible}
                          onClick={() => control.mutate({ id: job.id, action: 'start' })}
                        >
                          Resume
                        </button>
                      </>
                    )}
                    <button
                      className="btn btn-sm btn-destructive"
                      disabled={control.isPending}
                      onClick={() => {
                        if (confirm('Remove this restore from the list? Files already restored stay where they are.'))
                          control.mutate({ id: job.id, action: 'remove' })
                      }}
                    >
                      Remove
                    </button>
                  </span>
                </div>
                {!job.admissible && running && (
                  <span className="row-detail">This restore targets a folder outside the mounted restore folders and cannot run.</span>
                )}
              </div>
            )
          })
        )}
      </Section>

      {(operations.data?.length ?? 0) > 0 && (
        <details className="group disclosure">
          <summary>
            <ChevronRight size={14} />
            Submitted restores ({operations.data!.length})
          </summary>
          <ErrorNotice error={operations.error ?? archiveOperations.error} />
          {operations.data!.map((op) => (
            <Row
              key={op.requestId}
              title={<span className="mono truncate">{op.restorePath}</span>}
              detail={`${dateTime(op.created)} · ${
                op.state === 'accepted' ? 'Accepted by CrashPlan' : op.state === 'rejected' ? 'Rejected' : 'Outcome unknown'
              }`}
            >
              {op.state !== 'indeterminate' &&
                !operations.data!.some((parent) => parent.children?.includes(op.requestId)) && (
                  <button
                    className="btn btn-sm"
                    disabled={
                      archiveOperations.isPending ||
                      !jobs.data ||
                      !!jobs.data.find((job) => job.id === op.restoreId && !job.completed && !job.canceled)
                    }
                    onClick={() => archiveOperations.mutate([op.requestId, ...(op.children ?? [])])}
                  >
                    Hide
                  </button>
                )}
            </Row>
          ))}
        </details>
      )}
      {devices.data && sources.length === 0 && (
        <Notice tone="warning">CrashPlan reports no device with a backup.</Notice>
      )}
      {base === undefined && when === 'date' && badDate && (
        <Notice>Choose a date and time to see the files as they were then.</Notice>
      )}
    </Page>
  )
}
