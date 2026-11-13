import { newRequestId } from '../../shared/requestId'
import { useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import {
  ArrowLeft,
  ChevronRight,
  Download,
  File,
  Folder,
  RefreshCw,
} from 'lucide-react'
import { api, APIError, identifier, archiveSelection, archiveSearchQuery } from '../../shared/api'
import type {
  ArchiveItem,
  ArchiveQuery,
  RestoreRequest,
  RestoreSelection,
} from '../../shared/api'

function errorMessage(error: unknown) {
  return error instanceof APIError
    ? error.message
    : 'The operation failed. Check the service logs.'
}
function Notice({ error }: { error: unknown }) {
  return error ? (
    <div className="notice error" role="alert">
      {errorMessage(error)}
    </div>
  ) : null
}
function date(value: string) {
  const d = new Date(Number(value))
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString()
}

export function Restore() {
  const client = useQueryClient()
  const [source, setSource] = useState('')
  const [destination, setDestination] = useState('')
  const [set, setSet] = useState('')
  const [includeDeleted, setIncludeDeleted] = useState(false)
  const [at, setAt] = useState('')
  const [pattern, setPattern] = useState('')
  const [fullPath, setFullPath] = useState(false)
  const [searchPattern, setSearchPattern] = useState('')
  const [query, setQuery] = useState<ArchiveQuery>()
  const [trail, setTrail] = useState<{ name: string; fileId: string }[]>([
    { name: 'Archive', fileId: '' },
  ])
  const [cursors, setCursors] = useState<string[]>([])
  const [selection, setSelection] = useState<Record<string, RestoreSelection>>(
    {},
  )
  const [excluded, setExcluded] = useState<Record<string, RestoreSelection>>({})
  const [rootId, setRootId] = useState('')
  const [restoreMode, setRestoreMode] = useState<'folder' | 'original'>(
    'folder',
  )
  const [relative, setRelative] = useState('')
  const [existing, setExisting] = useState('RENAME_ORIGINAL')
  const [permissions, setPermissions] = useState<'current' | 'original'>(
    'current',
  )
  const [localError, setLocalError] = useState<unknown>()
  const [versionItem, setVersionItem] = useState<ArchiveItem>()
  const [attempt, setAttempt] = useState<RestoreRequest>()
  const devices = useQuery({
    queryKey: ['devices'],
    queryFn: () => api.devices(),
  })
  const sets = useQuery({
    queryKey: ['archive-sets', source, destination],
    queryFn: () => api.archiveSets(source, destination),
    enabled: !!source && !!destination,
  })
  const roots = useQuery({
    queryKey: ['restore-roots'],
    queryFn: () => api.restoreRoots(),
  })
  const jobs = useQuery({
    queryKey: ['restore-jobs'],
    queryFn: () => api.restoreJobs(),
    refetchInterval: 15_000,
  })
  const operations = useQuery({
    queryKey: ['restore-operations'],
    queryFn: () => api.restoreOperations(),
    refetchInterval: 15_000,
  })
  const archiveOperations = useMutation({
    mutationFn: (ids: string[]) => api.archiveRestoreOperations(ids),
    onSettled: () =>
      client.invalidateQueries({ queryKey: ['restore-operations'] }),
  })
  const helper = useQuery({
    queryKey: ['restore-helper'],
    queryFn: () => api.restoreHelper(),
    refetchInterval: 15_000,
  })
  const contents = useMutation({
    mutationFn: (item: ArchiveItem) =>
      api.archiveContents({
        ...query!,
        fileId: item.fileId,
        cursor: undefined,
      }),
  })
  const browse = useQuery({
    queryKey: ['archive', query],
    queryFn: () => api.browseArchive(query!),
    enabled: !!query && !searchPattern,
    staleTime: 60_000,
  })
  const search = useMutation({
    mutationFn: (
      request: ArchiveQuery & { pattern: string; fullPath: boolean },
    ) =>
      api.searchArchive(
        archiveSearchQuery(
          request,
          devices.data?.find((device) => device.id === request.targetId)
            ?.archiveType,
        ),
      ),
  })
  const resultPage = searchPattern ? search.data : browse.data
  const fetching = searchPattern ? search.isPending : browse.isFetching
  const versions = useMutation({
    mutationFn: (item: ArchiveItem) =>
      api.archiveVersions({
        ...query!,
        fileId: item.fileId,
        cursor: undefined,
      }),
  })
  const start = useMutation({
    mutationFn: (request: RestoreRequest) =>
      restoreMode === 'original'
        ? api.startOriginalRestore(request)
        : api.startRestore(request),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['restore-operations'] })
      void client.invalidateQueries({ queryKey: ['restore-jobs'] })
    },
  })
  const control = useMutation({
    mutationFn: ({
      id,
      action,
    }: {
      id: string
      action: 'start' | 'stop' | 'remove'
    }) => api.controlRestore(id, action),
    onSettled: () => client.invalidateQueries({ queryKey: ['restore-jobs'] }),
  })
  const scroll = useRef<HTMLDivElement>(null)
  const items = resultPage?.items ?? []
  const virtual = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroll.current,
    estimateSize: () => 56,
    overscan: 6,
  })

  function openArchive(e: FormEvent) {
    e.preventDefault()
    try {
      const cutoff = at ? new Date(at).getTime() : 0
      if (!Number.isSafeInteger(cutoff) || cutoff < 0)
        throw new APIError('INVALID_REQUEST', 'Choose a valid archive date.')
      setQuery({
        sourceId: identifier(source),
        targetId: identifier(destination),
        backupSetId: identifier(set),
        fileId: '',
        at: String(cutoff),
        includeDeleted,
        limit: 200,
      })
      setSelection({})
      setExcluded({})
      setSearchPattern('')
      setTrail([{ name: 'Archive', fileId: '' }])
      setCursors([])
      setVersionItem(undefined)
      setLocalError(undefined)
    } catch (error) {
      setLocalError(error)
    }
  }
  function navigate(index: number, item?: ArchiveItem) {
    if (!query) return
    setSearchPattern('')
    const next = item
      ? [...trail, { name: item.path, fileId: item.fileId }]
      : trail.slice(0, index + 1)
    setTrail(next)
    setQuery({ ...query, fileId: next.at(-1)!.fileId, cursor: undefined })
    setCursors([])
    setVersionItem(undefined)
    scroll.current?.scrollTo({ top: 0 })
  }
  function select(item: ArchiveItem) {
    const next = { ...selection }
    if (next[item.fileId]) delete next[item.fileId]
    else
      next[item.fileId] = archiveSelection(item, query!.backupSetId, query!.at)
    setSelection(next)
    setExcluded((old) =>
      Object.fromEntries(
        Object.entries(old).filter(
          ([id, entry]) =>
            !next[id] &&
            Object.values(next).some(
              (parent) =>
                parent.isFolder &&
                entry.path.startsWith(parent.path.replace(/\/$/, '') + '/'),
            ),
        ),
      ),
    )
  }
  function submit(e: FormEvent) {
    e.preventDefault()
    if (
      !query ||
      attempt ||
      Object.keys(selection).length === 0 ||
      (restoreMode === 'folder' && rootId === '')
    )
      return
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
  return (
    <>
      <header className="page-title">
        <h1>Restore</h1>
        <p className="muted">
          Browse archive versions and restore into a configured writable
          directory.
        </p>
      </header>
      <ol className="workflow-steps" aria-label="Restore progress">
        {['Choose archive', 'Select files', 'Restore'].map((label, index) => {
          const current = !query
            ? 0
            : Object.keys(selection).length === 0
              ? 1
              : 2
          return (
            <li
              key={label}
              className={
                index < current ? 'done' : index === current ? 'current' : ''
              }
              aria-current={index === current ? 'step' : undefined}
            >
              <span>{index + 1}</span>
              <strong>{label}</strong>
            </li>
          )
        })}
      </ol>
      <Notice
        error={
          localError ??
          devices.error ??
          sets.error ??
          roots.error ??
          jobs.error ??
          operations.error ??
          archiveOperations.error ??
          helper.error ??
          (searchPattern ? search.error : browse.error) ??
          start.error ??
          control.error
        }
      />
      <section className="card">
        <h2>Choose your archive</h2>
        <p className="muted">
          Choose the source device, archive destination and backup set. Archived
          devices remain available for restore.
        </p>
        <form onSubmit={openArchive}>
          <div className="restore-fields">
            <label>
              Source device
              <select
                value={source}
                onChange={(e) => {
                  setSource(e.target.value)
                  setSet('')
                }}
                required
              >
                <option value="">Choose a source</option>
                {devices.data
                  ?.filter((d) => d.source || d.self)
                  .map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.name || d.id}
                      {d.deleted ? ' (archived device)' : ''}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              Archive destination
              <select
                value={destination}
                onChange={(e) => {
                  setDestination(e.target.value)
                  setSet('')
                }}
                required
              >
                <option value="">Choose a destination</option>
                {devices.data
                  ?.filter((d) => d.target || d.availableTarget)
                  .map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.name || d.id}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              Backup set
              <select
                value={set}
                onChange={(e) => setSet(e.target.value)}
                required
              >
                <option value="">Choose a set</option>
                {sets.data?.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name || s.id}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <label>
            Archive as of (local time; blank means latest)
            <input
              type="datetime-local"
              value={at}
              onChange={(e) => setAt(e.target.value)}
            />
          </label>
          <label className="checkbox">
            <input
              type="checkbox"
              checked={includeDeleted}
              onChange={(e) => setIncludeDeleted(e.target.checked)}
            />{' '}
            Include deleted files
          </label>
          <button className="secondary" disabled={fetching}>
            Browse archive <ChevronRight size={16} />
          </button>
        </form>
      </section>
      {query && (
        <section className="card">
          <p className="muted">
            Browsing device {query.sourceId} · destination {query.targetId} ·
            set {query.backupSetId}
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              const term = pattern.trim()
              setSearchPattern(term)
              const next = { ...query, cursor: undefined }
              setQuery(next)
              setCursors([])
              if (term) search.mutate({ ...next, pattern: term, fullPath })
            }}
          >
            {devices.data?.find((device) => device.id === query.targetId)?.archiveType === 'ARCHIVE_V3' && (
              <p className="muted">Search covers the selected archive, including all folders.</p>
            )}
            <label>
              Search archive (wildcards * and ?)
              <input
                value={pattern}
                maxLength={4096}
                onChange={(e) => setPattern(e.target.value)}
              />
            </label>
            <label className="checkbox">
              <input
                type="checkbox"
                checked={fullPath}
                onChange={(e) => setFullPath(e.target.checked)}
              />{' '}
              Match full path
            </label>
            <button className="secondary" disabled={fetching}>
              Search
            </button>
            {searchPattern && (
              <button
                className="secondary"
                type="button"
                onClick={() => {
                  setSearchPattern('')
                  setQuery({ ...query, cursor: undefined })
                  setCursors([])
                }}
              >
                Back to folders
              </button>
            )}
          </form>
          <div className="restore-breadcrumbs" aria-label="Archive location">
            {trail.map((part, index) => (
              <button
                className="secondary"
                key={part.fileId}
                onClick={() => navigate(index)}
                disabled={fetching}
              >
                {part.name}
                <ChevronRight size={14} />
              </button>
            ))}
          </div>
          {fetching ? (
            <p role="status">Loading archive…</p>
          ) : items.length === 0 ? (
            <p className="muted">No archived items in this folder.</p>
          ) : (
            <div
              ref={scroll}
              className="archive-list"
              aria-label="Archived files"
            >
              <div
                style={{ height: virtual.getTotalSize(), position: 'relative' }}
              >
                {virtual.getVirtualItems().map((row) => {
                  const item = items[row.index]!
                  return (
                    <div
                      className="archive-row"
                      key={item.fileId}
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
                        type="checkbox"
                        aria-label={'Select ' + item.path}
                        checked={!!selection[item.fileId]}
                        disabled={!!attempt}
                        onChange={() => select(item)}
                      />
                      {item.isFolder ? (
                        <Folder size={19} />
                      ) : (
                        <File size={19} />
                      )}
                      <button
                        className="archive-name"
                        title={item.path}
                        disabled={!item.isFolder}
                        onClick={() => navigate(0, item)}
                      >
                        {item.path}
                      </button>
                      <span className="muted">{item.size} B</span>
                      <button
                        className="secondary"
                        disabled={
                          !!attempt ||
                          !Object.values(selection).some(
                            (parent) =>
                              parent.isFolder &&
                              item.path.startsWith(
                                parent.path.replace(/\/$/, '') + '/',
                              ),
                          )
                        }
                        onClick={() => {
                          setExcluded((old) => ({
                            ...old,
                            [item.fileId]: archiveSelection(
                              item,
                              query.backupSetId,
                              query.at,
                            ),
                          }))
                          setSelection((old) => {
                            const next = { ...old }
                            delete next[item.fileId]
                            return next
                          })
                        }}
                      >
                        Exclude
                      </button>
                      <button
                        className="secondary"
                        disabled={item.isFolder}
                        onClick={() => {
                          setVersionItem(item)
                          versions.mutate(item)
                        }}
                      >
                        Versions
                      </button>
                      <button
                        className="secondary"
                        disabled={contents.isPending}
                        onClick={() => contents.mutate(item)}
                      >
                        Size and counts
                      </button>
                    </div>
                  )
                })}
              </div>
            </div>
          )}
          {contents.error && (
            <div className="notice error" role="alert">
              Selection statistics are unavailable.
            </div>
          )}
          {contents.data && (
            <div className="notice" role="status">
              {contents.data.files} files, {contents.data.directories} folders,{' '}
              {contents.data.bytes} bytes.{' '}
              {!contents.data.verified && 'The native response does not identify the complete archive view. Counts are unverified. '}
              {contents.data.done
                ? 'Count complete.'
                : 'Count is incomplete; request again after the native calculation finishes.'}
            </div>
          )}
          {resultPage?.incomplete && (
            <div className="notice" role="status">
              Search reached the result limit. More matches may exist. Narrow
              the pattern.
            </div>
          )}
          <div className="restore-paging">
            <button
              className="secondary"
              disabled={cursors.length === 0 || fetching}
              onClick={() => {
                const previous = cursors.at(-1)
                setCursors((old) => old.slice(0, -1))
                const next = { ...query, cursor: previous || undefined }
                setQuery(next)
                if (searchPattern)
                  search.mutate({ ...next, pattern: searchPattern, fullPath })
                scroll.current?.scrollTo({ top: 0 })
              }}
            >
              <ArrowLeft size={16} /> Previous page
            </button>
            <button
              className="secondary"
              disabled={!resultPage?.nextCursor || fetching}
              onClick={() => {
                setCursors((old) => [...old, query.cursor ?? ''])
                const next = { ...query, cursor: resultPage!.nextCursor }
                setQuery(next)
                if (searchPattern)
                  search.mutate({ ...next, pattern: searchPattern, fullPath })
                scroll.current?.scrollTo({ top: 0 })
              }}
            >
              Next page <ChevronRight size={16} />
            </button>
            <span className="muted">
              {Object.keys(selection).length} selected
            </span>
          </div>
          {versionItem && (
            <div className="version-panel">
              <h2>Versions of {versionItem.path}</h2>
              <Notice error={versions.error} />
              {versions.isPending ? (
                <p role="status">Loading versions…</p>
              ) : (
                <label>
                  Restore version
                  <select
                    disabled={!!attempt}
                    value={selection[versionItem.fileId]?.timestamp ?? ''}
                    onChange={(e) => {
                      const selected = versions.data?.find(
                        (v) => v.timestamp === e.target.value,
                      )
                      if (selected)
                        setSelection((old) => ({
                          ...old,
                          [versionItem.fileId]: {
                            path: versionItem.path,
                            backupSetId: query.backupSetId,
                            timestamp: selected.timestamp,
                            isFolder: selected.isFolder,
                          },
                        }))
                    }}
                  >
                    <option value="">Choose a version</option>
                    {versions.data?.map((v) => (
                      <option key={v.timestamp} value={v.timestamp}>
                        {date(v.timestamp)} · {v.size} B
                      </option>
                    ))}
                  </select>
                </label>
              )}
              <button
                type="button"
                className="secondary"
                onClick={() => setVersionItem(undefined)}
              >
                Close versions
              </button>
            </div>
          )}
        </section>
      )}
      {query && (
        <section className="card form-card">
          <h2>Excluded archive paths</h2>
          {Object.entries(excluded).length === 0 ? (
            <p className="muted">
              No exclusions. Select a folder, then browse inside it to exclude
              specific paths.
            </p>
          ) : (
            Object.entries(excluded).map(([id, item]) => (
              <p key={id}>
                {item.path}{' '}
                <button
                  className="secondary"
                  disabled={!!attempt}
                  onClick={() =>
                    setExcluded((old) => {
                      const next = { ...old }
                      delete next[id]
                      return next
                    })
                  }
                >
                  Remove exclusion
                </button>
              </p>
            ))
          )}
        </section>
      )}
      {query && Object.keys(selection).length > 0 && (
        <section className="card form-card" id="restore-destination">
          <h2>Restore destination</h2>
          <form onSubmit={submit}>
            <label>
              Restore mode
              <select
                disabled={!!attempt}
                value={restoreMode}
                onChange={(e) =>
                  setRestoreMode(e.target.value as 'folder' | 'original')
                }
              >
                <option value="folder">Configured folder</option>
                <option value="original">Original location</option>
              </select>
            </label>
            {restoreMode === 'original' ? (
              <p className="notice">
                Each original parent directory must be writable and inside a
                configured restore root. Files are grouped by parent into
                separate jobs.
              </p>
            ) : (
              <>
                <label>
                  Writable root
                  <select
                    value={rootId}
                    disabled={!!attempt}
                    onChange={(e) => setRootId(e.target.value)}
                    required
                  >
                    <option value="">Choose a restore root</option>
                    {roots.data?.map((root) => (
                      <option
                        key={root.id}
                        value={root.id}
                        disabled={!root.writable}
                      >
                        {root.path}
                        {root.writable ? '' : ' (unavailable)'}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Existing subdirectory
                  <input
                    value={relative}
                    onChange={(e) => setRelative(e.target.value)}
                    placeholder="Leave empty to use the root"
                    disabled={!!attempt}
                  />
                </label>
              </>
            )}
            <label>
              If a file exists
              <select
                value={existing}
                disabled={!!attempt}
                onChange={(e) => setExisting(e.target.value)}
              >
                <option value="RENAME_ORIGINAL">
                  Rename the existing file
                </option>
                <option value="OVERWRITE_ORIGINAL">
                  Overwrite the existing file
                </option>
                <option value="SKIP_ALL">Skip existing files</option>
                <option value="SKIP_IF_NEWER">
                  Skip when the existing file is newer
                </option>
                <option value="SKIP_IF_OLDER">
                  Skip when the existing file is older
                </option>
              </select>
            </label>
            <label>
              Permissions
              <select
                value={permissions}
                disabled={!!attempt}
                onChange={(e) =>
                  setPermissions(e.target.value as 'current' | 'original')
                }
              >
                <option value="current">Current user</option>
                <option value="original">Original permissions</option>
              </select>
            </label>
            <button
              disabled={
                !!attempt ||
                (restoreMode === 'folder' && rootId === '') ||
                Object.keys(selection).length === 0
              }
            >
              <Download size={16} /> Restore selected items
            </button>
          </form>
          {roots.data?.length === 0 && (
            <p className="notice">
              No restore roots are configured. Configure a writable restore
              mount before submitting a job.
            </p>
          )}
        </section>
      )}
      {attempt && (
        <section className="card form-card">
          <h2>Restore submission</h2>
          <div className="restore-attempt">
            <p>
              Request <code>{attempt.requestId}</code>
            </p>
            {start.isPending ? (
              <p role="status">Submitting restore…</p>
            ) : (
              <>
                <p className="muted">
                  {start.data?.state === 'accepted'
                    ? 'CrashPlan accepted this request. Follow the job below; acceptance is not completion.'
                    : 'Check the retained operations and native jobs before creating another request.'}
                </p>
                <button
                  className="secondary"
                  onClick={() => {
                    if (
                      confirm(
                        'Start a different restore selection? The previous request remains recorded.',
                      )
                    ) {
                      setAttempt(undefined)
                      start.reset()
                      setSelection({})
                    }
                  }}
                >
                  Prepare another restore
                </button>
              </>
            )}
          </div>
        </section>
      )}
      <section className="card">
        <div className="set-heading">
          <h2>Restore jobs</h2>
          <button
            className="secondary"
            onClick={() => {
              void jobs.refetch()
              void operations.refetch()
              void helper.refetch()
            }}
          >
            <RefreshCw size={16} /> Refresh
          </button>
        </div>
        <p className="muted">
          Helper: {helper.data?.state ?? 'Unavailable'}
          {helper.data?.reason ? ' · ' + helper.data.reason : ''}
        </p>
        {jobs.data?.length === 0 && (
          <p className="muted">No native restore jobs.</p>
        )}
        {jobs.data?.map((job) => (
          <div className="restore-job" key={job.id}>
            <h3>
              Restore {job.id}{' '}
              <span className="badge">
                {job.completed
                  ? 'Native completed'
                  : job.canceled
                    ? 'Canceled'
                    : 'Open'}
              </span>
            </h3>
            <p>{job.restorePath}</p>
            <p className="muted">
              {job.filesRestored} / {job.numFiles} files · {job.bytesRestored} /{' '}
              {job.numBytes} B{job.imported ? ' · Imported job' : ''}
            </p>
            {!job.admissible && !job.completed && !job.canceled && (
              <p className="notice">
                The helper cannot run this job under the configured restore
                roots.
              </p>
            )}
            <div className="restore-paging">
              {!job.completed && !job.canceled && (
                <>
                  <button
                    className="secondary"
                    disabled={control.isPending}
                    onClick={() =>
                      control.mutate({ id: job.id, action: 'stop' })
                    }
                  >
                    Stop
                  </button>
                  <button
                    className="secondary"
                    disabled={control.isPending || !job.admissible}
                    onClick={() =>
                      control.mutate({ id: job.id, action: 'start' })
                    }
                  >
                    Resume
                  </button>
                </>
              )}
              <button
                className="secondary"
                disabled={control.isPending}
                onClick={() => {
                  if (
                    confirm(
                      'Remove this job from the native restore queue? Already restored files remain.',
                    )
                  )
                    control.mutate({ id: job.id, action: 'remove' })
                }}
              >
                Remove job
              </button>
            </div>
          </div>
        ))}
      </section>
      <section className="card">
        <h2>Retained submissions</h2>
        <p className="muted">
          Unknown outcomes remain recorded. This list does not automatically
          submit another restore.
        </p>
        {operations.data?.map((op) => (
          <div className="restore-job" key={op.requestId}>
            <code>{op.requestId}</code>
            <p>
              <span className="badge">{op.state}</span>{' '}
              {op.restoreId
                ? 'Native job ' + op.restoreId
                : 'No confirmed native job ID'}
            </p>
            <p className="muted">
              {op.restorePath} · {new Date(op.created).toLocaleString()}
            </p>
            {op.state !== 'indeterminate' &&
              !operations.data?.some((parent) =>
                parent.children?.includes(op.requestId),
              ) && (
                <button
                  className="secondary"
                  disabled={
                    archiveOperations.isPending ||
                    !jobs.data ||
                    !!jobs.data.find(
                      (job) =>
                        job.id === op.restoreId &&
                        !job.completed &&
                        !job.canceled,
                    )
                  }
                  onClick={() => {
                    if (
                      confirm(
                        'Archive this completed or removed submission? Its permanent replay barrier is retained.',
                      )
                    )
                      archiveOperations.mutate([
                        op.requestId,
                        ...(op.children ?? []),
                      ])
                  }}
                >
                  Archive history entry
                </button>
              )}
          </div>
        ))}
      </section>
    </>
  )
}
