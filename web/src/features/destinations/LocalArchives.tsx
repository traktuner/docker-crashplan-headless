import { useState } from 'react'
import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronRight } from 'lucide-react'
import { api, APIError } from '../../shared/api'
import type { LocalArchiveRequest } from '../../shared/api'
import { ErrorNotice, Notice, Section } from '../../shared/components'
import { newRequestId } from '../../shared/requestId'
import { InlineError } from '../backup/controls'
import { outcome } from './outcome'
import { allowedArchiveSources } from './policy'
import './destinations.css'

type Action = LocalArchiveRequest['action']

const actions: Record<Action, { label: string; button: string; detail: string }> = {
  create: {
    label: 'Create a new destination',
    button: 'Create destination',
    detail: 'Starts a new local archive in the chosen folder.',
  },
  attach: {
    label: 'Reconnect an existing archive',
    button: 'Reconnect archive',
    detail: 'Uses an archive folder that already exists, for example after recreating the container.',
  },
  move: {
    label: 'Move an archive',
    button: 'Move archive',
    detail: 'Copies the archive to the chosen folder and switches to it.',
  },
  relocate: {
    label: 'Relocate a missing archive',
    button: 'Relocate archive',
    detail: 'Points CrashPlan at an archive that was already moved. No files are copied.',
  },
}

