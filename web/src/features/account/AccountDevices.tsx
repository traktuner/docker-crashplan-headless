import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronRight, Cloud, HardDrive, Laptop, Monitor } from 'lucide-react'
import { api, APIError } from '../../shared/api'
import type {
  DeviceOperationRequest,
  LocalArchivePolicy,
} from '../../shared/api'
import {
  Field,
  Loading,
  Notice,
  Section,
  Segmented,
  Spinner,
} from '../../shared/components'
import { newRequestId } from '../../shared/requestId'
import { ReplacementWorkflow } from './ReplacementWorkflow'
import './account.css'

type PolicyDevice = LocalArchivePolicy['devices'][number]
type Kind = 'self' | 'own' | 'local' | 'destination'
const kinds: Record<Kind, { label: string; icon: typeof Monitor; tone: string }> = {
  self: { label: 'This device', icon: Monitor, tone: '' },
  own: { label: 'Other device in your account', icon: Laptop, tone: 'gray' },
  local: {
    label: 'Backup destination on this device',
    icon: HardDrive,
    tone: 'orange',
  },
  destination: { label: 'Backup destination', icon: Cloud, tone: 'green' },
}

function DeviceRow({
  device,
  kind,
  extra,
  editable,
  maintain,
  removable,
  blocked,
  submit,
}: {
  device: PolicyDevice
  kind: Kind
  extra?: { status?: string; id?: boolean }
  editable: boolean
  maintain: boolean
  removable: boolean
  blocked: boolean
  submit: (request: DeviceOperationRequest) => void
}) {
  const [mode, setMode] = useState<'view' | 'rename' | 'maintain' | 'remove'>(
    'view',
  )
  const [draft, setDraft] = useState(device.name)
  const [understood, setUnderstood] = useState(false)
  const { label, icon: Icon, tone } = kinds[kind]
  const changed = !!draft.trim() && draft !== device.name
  const offline = device.connected !== true
  const close = () => {
    setMode('view')
    setUnderstood(false)
  }
  const detail = (
    <>
      {label}
      {extra?.status && ' · ' + extra.status}
      {extra?.id && (
        <>
          {' · '}
          <span className="account-id">ID {device.id}</span>
        </>
      )}
    </>
  )
  if (mode === 'rename')
    return (
      <div className="row-block">
        <form
          noValidate
          className="account-inline"
          aria-label={'Rename ' + (device.name || device.id)}
          onSubmit={(e) => {
            e.preventDefault()
            if (!changed || blocked) return
            submit({
              requestId: newRequestId(),
              action: 'rename',
              deviceId: device.id,
              name: draft,
              confirm: 'RENAME ' + device.id,
            })
            close()
          }}
        >
          <input
            className="input"
            aria-label="Device name"
            value={draft}
            disabled={blocked}
            maxLength={30}
            autoFocus
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                setDraft(device.name)
                close()
              }
            }}
          />
          <button
            className="btn btn-primary btn-sm"
            disabled={blocked || !changed}
          >
            Save
          </button>
          <button
            type="button"
            className="btn btn-sm"
            onClick={() => {
              setDraft(device.name)
              close()
            }}
          >
            Cancel
          </button>
        </form>
        <div className="row-detail">
          {draft.trim()
            ? detail
            : 'Enter a name. Up to 30 characters.'}
        </div>
      </div>
    )
  return (
    <>
      <div className="row">
        <span className={'row-icon ' + tone}>
          <Icon size={16} aria-hidden="true" />
        </span>
        <div className="row-main">
          <div className="row-title" title={'Device ID ' + device.id}>
            {device.name || 'Unnamed device'}
          </div>
          <div className="row-detail">{detail}</div>
          {maintain && offline && (
            <div className="row-detail">
              Connect this destination before requesting maintenance.
            </div>
          )}
        </div>
        {mode === 'view' && (editable || maintain || removable) && (
          <div className="row-control">
            {editable && (
              <button
                className="btn btn-plain btn-sm"
                disabled={blocked}
                onClick={() => {
                  setDraft(device.name)
                  setMode('rename')
                }}
              >
                Rename
              </button>
            )}
            {maintain && (
              <button
                className="btn btn-sm"
                disabled={blocked || offline}
                onClick={() => setMode('maintain')}
              >
                Run archive maintenance…
              </button>
            )}
            {removable && (
              <button
                className="btn btn-sm"
                disabled={blocked}
                onClick={() => setMode('remove')}
              >
                Remove…
              </button>
            )}
          </div>
        )}
      </div>
      {mode === 'maintain' && (
        <div className="row-block">
          <Notice>
            Archive maintenance checks the backup archive at{' '}
            <strong>{device.name || 'this destination'}</strong> for damage,
            repairs what it can and frees space that your version and deleted
            file settings no longer keep. CrashPlan runs it in the background;
            on a large archive it can take hours.
          </Notice>
          <div className="button-row">
            <button
              className="btn btn-primary btn-sm"
              disabled={blocked || offline}
              onClick={() => {
                submit({
                  requestId: newRequestId(),
                  action: 'maintenance',
                  deviceId: device.id,
                  confirm: 'MAINTENANCE ' + device.id,
                })
                close()
              }}
            >
              Run maintenance
            </button>
            <button className="btn btn-sm" onClick={close}>
              Cancel
            </button>
          </div>
        </div>
      )}
      {mode === 'remove' && (
        <div className="row-block">
          <Notice tone="warning">
            Removing this destination can immediately delete its backup
            archive. To stop using it for one backup set only, detach it in
            Backup sets instead.
          </Notice>
          <label className="check">
            <input
              type="checkbox"
              checked={understood}
              disabled={blocked}
              onChange={(e) => setUnderstood(e.target.checked)}
            />
            I understand that removing this destination can immediately delete
            its backup archive.
          </label>
          <div className="button-row">
            <button
              className="btn btn-destructive btn-sm"
              disabled={blocked || !understood}
              onClick={() => {
                submit({
                  requestId: newRequestId(),
                  action: 'remove',
                  deviceId: device.id,
                  confirm: 'REMOVE ' + device.id,
                })
                close()
              }}
            >
              Remove destination
            </button>
            <button className="btn btn-sm" onClick={close}>
              Cancel
            </button>
          </div>
        </div>
      )}
    </>
  )
}

