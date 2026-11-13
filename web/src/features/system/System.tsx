import { useMutation, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { api } from '../../shared/api'
import { PageTitle, ErrorNotice } from '../../shared/components'

export function System() {
  const client = useQueryClient()
  const restart = useMutation({
    mutationFn: () => api.restartEngine(),
    onSuccess: () => client.invalidateQueries(),
  })
  return (
    <>
      <PageTitle
        title="System"
        subtitle="Manage the backup engine without recreating the container."
      />
      <section className="card form-card">
        <h2>Restart CrashPlan</h2>
        <p className="muted">
          A restart interrupts active backup and restore work. Your
          configuration stays on the persistent volume.
        </p>
        <button
          className="secondary"
          onClick={() => {
            if (
              confirm(
                'Restart the CrashPlan engine? Active work will be interrupted.',
              )
            )
              restart.mutate()
          }}
          disabled={restart.isPending}
        >
          <RefreshCw size={16} /> Restart engine
        </button>
        <ErrorNotice error={restart.error} />
        {restart.isSuccess && (
          <div className="notice success">Restart requested.</div>
        )}
      </section>
    </>
  )
}