function FormRow({
  title,
  detail,
  problem,
  children,
}: {
  title: string
  detail?: ReactNode
  problem?: string
  children: ReactNode
}) {
  return (
    <div className="row">
      <div className="row-main">
        <div className="row-title">{title}</div>
        {detail && <div className="row-detail">{detail}</div>}
        <InlineError>{problem}</InlineError>
      </div>
      <div className="row-control">{children}</div>
    </div>
  )
}

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
  const [chosenAction, setAction] = useState<Action>('create')
  const action: Action =
    !policy.data?.allowed && chosenAction === 'create' ? 'attach' : chosenAction
  const [root, setRoot] = useState(''),
    [relative, setRelative] = useState(''),
    [source, setSource] = useState(''),
    [destination, setDestination] = useState(''),
    [confirm, setConfirm] = useState(''),
    [tried, setTried] = useState(false),
    [expanded, setExpanded] = useState(false)
  const [attempt, setAttempt] = useState<LocalArchiveRequest>()
  const operation = useMutation({
    mutationFn: (request: LocalArchiveRequest) => api.localArchive(request),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['devices'] }, { cancelRefetch: false })
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
  const deviceName = (id: string) =>
    policy.data?.devices.find((d) => d.id === id)?.name || 'Unnamed destination'
  const needsDestination = action === 'move' || action === 'relocate'
  const word = action.toUpperCase()
  const problems = {
    root: root ? undefined : 'Choose where the archive is stored.',
    destination:
      needsDestination && !destination ? 'Choose the destination to change.' : undefined,
  }
  const valid = !problems.root && !problems.destination
  const locked = !!attempt || operation.isPending
  const error = policy.error ?? roots.error ?? operation.error
  const result = operation.data
  const resultText = result && outcome(result.state)
  return (
    <Section
      title="Local archives"
      description="Back up to a folder on this server, or reconnect, move or relocate an existing archive. Folders must be inside a writable storage root. Cloud destinations are not managed here."
    >
      {error && (
        <div className="row-block">
          {error instanceof APIError ? (
            <ErrorNotice error={error} />
          ) : (
            <Notice tone="error">Local archive controls are unavailable.</Notice>
          )}
        </div>
      )}
      {policy.data && !policy.data.allowed && (
        <div className="row-block">
          <Notice>
            Backing up to a local folder is turned off for this computer. Archives from your other
            computers follow their own account policy.
          </Notice>
        </div>
      )}
      {sources.length === 0 ? (
        policy.data && (
          <div className="row">
            <span className="row-detail">No local archive changes are allowed for this account.</span>
          </div>
        )
      ) : (
        <details
          className="disclosure"
          open={expanded}
          onToggle={(e) => setExpanded(e.currentTarget.open)}
        >
          <summary>
            <ChevronRight size={14} aria-hidden="true" />
            Create or change a local archive
          </summary>
          <form
            noValidate
            className="form-rows"
            onSubmit={(e) => {
              e.preventDefault()
              if (!attempt) {
                setTried(true)
                if (!valid) return
              }
              const request = attempt ?? {
                requestId: newRequestId(),
                action,
                target: { rootId: root, relative },
                sourceId: action === 'create' ? (policy.data?.selfId ?? '') : sourceId,
                destinationId: destination,
                confirm,
              }
              setAttempt(request)
              operation.mutate(request)
            }}
          >
            <fieldset className="bare" disabled={locked}>
              <FormRow title="Action" detail={actions[action].detail}>
                <select
                  className="select"
                  aria-label="Action"
                  value={action}
                  onChange={(e) => {
                    setAction(e.target.value as Action)
                    setConfirm('')
                    setDestination('')
                    setTried(false)
                  }}
                >
                  {(Object.keys(actions) as Action[])
                    .filter((a) => a !== 'create' || policy.data?.allowed)
                    .map((a) => (
                      <option key={a} value={a}>
                        {actions[a].label}
                      </option>
                    ))}
                </select>
              </FormRow>
              {action !== 'create' && (
                <FormRow title="Computer" detail="The computer whose backups are in the archive.">
                  <select
                    className="select"
                    aria-label="Computer"
                    value={sourceId}
                    onChange={(e) => {
                      setSource(e.target.value)
                      setDestination('')
                    }}
                  >
                    {sources.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.name || 'Unnamed computer'}
                        {d.self ? ' (this computer)' : ''}
                      </option>
                    ))}
                  </select>
                </FormRow>
              )}
              {action !== 'create' && (
                <FormRow
                  title="Destination"
                  detail={needsDestination ? undefined : 'Optional. Leave empty to find or create one.'}
                  problem={tried ? problems.destination : undefined}
                >
                  <select
                    className="select"
                    aria-label="Destination"
                    value={destination}
                    aria-invalid={tried && problems.destination ? true : undefined}
                    onChange={(e) => setDestination(e.target.value)}
                  >
                    <option value="">
                      {needsDestination ? 'Choose…' : 'Find automatically'}
                    </option>
                    {destinations.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.name || 'Unnamed destination'}
                      </option>
                    ))}
                  </select>
                </FormRow>
              )}
              <FormRow
                title={action === 'move' ? 'Move to' : 'Storage root'}
                problem={tried ? problems.root : undefined}
              >
                <select
                  className="select"
                  aria-label="Storage root"
                  value={root}
                  aria-invalid={tried && problems.root ? true : undefined}
                  onChange={(e) => setRoot(e.target.value)}
                >
                  <option value="">Choose…</option>
                  {roots.data?.map((r) => (
                    <option key={r.id} value={r.id} disabled={!r.writable}>
                      {r.path}
                      {r.writable ? '' : ' (read-only)'}
                    </option>
                  ))}
                </select>
              </FormRow>
              <FormRow title="Folder" detail="An existing folder inside the root. Leave empty to use the root itself.">
                <input
                  className="input"
                  aria-label="Folder inside the storage root"
                  placeholder="backups/crashplan"
                  value={relative}
                  onChange={(e) => setRelative(e.target.value)}
                />
              </FormRow>
              <FormRow title="Confirm" detail={`Type ${word} to confirm this change.`}>
                <input
                  className="input"
                  aria-label={`Type ${word} to confirm`}
                  value={confirm}
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(e) => setConfirm(e.target.value)}
                />
              </FormRow>
            </fieldset>
            <div className="row">
              <div className="row-main">
                {attempt && (
                  <span
                    className="row-detail"
                    title="Sending this request again returns its stored result instead of running it twice."
                  >
                    Request {attempt.requestId}
                  </span>
                )}
              </div>
              <div className="button-row">
                {attempt && !operation.isPending && (
                  <button
                    type="button"
                    className="btn"
                    onClick={() => {
                      setAttempt(undefined)
                      operation.reset()
                      setConfirm('')
                      setTried(false)
                    }}
                  >
                    Start another change
                  </button>
                )}
                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={
                    operation.isPending ||
                    confirm !== word ||
                    (!!attempt && !operation.isError)
                  }
                >
                  {operation.isPending
                    ? 'Sending…'
                    : attempt && operation.isError
                      ? 'Try again'
                      : actions[action].button}
                </button>
              </div>
            </div>
          </form>
        </details>
      )}
      {result && resultText && (
        <div className="row-block">
          <Notice tone={resultText.tone}>
            <p>{resultText.title}</p>
            {result.existingArchive && (
              <p>Review the existing archive, then reconnect it as a separate change.</p>
            )}
            {result.sourceMissing && (
              <p>Only the archive location in CrashPlan changed. No files were copied.</p>
            )}
            {result.jobId && (
              <p>
                The files are copied in the background. Accepted does not mean the copy has
                finished. <span className="muted">Job {result.jobId}</span>
              </p>
            )}
            {result.destinationId && (
              <p>
                Destination: {deviceName(result.destinationId)}{' '}
                <span className="muted">(ID {result.destinationId})</span>
              </p>
            )}
          </Notice>
        </div>
      )}
    </Section>
  )
}