/** Names of this device, the account's other devices and used destinations. */
export function DeviceNames() {
  const client = useQueryClient()
  const policy = useQuery({
    queryKey: ['local-archive-policy'],
    queryFn: () => api.localArchivePolicy(),
  })
  const sets = useQuery({
    queryKey: ['set-details'],
    queryFn: () => api.backupSetDetails(),
  })
  const catalog = useQuery({
    queryKey: ['devices'],
    queryFn: () => api.devices(),
  })
  const [attempt, setAttempt] = useState<DeviceOperationRequest>()
  const operation = useMutation({
    mutationFn: (request: DeviceOperationRequest) => api.changeDevice(request),
    onSettled: () => {
      void client.invalidateQueries(
        { queryKey: ['devices'] },
        { cancelRefetch: false },
      )
      void client.invalidateQueries(
        { queryKey: ['local-archive-policy'] },
        { cancelRefetch: false },
      )
    },
  })
  const error = policy.error ?? sets.error ?? operation.error
  const used = (id: string) =>
    !!sets.data?.some((s) => s.destinations.some((d) => d.id === id))
  const visible =
    policy.data?.devices.filter(
      (d) => !d.deleted && (d.own || d.self || used(d.id)),
    ) ?? []
  const kindOf = (d: PolicyDevice): Kind =>
    d.self
      ? 'self'
      : d.parentId === policy.data?.selfId
        ? 'local'
        : d.own
          ? 'own'
          : 'destination'
  const key = (name: string) => name.trim().toLowerCase()
  const counts = new Map<string, number>()
  for (const d of visible) counts.set(key(d.name), (counts.get(key(d.name)) ?? 0) + 1)
  // Same-named entries get what tells them apart: whether a destination is
  // in use, and the device ID as a last resort.
  function extra(d: PolicyDevice, kind: Kind) {
    if ((counts.get(key(d.name)) ?? 0) < 2) return undefined
    let status: string | undefined
    if (kind === 'local' || kind === 'destination') {
      const known = catalog.data?.find((c) => String(c.id) === String(d.id))
      status =
        known && !known.target && !known.availableTarget
          ? 'Not in use'
          : used(d.id)
            ? 'Used by a backup set'
            : 'Not used by a backup set'
    }
    return { status, id: true }
  }
  return (
    <Section
      title="Devices and destination names"
      description="This device and the other devices of the same CrashPlan account, for example a Mac, appear here together with the destinations your backup sets use."
    >
      {error && (
        <div className="row-block">
          <Notice tone="error">
            {error instanceof APIError
              ? error.message
              : 'Device controls are unavailable.'}
          </Notice>
        </div>
      )}
      {policy.isPending ? (
        <Loading label="Reading devices…" />
      ) : (
        policy.data &&
        visible.length === 0 && (
          <div className="row">
            <div className="row-main row-detail">No devices reported.</div>
          </div>
        )
      )}
      {visible.map((device) => {
        const kind = kindOf(device)
        return (
          <DeviceRow
            key={device.id}
            device={device}
            kind={kind}
            extra={extra(device, kind)}
            editable={device.own || device.self}
            maintain={used(device.id)}
            removable={kind === 'local'}
            blocked={!!attempt || operation.isPending}
            submit={(request) => {
              if (attempt) return
              setAttempt(request)
              operation.mutate(request)
            }}
          />
        )
      })}
      {attempt && (
        <div className="row-block" aria-live="polite">
          {operation.isPending ? (
            <div className="account-waiting">
              <Spinner /> Sending the change to CrashPlan…
            </div>
          ) : operation.data ? (
            <Notice tone="success">
              CrashPlan reported: {operation.data.state}.
            </Notice>
          ) : null}
          <div className="row-detail">
            Request <span className="account-id">{attempt.requestId}</span>.
            Its saved outcome prevents automatic repetition.
          </div>
          {!operation.isPending && (
            <div className="button-row">
              <button
                className="btn btn-sm"
                onClick={() => {
                  setAttempt(undefined)
                  operation.reset()
                }}
              >
                Prepare another change
              </button>
            </div>
          )}
        </div>
      )}
    </Section>
  )
}

