import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, APIError } from '../../shared/api'
import type {
  DeviceOperationRequest,
  LocalArchivePolicy,
} from '../../shared/api'
import { newRequestId } from '../../shared/requestId'

type Device = LocalArchivePolicy['devices'][number]
function DeviceRow({
  device,
  local,
  blocked,
  submit,
  maintain,
  editable,
}: {
  device: Device
  editable: boolean
  maintain: boolean
  local: boolean
  blocked: boolean
  submit: (in_: DeviceOperationRequest) => void
}) {
  const [draft, setDraft] = useState<string>(),
    [understood, setUnderstood] = useState(false)
  const name = draft ?? device.name
  return (
    <div className="restore-job">
      <div>
        <strong>{device.name || device.id}</strong>
        <span className="muted">
          {' '}
          {device.self
            ? 'This device'
            : local
              ? 'Local destination'
              : device.own
                ? 'Owned device'
                : 'Backup destination'}{' '}
          · {device.id}
        </span>
      </div>
      {editable && (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            submit({
              requestId: newRequestId(),
              action: 'rename',
              deviceId: device.id,
              name,
              confirm: 'RENAME ' + device.id,
            })
            setDraft(undefined)
          }}
        >
          <div className="columns">
            <label>
              Device name
              <input
                value={name}
                disabled={blocked}
                maxLength={30}
                onChange={(e) => setDraft(e.target.value)}
              />
            </label>
            <button disabled={blocked || !name.trim() || name === device.name}>
              Rename
            </button>
          </div>
        </form>
      )}
      {maintain && (
        <>
          <button
            className="secondary"
            disabled={blocked || device.connected !== true}
            onClick={() =>
              submit({
                requestId: newRequestId(),
                action: 'maintenance',
                deviceId: device.id,
                confirm: 'MAINTENANCE ' + device.id,
              })
            }
          >
            Run archive maintenance
          </button>
          {device.connected !== true && (
            <p className="muted">
              Connect this destination before requesting maintenance.
            </p>
          )}
        </>
      )}
      {local && !device.self && (
        <div>
          <label className="checkbox">
            <input
              type="checkbox"
              checked={understood}
              disabled={blocked}
              onChange={(e) => setUnderstood(e.target.checked)}
            />
            I understand that removing this destination can immediately delete
            its backup archive.
          </label>
          <button
            className="secondary"
            disabled={blocked || !understood}
            onClick={() =>
              submit({
                requestId: newRequestId(),
                action: 'remove',
                deviceId: device.id,
                confirm: 'REMOVE ' + device.id,
              })
            }
          >
            Remove destination
          </button>
        </div>
      )}
    </div>
  )
}
export function DeviceManagement() {
  const client = useQueryClient()
  const policy = useQuery({
    queryKey: ['local-archive-policy'],
    queryFn: () => api.localArchivePolicy(),
  })
  const sets = useQuery({
    queryKey: ['set-details'],
    queryFn: () => api.backupSetDetails(),
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
  return (
    <section className="card">
      <h2>Devices and destination names</h2>
      <p className="muted">
        Rename your devices and local destinations, or request archive
        maintenance. Removal is separate from detaching a destination from one
        backup set.
      </p>
      {error && (
        <div className="notice error" role="alert">
          {error instanceof APIError
            ? error.message
            : 'Device controls are unavailable.'}
        </div>
      )}
      {policy.isPending && <p role="status">Reading devices…</p>}
      {policy.data?.devices
        .filter(
          (d) =>
            !d.deleted &&
            (d.own ||
              d.self ||
              sets.data?.some((s) =>
                s.destinations.some((target) => target.id === d.id),
              )),
        )
        .map((device) => (
          <DeviceRow
            editable={device.own || device.self}
            key={device.id}
            device={device}
            maintain={
              !!sets.data?.some((s) =>
                s.destinations.some((d) => d.id === device.id),
              )
            }
            local={device.parentId === policy.data?.selfId}
            blocked={!!attempt || operation.isPending}
            submit={(request) => {
              if (attempt) return
              setAttempt(request)
              operation.mutate(request)
            }}
          />
        ))}
      {attempt && (
        <p>
          Submission: <code>{attempt.requestId}</code>. Its saved outcome
          prevents automatic repetition.
        </p>
      )}
      {operation.data && (
        <div className="notice" role="status">
          Native result: {operation.data.state}.
        </div>
      )}
      {attempt && !operation.isPending && (
        <button
          className="secondary"
          onClick={() => {
            setAttempt(undefined)
            operation.reset()
          }}
        >
          Prepare another operation
        </button>
      )}
    </section>
  )
}
