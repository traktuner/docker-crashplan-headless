import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../shared/api'
import { ErrorNotice } from '../../shared/components'

export function ReplacementWorkflow() {
  const client = useQueryClient()
  const state = useQuery({
    queryKey: ['replacement'],
    queryFn: () => api.replacementState(),
  })
  const [action, setAction] = useState<'finish' | 'cancel'>('finish')
  const [confirmation, setConfirmation] = useState('')
  const complete = useMutation({
    mutationFn: () => api.completeReplacement(action, confirmation),
    onSettled: () => {
      setConfirmation('')
      for (const key of ['replacement', 'local-archive-policy']) {
        void client.invalidateQueries(
          { queryKey: [key] },
          { cancelRefetch: false },
        )
      }
    },
  })
  const retained =
    state.data &&
    (state.data.inProgress ||
      state.data.available ||
      state.data.deviceSelectedGuid ||
      state.data.screenState)
  const required = action.toUpperCase() + ' REPLACEMENT'
  return (
    <section className="card form-card">
      <h2>Device replacement workflow</h2>
      <ErrorNotice error={state.error ?? complete.error} />
      {state.isPending && <p role="status">Reading replacement state…</p>}
      {state.data && !retained && (
        <p className="muted">No unfinished replacement workflow.</p>
      )}
      {retained && (
        <>
          <p>
            A saved replacement workflow is{' '}
            {state.data?.inProgress ? 'in progress' : 'available'}.
          </p>
          {state.data?.deviceSelectedGuid && (
            <p>
              Selected device: <code>{state.data.deviceSelectedGuid}</code>
            </p>
          )}
          <p className="notice">
            Closing this workflow does not reverse device identity changes or
            stop backup and restore jobs.
          </p>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              complete.mutate()
            }}
          >
            <label>
              Action
              <select
                value={action}
                disabled={complete.isPending}
                onChange={(event) => {
                  setAction(event.target.value as 'finish' | 'cancel')
                  setConfirmation('')
                }}
              >
                <option value="finish">Finish setup</option>
                <option value="cancel">Cancel setup</option>
              </select>
            </label>
            <label>
              Type {required}
              <input
                required
                autoComplete="off"
                disabled={complete.isPending}
                value={confirmation}
                onChange={(event) => setConfirmation(event.target.value)}
              />
            </label>
            <button disabled={complete.isPending || confirmation !== required}>
              Close replacement workflow
            </button>
          </form>
        </>
      )}
      {complete.isSuccess && (
        <p role="status">The saved replacement workflow is closed.</p>
      )}
    </section>
  )
}
