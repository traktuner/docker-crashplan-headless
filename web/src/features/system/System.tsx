import { useMutation, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { api } from '../../shared/api'
import { ErrorNotice, Notice, Page, Row, Section, useConfirm } from '../../shared/components'

export function System() {
  const client = useQueryClient()
  const restart = useMutation({
    mutationFn: () => api.restartEngine(),
    onSuccess: () => client.invalidateQueries(),
  })
  const [confirmDialog, ask] = useConfirm()
  return (
    <Page title="System" subtitle="Manage the backup engine without recreating the container.">
      {confirmDialog}
      <Section
        title="Backup engine"
        footer="Your configuration and backup history stay on the persistent volume."
      >
        <Row
          icon={
            <span className="row-icon orange" aria-hidden="true">
              <RefreshCw size={16} />
            </span>
          }
          title="Restart CrashPlan"
          detail="Stops and starts the engine. Backups and restores that are running are interrupted."
        >
          <button
            type="button"
            className="btn btn-destructive"
            disabled={restart.isPending}
            onClick={async () => {
              if (
                await ask({
                  title: 'Restart CrashPlan?',
                  message: 'Backups and restores that are running are interrupted. They continue after the engine is back.',
                  confirm: 'Restart',
                  destructive: true,
                })
              )
                restart.mutate()
            }}
          >
            {restart.isPending ? 'Restarting…' : 'Restart…'}
          </button>
        </Row>
      </Section>
      <ErrorNotice error={restart.error} />
      {restart.isSuccess && (
        <Notice tone="success">
          Restart requested. Status and settings reload once the engine is back.
        </Notice>
      )}
    </Page>
  )
}
