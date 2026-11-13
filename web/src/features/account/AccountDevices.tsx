import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, APIError } from '../../shared/api'
import { ReplacementWorkflow } from './ReplacementWorkflow'

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
  function reset() {
    setSelected('')
    setConfirmId('')
    inherit.reset()
    select.reset()
  }
  return (
    <>
      <ReplacementWorkflow />
      <section className="card form-card">
        <h2>Device setup and inheritance</h2>
        {error && (
          <p className="notice error" role="alert">
            {error instanceof APIError
              ? error.message
              : 'Device setup is unavailable.'}
          </p>
        )}
        {selectionRequired ? (
          <p className="notice">
            Sign-in requires a choice: register this as a new device or inherit
            an offered device.
          </p>
        ) : (
          <p className="muted">
            Reusing the existing persistent configuration preserves its device
            identity. Choose inheritance only when moving another device's
            backup to this installation.
          </p>
        )}
        {!selectionRequired && (
          <label>
            Migration method
            <select
              value={mode}
              onChange={(e) => {
                setMode(e.target.value as 'inherit' | 'replace')
                reset()
                setShowCandidates(false)
              }}
            >
              <option value="inherit">Authorized device inheritance</option>
              <option value="replace">Replace an existing device</option>
            </select>
          </label>
        )}
        {!selectionRequired && !showCandidates && (
          <button
            className="secondary"
            onClick={() => {
              reset()
              setShowCandidates(true)
            }}
          >
            Show eligible devices
          </button>
        )}
        {choices && (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (selectionRequired) select.mutate()
              else inherit.mutate()
            }}
          >
            <label>
              Device
              <select
                value={selected}
                onChange={(e) => {
                  setSelected(e.target.value)
                  setConfirmId('')
                }}
                required={!selectionRequired}
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
            </label>
            {selected && (
              <>
                <p className="notice">
                  This changes the backup device identity and can deauthorize
                  the previous installation. Confirm device ID:{' '}
                  <code>{selected}</code>.
                </p>
                <label>
                  Confirm device ID
                  <input
                    value={confirmId}
                    autoComplete="off"
                    onChange={(e) => setConfirmId(e.target.value)}
                  />
                </label>
              </>
            )}
            <button
              disabled={
                select.isPending ||
                inherit.isPending ||
                (!!selected && selected !== confirmId)
              }
            >
              {selectionRequired && !selected
                ? 'Register new device'
                : mode === 'replace' && !selectionRequired
                  ? 'Replace selected device'
                  : 'Inherit selected device'}
            </button>
          </form>
        )}
        {select.data?.complete && (
          <p className="notice success">Device setup completed.</p>
        )}
        {inherit.data?.accepted && (
          <p className="notice success">
            CrashPlan accepted the migration request. The engine may reconnect
            or require sign-in while it applies the change.
          </p>
        )}
      </section>
    </>
  )
}
