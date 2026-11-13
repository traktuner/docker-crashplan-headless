import { allowedArchiveSources } from './policy'
import { newRequestId } from '../../shared/requestId'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, APIError } from '../../shared/api'
import type { LocalArchiveRequest } from '../../shared/api'
export function LocalArchives() {
  const client = useQueryClient()
  const policy = useQuery({
    queryKey: ['local-archive-policy'],
    queryFn: () => api.localArchivePolicy(),
  })
  const roots = useQuery({
    queryKey: ['restore-roots'],
    queryFn: () => api.restoreRoots(),
  })
  const [chosenAction, setAction] =
    useState<LocalArchiveRequest['action']>('create')
  const action =
    !policy.data?.allowed && chosenAction === 'create' ? 'attach' : chosenAction
  const [root, setRoot] = useState(''),
    [relative, setRelative] = useState(''),
    [source, setSource] = useState(''),
    [destination, setDestination] = useState(''),
    [confirm, setConfirm] = useState('')
  const [attempt, setAttempt] = useState<LocalArchiveRequest>()
  const operation = useMutation({
    mutationFn: (request: LocalArchiveRequest) => api.localArchive(request),
    onSuccess: () => {
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
  const sources = allowedArchiveSources(policy.data, action)
  const sourceId = sources.some((d) => d.id === source)
    ? source
    : (sources.find((d) => d.self)?.id ?? sources[0]?.id ?? '')
  const destinations =
    policy.data?.devices.filter((d) =>
      sourceId === policy.data?.selfId ? d.parentId === sourceId : d.self,
    ) ?? []
  const error = policy.error ?? roots.error ?? operation.error
  return (
    <section className="card">
      <h2>Local backup archives</h2>
      <p className="muted">
        Create a destination, reconnect an existing archive, or move an archive.
        A missing archive can be relocated by changing configuration without
        copying files. Both locations must be inside configured writable roots.
        Cloud destinations do not use this form.
      </p>
      {error && (
        <div className="notice error" role="alert">
          {error instanceof APIError
            ? error.message
            : 'Local archive controls are unavailable.'}
        </div>
      )}
      {policy.data && !policy.data.allowed && (
        <div className="notice">
          Local folder backup is disabled for this device. Owned incoming
          archives follow their separate account policy.
        </div>
      )}
      {sources.length > 0 && (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            const request = attempt ?? {
              requestId: newRequestId(),
              action,
              target: { rootId: root, relative },
              sourceId:
                action === 'create' ? (policy.data?.selfId ?? '') : sourceId,
              destinationId: destination,
              confirm,
            }
            setAttempt(request)
            operation.mutate(request)
          }}
        >
          <fieldset disabled={!!attempt || operation.isPending}>
            <div className="columns">
              <label>
                Operation
                <select
                  value={action}
                  onChange={(e) => {
                    setAction(e.target.value as LocalArchiveRequest['action'])
                    setConfirm('')
                    setDestination('')
                  }}
                >
                  {policy.data?.allowed && (
                    <option value="create">Create destination</option>
                  )}
                  <option value="attach">Reconnect existing archive</option>
                  <option value="move">Move archive</option>
                  <option value="relocate">Relocate a missing archive</option>
                </select>
              </label>
              {action !== 'create' && (
                <label>
                  Source computer
                  <select
                    value={sourceId}
                    onChange={(e) => {
                      setSource(e.target.value)
                      setDestination('')
                    }}
                  >
                    {sources.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.name || d.id}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              {action !== 'create' && (
                <label>
                  {action === 'move' || action === 'relocate'
                    ? 'Archive destination'
                    : 'Existing destination (optional)'}
                  <select
                    value={destination}
                    required={action === 'move' || action === 'relocate'}
                    onChange={(e) => setDestination(e.target.value)}
                  >
                    <option value="">
                      {action === 'move' || action === 'relocate'
                        ? 'Choose a local destination'
                        : 'Create or discover destination'}
                    </option>
                    {destinations.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.name || d.id}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              <label>
                Writable root
                <select
                  value={root}
                  required
                  onChange={(e) => setRoot(e.target.value)}
                >
                  <option value="">Choose a root</option>
                  {roots.data?.map((r) => (
                    <option key={r.id} value={r.id} disabled={!r.writable}>
                      {r.path}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Existing relative folder
                <input
                  value={relative}
                  onChange={(e) => setRelative(e.target.value)}
                  placeholder="Root directory when empty"
                />
              </label>
              <label>
                Type {action.toUpperCase()} to confirm
                <input
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  autoComplete="off"
                />
              </label>
            </div>
          </fieldset>
          <button
            disabled={
              operation.isPending ||
              confirm !== action.toUpperCase() ||
              (!!attempt && !operation.isError)
            }
          >
            Submit {action}
          </button>
        </form>
      )}
      {attempt && (
        <p className="muted">
          Submission: {attempt.requestId}. Repeating this submission retrieves
          its stored result without running it again.
        </p>
      )}
      {operation.data && (
        <div className="notice" role="status">
          Result: {operation.data.state}.
          {operation.data.existingArchive &&
            ' An archive already exists. Review it and submit a separate reconnect operation.'}
          {operation.data.sourceMissing &&
            ' The source was confirmed absent. Only the native archive location was changed; no files were copied.'}
          {operation.data.jobId &&
            ' Native move job: ' +
              operation.data.jobId +
              '. Accepted does not mean the copy has finished.'}
          {operation.data.destinationId &&
            ' Destination: ' + operation.data.destinationId + '.'}
        </div>
      )}
      {attempt && !operation.isPending && (
        <button
          className="secondary"
          onClick={() => {
            setAttempt(undefined)
            operation.reset()
            setConfirm('')
          }}
        >
          Prepare another operation
        </button>
      )}
    </section>
  )
}
