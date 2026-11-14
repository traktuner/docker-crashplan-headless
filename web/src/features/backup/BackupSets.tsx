import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ChevronRight, File, Folder, RefreshCw } from 'lucide-react'
import { api } from '../../shared/api'
import type { BackupSet, Device, ScanOperation, SelectionPath, Setting, SetResult } from '../../shared/api'
import {
  Badge,
  ErrorNotice,
  Loading,
  Notice,
  Page,
  Row,
  Section,
  Segmented,
  Switch,
  useConfirm,
} from '../../shared/components'
import { SaveBar } from '../../shared/controls'
import { baseName, bytes, count, dateTime, plural, relative, words } from '../../shared/format'
import { LocalArchives } from '../destinations/LocalArchives'
import { ByteInput, DurationInput, InlineError, PatternRows, WindowRows } from './controls'
import type { Pattern } from './controls'
import {
  compressionOptions,
  dedupOptions,
  epoch,
  fullScanSummary,
  optionLabel,
  pendingScanStates,
  retentionLabels,
  retentionValue,
  runWindow,
  scanStates,
  scheduleSummary,
  unresolvedScanStates,
  windowProblem,
} from './presentation'
import './backup.css'

const pageSize = 200

function fieldValue(set: BackupSet, name: string) {
  return set.fields?.find((f) => f.name === name)?.value
}
export function backupSetName(set: BackupSet) {
  const name = fieldValue(set, 'name')
  return typeof name === 'string' && name.trim() ? name : 'Backup set'
}
function selectionSummary(paths: SelectionPath[]) {
  const selected = paths.filter((p) => p.selected)
  const folders = selected.filter((p) => p.directory).length
  const files = selected.length - folders
  const parts = [
    folders ? plural(folders, 'folder') : '',
    files ? plural(files, 'file') : '',
  ].filter(Boolean)
  return parts.length ? parts.join(', ') : 'Nothing selected'
}
function useDeviceName() {
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => api.devices() })
  return {
    devices,
    /** undefined while the device list is still loading. */
    name: (id: string) =>
      devices.data
        ? devices.data.find((d) => String(d.id) === String(id))?.name || 'Unnamed destination'
        : undefined,
  }
}

function Result({ result }: { result: SetResult | undefined }) {
  if (!result) return null
  return result.complete ? (
    <Notice tone="success">Saved.</Notice>
  ) : (
    <Notice tone="warning">
      Some changes were not applied. The values shown are what CrashPlan reports now; nothing was
      retried automatically.
    </Notice>
  )
}

// ---------- Settings (schedule, full scan, data handling) ----------

type Draft = Record<string, unknown>