export function AccountDevices() {
  const client = useQueryClient()
  const pending = useQuery({
    queryKey: ['migration'],
    queryFn: () => api.migration(),
  })
  const [selected, setSelected] = useState('')
  const [confirmId, setConfirmId] = useState('')
  const [showCandidates, setShowCandidates] = useState(false)
  const [mode, setMode] = useState<'inherit' | 'replace'>('inherit')
  const candidates = useQuery({
    queryKey: ['migration-candidates'],
    queryFn: () => api.migrationCandidates(),
    enabled: showCandidates && mode === 'inherit',
  })
  const devices = useQuery({
    queryKey: ['devices'],
    queryFn: () => api.devices(),
    enabled: showCandidates && mode === 'replace',
  })
  const select = useMutation({
    mutationFn: () => api.selectMigration(selected),
    onSuccess: () => client.invalidateQueries(),
  })
  const inherit = useMutation({
    mutationFn: () =>
      mode === 'replace'
        ? api.replaceDevice(selected)
        : api.inheritDevice(selected),
    onSettled: () => client.invalidateQueries(),
  })
  const error =
    pending.error ??
    select.error ??
    candidates.error ??
    devices.error ??
    inherit.error
  const selectionRequired = pending.data?.status === 'SELECTION_REQUIRED'
  const choices = selectionRequired
    ? pending.data?.devices
    : mode === 'inherit'
      ? candidates.data?.devices
      : devices.data
          ?.filter((d) => d.source && !d.self)
          .map((d) => ({ computerGuid: d.id, name: d.name, hostname: '' }))
  const loadingChoices =
    showCandidates &&
    !choices &&
    (mode === 'inherit' ? candidates.isPending : devices.isPending)
  function reset() {
    setSelected('')
    setConfirmId('')
    inherit.reset()
    select.reset()
  }
  const busy = select.isPending || inherit.isPending
  const mismatch = !!selected && selected !== confirmId

  const body = (
    <>
      {error && (
        <div className="row-block">
          <Notice tone="error">
            {error instanceof APIError
              ? error.message
              : 'Device setup is unavailable.'}
          </Notice>
        </div>
      )}
      {!selectionRequired && (
        <div className="row-block">
          <p className="account-lead">
            Keeping the existing configuration in <code>/config</code> keeps
            this device's identity, so nothing needs to be done here. Take over
            another device only when you move its backup to this installation.
          </p>
          <Segmented
            label="Migration method"
            value={mode}
            options={[
              { value: 'inherit', label: 'Inherit an authorized device' },
              { value: 'replace', label: 'Replace a device' },
            ]}
            onChange={(value) => {
              setMode(value)
              reset()
              setShowCandidates(false)
            }}
          />
          {!showCandidates && (
            <div className="button-row">
              <button
                className="btn btn-sm"
                onClick={() => {
                  reset()
                  setShowCandidates(true)
                }}
              >
                Show eligible devices
              </button>
            </div>
          )}
          {loadingChoices && <Loading label="Reading devices…" />}
        </div>
      )}
      {choices && (
        <div className="row-block">
          <form
            className="account-form"
            noValidate
            onSubmit={(e) => {
              e.preventDefault()
              if (busy || mismatch) return
              if (selectionRequired) select.mutate()
              else if (selected) inherit.mutate()
            }}
          >
            {!selectionRequired && choices.length === 0 ? (
              <p className="account-lead">No eligible devices found.</p>
            ) : (
              <Field label="Device">
                <select
                  className="select"
                  value={selected}
                  onChange={(e) => {
                    setSelected(e.target.value)
                    setConfirmId('')
                  }}
                >
                  <option value="">
                    {selectionRequired
                      ? 'Register a new device'
                      : 'Choose a device'}
                  </option>
                  {choices.map((d) => (
                    <option value={d.computerGuid} key={d.computerGuid}>
                      {d.name}
                      {d.hostname ? ' · ' + d.hostname : ''}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            {selected && (
              <>
                <Notice tone="warning">
                  This changes the backup identity of this device and can sign
                  out the previous installation. To confirm, type the device ID{' '}
                  <code>{selected}</code>.
                </Notice>
                <Field label="Device ID">
                  <input
                    className="input"
                    value={confirmId}
                    autoComplete="off"
                    spellCheck={false}
                    onChange={(e) => setConfirmId(e.target.value)}
                  />
                </Field>
              </>
            )}
            {(selectionRequired || choices.length > 0) && (
              <div className="button-row">
                <button
                  className="btn btn-primary"
                  disabled={
                    busy || mismatch || (!selectionRequired && !selected)
                  }
                >
                  {busy && <Spinner size={14} />}
                  {selectionRequired && !selected
                    ? 'Register new device'
                    : mode === 'replace' && !selectionRequired
                      ? 'Replace selected device'
                      : 'Inherit selected device'}
                </button>
              </div>
            )}
          </form>
        </div>
      )}
      {select.data?.complete && (
        <div className="row-block">
          <Notice tone="success">Device setup completed.</Notice>
        </div>
      )}
      {inherit.data?.accepted && (
        <div className="row-block">
          <Notice tone="success">
            CrashPlan accepted the migration request. The engine may reconnect
            or ask you to sign in while it applies the change.
          </Notice>
        </div>
      )}
    </>
  )

  return (
    <>
      <ReplacementWorkflow />
      {selectionRequired ? (
        <Section
          title="Choose how to set up this device"
          description="CrashPlan needs a choice to finish signing in: register this as a new device, or take over one of the devices offered."
        >
          {body}
        </Section>
      ) : (
        <Section title="Advanced">
          <details className="disclosure">
            <summary>
              <ChevronRight size={14} aria-hidden="true" />
              Move a backup from another device to this one
            </summary>
            {body}
          </details>
        </Section>
      )}
    </>
  )
}
