import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../shared/api'
import {
  ErrorNotice,
  Field,
  Notice,
  Row,
  Section,
  Segmented,
  Spinner,
} from '../../shared/components'
import './account.css'

/** Only shown while CrashPlan keeps an unfinished device replacement. */
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
  if (!retained && !state.error && !complete.isSuccess && !complete.error)
    return null
  return (
    <Section
      title="Unfinished device replacement"
      description="CrashPlan saved a device replacement that was not completed. Finish or cancel it here."
    >
      {(state.error || complete.error) && (
        <div className="row-block">
          <ErrorNotice error={state.error ?? complete.error} />
        </div>
      )}
      {complete.isSuccess && (
        <div className="row-block">
          <Notice tone="success">The saved replacement workflow is closed.</Notice>
        </div>
      )}
      {retained && (
        <>
          <Row
            title="Status"
            value={state.data?.inProgress ? 'In progress' : 'Available'}
          />
          {state.data?.deviceSelectedGuid && (
            <Row
              title="Selected device"
              value={<code>{state.data.deviceSelectedGuid}</code>}
            />
          )}
          <div className="row-block">
            <Notice>
              Closing this workflow does not reverse device identity changes or
              stop backup and restore jobs.
            </Notice>
            <form
              className="account-form"
              noValidate
              onSubmit={(event) => {
                event.preventDefault()
                if (complete.isPending || confirmation !== required) return
                complete.mutate()
              }}
            >
              <Segmented
                label="Action"
                value={action}
                options={[
                  { value: 'finish', label: 'Finish setup' },
                  { value: 'cancel', label: 'Cancel setup' },
                ]}
                onChange={(value) => {
                  if (complete.isPending) return
                  setAction(value)
                  setConfirmation('')
                }}
              />
              <Field label={<>To confirm, type <code>{required}</code></>}>
                <input
                  className="input"
                  autoComplete="off"
                  spellCheck={false}
                  autoCapitalize="characters"
                  disabled={complete.isPending}
                  value={confirmation}
                  onChange={(event) => setConfirmation(event.target.value)}
                />
              </Field>
              <div className="button-row">
                <button
                  className={
                    'btn ' +
                    (action === 'cancel' ? 'btn-destructive' : 'btn-primary')
                  }
                  disabled={complete.isPending || confirmation !== required}
                >
                  {complete.isPending && <Spinner size={14} />}
                  Close replacement workflow
                </button>
              </div>
            </form>
          </div>
        </>
      )}
    </Section>
  )
}