function FieldRow({
  field,
  value,
  disabled,
  problem,
  change,
}: {
  field: Setting
  value: unknown
  disabled: boolean
  problem?: string
  change: (value: unknown, problem?: string) => void
}) {
  const lockedDetail = field.locked ? 'Managed by your organization' : undefined
  const row = (title: string, control: ReactNode, detail?: string) => (
    <div className="row">
      <div className="row-main">
        <div className="row-title">{title}</div>
        {(detail || lockedDetail) && (
          <div className="row-detail">{[detail, lockedDetail].filter(Boolean).join(' · ')}</div>
        )}
        <InlineError>{problem}</InlineError>
      </div>
      <div className="row-control">{control}</div>
    </div>
  )
  const text = value === null || value === undefined ? '' : String(value)
  switch (field.name) {
    case 'name':
      return row(
        'Name',
        <input
          className="input"
          aria-label="Name"
          value={text}
          maxLength={256}
          disabled={disabled}
          onChange={(e) =>
            change(e.target.value, e.target.value.trim() ? undefined : 'Enter a name.')
          }
        />,
      )
    case 'scanInterval':
      return row(
        'Scan every',
        <DurationInput
          label="Scan every"
          value={value}
          disabled={disabled}
          change={(ms) =>
            change(ms ?? value, ms === undefined ? 'Enter a number greater than zero.' : undefined)
          }
        />,
        'A full scan finds changes that file watching missed.',
      )
    case 'scanTime':
      return row(
        'Start scan at',
        <input
          className="input"
          type="time"
          aria-label="Start scan at"
          value={text}
          disabled={disabled}
          onChange={(e) =>
            change(e.target.value, /^\d\d:\d\d$/.test(e.target.value) ? undefined : 'Enter a time.')
          }
        />,
      )
    case 'compression':
      return row(
        'Compression',
        <select
          className="select"
          aria-label="Compression"
          value={text}
          disabled={disabled}
          onChange={(e) => change(e.target.value)}
        >
          {!text && <option value="">Not set</option>}
          {compressionOptions.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>,
      )
    case 'dataDeDuplication':
      return row(
        'Deduplication',
        <select
          className="select"
          aria-label="Deduplication"
          value={text}
          disabled={disabled}
          onChange={(e) => change(e.target.value)}
        >
          {!text && <option value="">Not set</option>}
          {dedupOptions.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>,
        'Sends only the parts of a file that changed.',
      )
  }
  if (field.kind === 'boolean')
    return row(
      field.name === 'encryptionEnabled' ? 'Encryption' : field.label,
      <Switch
        label={field.label}
        checked={value === true}
        disabled={disabled}
        onChange={(checked) => change(checked)}
      />,
    )
  if (field.unit === 'bytes')
    return row(
      field.name === 'dataDeDupAutoMaxFileSize'
        ? 'Deduplicate files up to'
        : field.name === 'dataDeDupAutoMaxFileSizeForWan'
          ? 'Deduplicate files up to (internet)'
          : field.label,
      <ByteInput
        label={field.label}
        value={value}
        disabled={disabled}
        change={(b) => change(b ?? value, b === undefined ? 'Enter a size of 0 or more.' : undefined)}
      />,
      field.name.startsWith('dataDeDupAuto') ? 'Applies when deduplication is Automatic.' : undefined,
    )
  if (field.unit === 'ms')
    return row(
      field.label,
      <DurationInput
        label={field.label}
        value={value}
        disabled={disabled}
        change={(ms) =>
          change(ms ?? value, ms === undefined ? 'Enter a number greater than zero.' : undefined)
        }
      />,
    )
  const numeric = field.kind === 'integer' || field.kind === 'long'
  return row(
    field.label,
    <input
      className="input"
      aria-label={field.label}
      inputMode={numeric ? 'numeric' : undefined}
      value={text}
      disabled={disabled}
      onChange={(e) =>
        change(
          e.target.value,
          numeric && !/^\d+$/.test(e.target.value.trim()) ? 'Enter a whole number.' : undefined,
        )
      }
    />,
  )
}

const settingGroups: { title: string; description?: string; names: string[] }[] = [
  { title: 'General', names: ['name', 'priority'] },
  { title: 'Full scan', names: ['scanInterval', 'scanTime'] },
  {
    title: 'Data handling',
    names: [
      'encryptionEnabled',
      'backupOpenFiles',
      'compression',
      'dataDeDuplication',
      'dataDeDupAutoMaxFileSize',
      'dataDeDupAutoMaxFileSizeForWan',
    ],
  },
]

function SettingsEditor({
  set,
  changed,
  onDirty,
}: {
  set: BackupSet
  changed: (result: SetResult) => void
  onDirty: (dirty: boolean) => void
}) {
  const [draft, setDraft] = useState<Draft>({})
  const [problems, setProblems] = useState<Record<string, string>>({})
  const [generation, setGeneration] = useState(0)
  const [tried, setTried] = useState(false)
  const reset = () => {
    setDraft({})
    setProblems({})
    setTried(false)
    setGeneration((n) => n + 1)
  }
  const save = useMutation({
    mutationFn: () => api.saveBackupSet(set.id, draft),
    onSuccess: (result) => {
      changed(result)
      if (result.complete) reset()
    },
  })
  const fields = (set.fields ?? []).filter((f) => f.available)
  const busy = save.isPending
  const current = (field: Setting) =>
    Object.hasOwn(draft, field.name) ? draft[field.name] : field.value
  const update = (name: string, value: unknown, problem?: string) => {
    setDraft((old) => ({ ...old, [name]: value }))
    setProblems((old) => {
      const next = { ...old }
      if (problem) next[name] = problem
      else delete next[name]
      return next
    })
  }
  const windowField = fields.find((f) => f.name === 'backupRunWindow')
  const known = new Set(['backupRunWindow', ...settingGroups.flatMap((g) => g.names)])
  const groups = [
    ...settingGroups,
    { title: 'Other', names: fields.filter((f) => !known.has(f.name)).map((f) => f.name) },
  ]
  const hasProblems = Object.keys(problems).length > 0
  const dirty = Object.keys(draft).length > 0
  useEffect(() => onDirty(dirty), [dirty]) // eslint-disable-line react-hooks/exhaustive-deps
  const renderRow = (field: Setting) => (
    <FieldRow
      key={field.name + ':' + generation}
      field={field}
      value={current(field)}
      disabled={field.locked || busy}
      problem={problems[field.name]}
      change={(value, problem) => update(field.name, value, problem)}
    />
  )
  return (
    <form
      noValidate
      className="set-editor"
      onSubmit={(e) => {
        e.preventDefault()
        setTried(true)
        if (!hasProblems && dirty) save.mutate()
      }}
    >
      {windowField && (
        <Section
          title="Backup schedule"
          description="New and changed files are backed up while the schedule allows it."
        >
          <WindowRows
            value={runWindow(current(windowField))}
            locked={windowField.locked}
            disabled={windowField.locked || busy}
            change={(value) => update(windowField.name, value, windowProblem(value))}
          />
        </Section>
      )}
      {groups.map((group) => {
        const rows = group.names
          .map((name) => fields.find((f) => f.name === name))
          .filter((f): f is Setting => !!f)
        return rows.length ? (
          <Section key={group.title} title={group.title}>
            {rows.map(renderRow)}
          </Section>
        ) : null
      })}
      <ErrorNotice error={save.error} />
      {tried && hasProblems && <Notice tone="error">Correct the highlighted values first.</Notice>}
      <Result result={save.data} />
      <SaveBar
        dirty={dirty}
        busy={busy}
        blocked={hasProblems ? 'Correct the highlighted values first.' : undefined}
        saved={save.data?.complete}
        saveLabel="Save changes"
        onRevert={reset}
      />
    </form>
  )
}

// ---------- Files ----------

function crumbs(path: string) {
  const out = [{ label: 'Computer', path: '' }]
  if (!path) return out
  out.push({ label: '/', path: '/' })
  let current = ''
  for (const part of path.split('/').filter(Boolean)) {
    current += '/' + part
    out.push({ label: part, path: current })
  }
  return out
}

function SourceSelection({
  set,
  changed,
  onDirty,
}: {
  set: BackupSet
  changed: (result: SetResult) => void
  onDirty: (dirty: boolean) => void
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
  const total = query.data?.total ?? 0
  const virtual = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroll.current,
    // Taller rows on touch screens keep every row a comfortable target.
    estimateSize: () => (typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches ? 48 : 40),
    overscan: 8,
  })
  function navigate(path: string) {
    setParent(path)
    setOffset(0)
    scroll.current?.scrollTo({ top: 0 })
  }
  const changes = Object.keys(draft).length
  const explicit = set.paths ?? []
  useEffect(() => onDirty(changes > 0), [changes > 0]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className="set-editor">
      <Section
        title="Files to back up"
        description="Checking a folder includes everything inside it. Other configured paths stay as they are."
      >
        <div className="row">
          <nav className="breadcrumbs" aria-label="Folder">
            {crumbs(parent).map((crumb, index, all) => (
              <span key={crumb.path} className="crumb">
                {index > 0 && <ChevronRight size={12} />}
                <button
                  type="button"
                  aria-current={index === all.length - 1 ? 'location' : undefined}
                  disabled={query.isFetching}
                  onClick={() => index < all.length - 1 && navigate(crumb.path)}
                >
                  {crumb.label}
                </button>
              </span>
            ))}
          </nav>
        </div>
        <ErrorNotice error={query.error} />
        {query.isFetching ? (
          <div className="row">
            <Loading label="Reading folder…" />
          </div>
        ) : items.length === 0 ? (
          <div className="row">
            <span className="row-detail">This folder is empty.</span>
          </div>
        ) : (
          <div className="source-list" ref={scroll} aria-label="Backup file selection">
            <div style={{ height: virtual.getTotalSize(), position: 'relative' }}>
              {virtual.getVirtualItems().map((row) => {
                const item = items[row.index]!
                const excluded = item.selectionCode === 'EXCLUDED'
                const reason = item.excludedForSystem
                  ? 'Excluded by the system policy'
                  : item.excludedForHost
                    ? 'Excluded by the container policy'
                    : item.excludedForUser
                      ? 'Excluded by one of your exclusions'
                      : 'Excluded by policy or exclusion'
                const label = parent === '' ? item.path : baseName(item.path)
                return (
                  <div
                    className="source-row"
                    key={item.path}
                    style={{ height: row.size, transform: `translateY(${row.start}px)` }}
                  >
                    <label className="hit">
                    <input
                      aria-label={'Back up ' + item.path}
                      type="checkbox"
                      disabled={set.pathsLocked || excluded || save.isPending}
                      checked={draft[item.path]?.selected ?? item.selectionCode === 'SELECTED'}
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
                    </label>
                    {item.directory ? (
                      <Folder size={16} className="folder-icon" aria-hidden="true" />
                    ) : (
                      <File size={16} className="file-icon" aria-hidden="true" />
                    )}
                    {item.directory && item.exists ? (
                      <button
                        type="button"
                        className="source-name folder truncate"
                        title={item.path}
                        onClick={() => navigate(item.path)}
                      >
                        {label}
                      </button>
                    ) : (
                      <span className="source-name truncate" title={item.path}>
                        {label}
                      </span>
                    )}
                    <span className="source-badges">
                      {!item.exists && <Badge tone="orange">Missing</Badge>}
                      {excluded && (
                        <span title={reason}>
                          <Badge>Excluded</Badge>
                        </span>
                      )}
                    </span>
                    {item.directory && item.exists ? (
                      <ChevronRight size={14} className="row-chevron" aria-hidden="true" />
                    ) : (
                      <span />
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        )}
        {total > pageSize && (
          <div className="row">
            <div className="row-main">
              <span className="row-detail">
                {count(offset + 1)}–{count(offset + items.length)} of {count(total)}
              </span>
            </div>
            <div className="button-row">
              <button
                type="button"
                className="btn btn-sm"
                disabled={offset === 0 || query.isFetching}
                onClick={() => setOffset((n) => Math.max(0, n - pageSize))}
              >
                Previous
              </button>
              <button
                type="button"
                className="btn btn-sm"
                disabled={query.isFetching || offset + items.length >= total}
                onClick={() => setOffset((n) => n + pageSize)}
              >
                Next
              </button>
            </div>
          </div>
        )}
      </Section>
      <SaveBar
        dirty={changes > 0}
        busy={save.isPending}
        blocked={set.pathsLocked ? 'The file selection is managed by your organization.' : undefined}
        status={changes ? plural(changes, 'unsaved change') : undefined}
        saved={save.data?.complete}
        saveLabel="Save selection"
        revertLabel="Discard"
        onRevert={() => setDraft({})}
        onSave={() => save.mutate()}
      />
      <ErrorNotice error={save.error} />
      <Result result={save.data} />
      {explicit.length > 0 && (
        <Section title="Configured paths" description="The paths stored in this backup set.">
          {explicit.map((path) => (
            <Row
              key={path.path}
              title={<span className="mono">{path.path}</span>}
              value={path.selected ? <Badge tone="green">Included</Badge> : <Badge>Excluded</Badge>}
            />
          ))}
        </Section>
      )}
    </div>
  )
}

// ---------- Exclusions ----------

function Excludes({
  set,
  changed,
  onDirty,
}: {
  set: BackupSet
  changed: (result: SetResult) => void
  onDirty: (dirty: boolean) => void
}) {
  const [patterns, setPatterns] = useState<Pattern[]>(set.excludes ?? [])
  const dirty = JSON.stringify(patterns) !== JSON.stringify(set.excludes ?? [])
  useEffect(() => onDirty(dirty), [dirty]) // eslint-disable-line react-hooks/exhaustive-deps
  const [tried, setTried] = useState(false)
  const save = useMutation({
    mutationFn: () => api.setExcludes(set.id, patterns),
    onSuccess: changed,
  })
  const invalid = patterns.some((p) => !p.pattern.trim())
  const disabled = set.excludesLocked || save.isPending
  return (
    <form
      noValidate
      className="set-editor"
      onSubmit={(e) => {
        e.preventDefault()
        setTried(true)
        if (!invalid) save.mutate()
      }}
    >
      <Section
        title="Exclusions"
        description="Files matching these patterns are skipped, in addition to the system exclusions. Regular expressions use Java syntax."
      >
        <PatternRows patterns={patterns} disabled={disabled} showProblems={tried} change={setPatterns} />
      </Section>
      <ErrorNotice error={save.error} />
      <Result result={save.data} />
      <SaveBar
        dirty={dirty}
        busy={save.isPending}
        blocked={
          set.excludesLocked
            ? 'Exclusions are managed by your organization.'
            : tried && invalid
              ? 'Enter a pattern in every line or remove it.'
              : undefined
        }
        saved={save.data?.complete}
        saveLabel="Save exclusions"
        onRevert={() => {
          setPatterns(set.excludes ?? [])
          setTried(false)
        }}
      />
    </form>
  )
}

// ---------- Destinations ----------

function Destinations({ set, changed }: { set: BackupSet; changed: (result: SetResult) => void }) {
  const { devices } = useDeviceName()
  const [confirmDialog, ask] = useConfirm()
  const save = useMutation({
    mutationFn: ({ id, attached }: { id: string; attached: boolean }) =>
      api.setBackupDestination(set.id, id, attached),
    onSuccess: changed,
  })
  const attachedIds = (set.destinations ?? []).map((d) => String(d.id))
  const options: Device[] =
    devices.data?.filter(
      (d) => d.target || d.availableTarget || attachedIds.includes(String(d.id)),
    ) ?? []
  return (
    <div className="set-editor">
      {confirmDialog}
      <Section
        title="Destinations"
        description="Where this backup set is stored. Turning a destination off stops future backups to it."
      >
        {devices.isPending && (
          <div className="row">
            <Loading label="Reading destinations…" />
          </div>
        )}
        <ErrorNotice error={devices.error} />
        {devices.data && options.length === 0 && (
          <Row title="No destinations available" />
        )}
        {options.map((device) => {
          const current = set.destinations?.find((d) => String(d.id) === String(device.id))
          const locked = set.destinationsLocked || !!current?.locked
          const detail = [
            device.connected === true ? 'Connected' : device.connected === false ? 'Not connected' : undefined,
            device.usedBytes
              ? device.quotaBytes && device.quotaBytes !== '0'
                ? `${bytes(device.usedBytes)} of ${bytes(device.quotaBytes)} used`
                : `${bytes(device.usedBytes)} used`
              : undefined,
            device.blocked ? 'Blocked' : undefined,
            locked ? 'Managed by your organization' : undefined,
          ]
            .filter(Boolean)
            .join(' · ')
          return (
            <Row
              key={String(device.id)}
              title={device.name || 'Unnamed destination'}
              detail={detail || undefined}
            >
              <Switch
                label={'Back up to ' + (device.name || 'this destination')}
                checked={!!current}
                disabled={locked || save.isPending}
                onChange={async (attached) => {
                  if (
                    !attached &&
                    !(await ask({
                      title: `Stop backing up to ${device.name || 'this destination'}?`,
                      message: `“${backupSetName(set)}” is no longer sent there. Backups already stored stay until your retention settings remove them.`,
                      confirm: 'Stop backing up',
                      destructive: true,
                    }))
                  )
                    return
                  save.mutate({ id: String(device.id), attached })
                }}
              />
            </Row>
          )
        })}
      </Section>
      <ErrorNotice error={save.error} />
      <Result result={save.data} />
    </div>
  )
}

// ---------- Retention ----------

function Retention({ set }: { set: BackupSet }) {
  const fields = (set.retention ?? []).filter((f) => f.available)
  return (
    <div className="set-editor">
      <Section
        title="Versions and deleted files"
        footer="CrashPlan does not accept retention changes from this interface. Change them in the CrashPlan console."
      >
        {fields.length === 0 && <Row title="No retention policy reported" />}
        {fields.map((field) => (
          <Row
            key={field.name}
            title={retentionLabels[field.name] ?? field.label}
            value={retentionValue(field)}
          />
        ))}
      </Section>
    </div>
  )
}

// ---------- Delete ----------

function DeleteSet({ set, remove }: { set: BackupSet; remove: ReturnType<typeof useRemove> }) {
  const isDefault = String(set.id) === '1'
  const [confirmDialog, ask] = useConfirm()
  return (
    <Section title="Delete backup set">
      {confirmDialog}
      <Row
        title="Delete this backup set"
        detail={
          isDefault
            ? 'The default backup set cannot be deleted.'
            : set.locked
              ? 'Managed by your organization.'
              : 'Its file selection and settings are removed. Stored backup history can be affected.'
        }
      >
        <button
          type="button"
          className="btn btn-destructive"
          disabled={isDefault || set.locked || remove.isPending}
          onClick={async () => {
            if (
              await ask({
                title: `Delete “${backupSetName(set)}”?`,
                message: 'Its file selection and settings are removed. This can affect its stored backup history.',
                confirm: 'Delete',
                destructive: true,
              })
            )
              remove.mutate(set.id)
          }}
        >
          Delete…
        </button>
      </Row>
    </Section>
  )
}

// ---------- Page ----------

type Tab = 'settings' | 'files' | 'destinations' | 'exclusions' | 'retention'
const tabs: { value: Tab; label: string }[] = [
  { value: 'settings', label: 'Schedule & options' },
  { value: 'files', label: 'Files' },
  { value: 'destinations', label: 'Destinations' },
  { value: 'exclusions', label: 'Exclusions' },
  { value: 'retention', label: 'Retention' },
]

function useRemove(refresh: () => void, closed: () => void) {
  return useMutation({
    mutationFn: (id: string) => api.deleteBackupSet(id),
    onSuccess: () => {
      closed()
      refresh()
    },
  })
}

function ScanRow({
  scan,
  acknowledging,
  acknowledge,
}: {
  scan: ScanOperation
  acknowledging: boolean
  acknowledge: () => void
}) {
  const state = scanStates[scan.state] ?? { label: words(scan.state) }
  const at = epoch(scan.updated)
  const unresolved = unresolvedScanStates.includes(scan.state)
  const [confirmDialog, ask] = useConfirm()
  return (
    <>
    {confirmDialog}
    <Row
      title="Last full scan"
      detail={
        unresolved
          ? 'CrashPlan has not confirmed how this scan ended. Acknowledge it to allow a new scan.'
          : scan.reason
            ? words(scan.reason)
            : undefined
      }
      value={
        <span className="scan-value">
          <Badge tone={state.tone}>{state.label}</Badge>
          <span title={dateTime(at)}>{relative(at)}</span>
        </span>
      }
    >
      {unresolved && (
        <button
          type="button"
          className="btn btn-sm"
          disabled={acknowledging}
          onClick={async () => {
            if (
              await ask({
                title: 'Acknowledge this scan?',
                message: 'CrashPlan never confirmed how it ended. Acknowledging it allows a new full scan; no scan is sent now.',
                confirm: 'Acknowledge',
              })
            )
              acknowledge()
          }}
        >
          Acknowledge
        </button>
      )}
    </Row>
    </>
  )
}

export function BackupSets() {
  const client = useQueryClient()
  const [open, setOpen] = useState('')
  const [tab, setTab] = useState<Tab>('settings')
  // Editors stay mounted once opened, so switching tabs keeps unsaved edits.
  const [visited, setVisited] = useState<Tab[]>(['settings'])
  const [dirtyTabs, setDirtyTabs] = useState<Tab[]>([])
  const markDirty = (which: Tab) => (on: boolean) =>
    setDirtyTabs((old) => (on ? (old.includes(which) ? old : [...old, which]) : old.filter((t) => t !== which)))
  const openSet = (id: string) => {
    setOpen(id)
    setTab('settings')
    setVisited(['settings'])
    setDirtyTabs([])
  }
  const [name, setName] = useState('')
  const [nameTried, setNameTried] = useState(false)
  const sets = useQuery({ queryKey: ['set-details'], queryFn: () => api.backupSetDetails() })
  const scans = useQuery({
    queryKey: ['scans'],
    queryFn: () => api.scans(),
    refetchInterval: 10000,
  })
  const { name: destinationName } = useDeviceName()
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['set-details'] })
    void client.invalidateQueries({ queryKey: ['source-browser'] })
  }
  const create = useMutation({
    mutationFn: () => api.createBackupSet(name.trim()),
    onSuccess: (set) => {
      setName('')
      setNameTried(false)
      openSet(String(set.id))
      refresh()
    },
  })
  const remove = useRemove(refresh, () => setOpen(''))
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
  const nameProblem = nameTried && !name.trim() ? 'Enter a name for the backup set.' : undefined
  const [confirmDialog, ask] = useConfirm()
  return (
    <Page
      title="Backup sets"
      subtitle="Choose what to back up, where it goes and when it runs."
    >
      {confirmDialog}
      <ErrorNotice error={sets.error ?? scans.error} />
      {sets.isPending ? (
        <Loading label="Loading backup sets…" />
      ) : (
        sets.data?.map((set) => {
          const id = String(set.id)
          const current = scans.data?.filter((s) => String(s.backupSetId) === id).at(-1)
          const pending = !!current && pendingScanStates.includes(current.state)
          const isOpen = open === id
          const destinations = (set.destinations ?? []).map((d) => destinationName(String(d.id)))
          return (
            <div className="set-block" key={id}>
              <Section
                title={backupSetName(set)}
                actions={
                  <>
                    <button
                      type="button"
                      className="btn btn-sm"
                      aria-describedby={pending && !unresolvedScanStates.includes(current!.state) ? 'scan-pending-' + id : undefined}
                      disabled={scan.isPending || pending}
                      onClick={() => scan.mutate(id)}
                    >
                      <RefreshCw size={14} aria-hidden="true" /> Full scan
                    </button>
                    <button
                      type="button"
                      className={'btn btn-sm' + (isOpen ? ' btn-primary' : '')}
                      aria-expanded={isOpen}
                      onClick={async () => {
                        if (
                          isOpen &&
                          dirtyTabs.length > 0 &&
                          !(await ask({
                            title: 'Discard unsaved changes?',
                            message: `Changes in ${dirtyTabs.map((t) => tabs.find((x) => x.value === t)!.label).join(', ')} are not saved yet.`,
                            confirm: 'Discard',
                            destructive: true,
                          }))
                        )
                          return
                        if (isOpen) setOpen('')
                        else openSet(id)
                      }}
                    >
                      {isOpen ? 'Done' : 'Edit'}
                    </button>
                  </>
                }
              >
                <Row title="Files" value={selectionSummary(set.paths ?? [])} />
                <Row
                  title="Destinations"
                  value={
                    destinations.length === 0
                      ? 'None'
                      : destinations.every(Boolean)
                        ? destinations.join(', ')
                        : plural(destinations.length, 'destination')
                  }
                />
                <Row title="Back up" value={scheduleSummary(fieldValue(set, 'backupRunWindow'))} />
                <Row
                  title="Full scan"
                  value={fullScanSummary(fieldValue(set, 'scanInterval'), fieldValue(set, 'scanTime'))}
                />
                {fieldValue(set, 'compression') !== undefined && (
                  <Row
                    title="Compression"
                    value={optionLabel(compressionOptions, fieldValue(set, 'compression'))}
                  />
                )}
                {pending && !unresolvedScanStates.includes(current!.state) && (
                  <Row title="Full scan in progress" detail={<span id={'scan-pending-' + id}>A new scan can start when this one ends.</span>} />
                )}
                {current && (
                  <ScanRow
                    scan={current}
                    acknowledging={ack.isPending}
                    acknowledge={() => ack.mutate(current.id)}
                  />
                )}
              </Section>
              {(scan.error && scan.variables === id) || (ack.error && current && ack.variables === current.id) || (remove.error && remove.variables === id) ? (
                <ErrorNotice error={(scan.variables === id && scan.error) || (remove.variables === id && remove.error) || ack.error} />
              ) : null}
              {isOpen && (
                <div className="set-editor">
                  <Segmented
                    tabs
                    label="Backup set settings"
                    value={tab}
                    options={tabs}
                    marked={dirtyTabs}
                    onChange={(next) => {
                      setTab(next)
                      setVisited((old) => (old.includes(next) ? old : [...old, next]))
                    }}
                  />
                  <div hidden={tab !== 'settings'} role="tabpanel" aria-label="Schedule & options">
                    <div className="set-editor">
                      <SettingsEditor key={id} set={set} changed={changed} onDirty={markDirty('settings')} />
                      <DeleteSet set={set} remove={remove} />
                    </div>
                  </div>
                  {visited.includes('files') && (
                    <div hidden={tab !== 'files'} role="tabpanel" aria-label="Files">
                      <SourceSelection key={id} set={set} changed={changed} onDirty={markDirty('files')} />
                    </div>
                  )}
                  {visited.includes('destinations') && (
                    <div hidden={tab !== 'destinations'} role="tabpanel" aria-label="Destinations">
                      <Destinations set={set} changed={changed} />
                    </div>
                  )}
                  {visited.includes('exclusions') && (
                    <div hidden={tab !== 'exclusions'} role="tabpanel" aria-label="Exclusions">
                      <Excludes key={id} set={set} changed={changed} onDirty={markDirty('exclusions')} />
                    </div>
                  )}
                  {visited.includes('retention') && (
                    <div hidden={tab !== 'retention'} role="tabpanel" aria-label="Retention">
                      <Retention set={set} />
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })
      )}
      <Section title="New backup set" description="Use another set for files that need a different schedule or destination.">
        <form
          noValidate
          className="row"
          onSubmit={(e) => {
            e.preventDefault()
            setNameTried(true)
            if (name.trim()) create.mutate()
          }}
        >
          <div className="row-main">
            <input
              className="input"
              aria-label="New backup set name"
              placeholder="Name"
              autoComplete="off"
              value={name}
              maxLength={256}
              aria-invalid={nameProblem ? true : undefined}
              onChange={(e) => setName(e.target.value)}
            />
            <InlineError>{nameProblem}</InlineError>
          </div>
          <button type="submit" className="btn btn-primary" disabled={create.isPending}>
            {create.isPending ? 'Creating…' : 'Create'}
          </button>
        </form>
      </Section>
      <ErrorNotice error={create.error} />
      <LocalArchives />
    </Page>
  )
}
