// The one contextual backup control: Back up now, Pause… or Resume, for
// everything or for one backup set at one destination. Used by the status
// card and by the Activity page so both behave the same.
import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Pause, Play, Upload } from 'lucide-react'
import { api } from './api'
import type { BackupStatus } from './backupStatus'
import { Dialog, ErrorNotice, Segmented } from './components'

const pauseOptions = [
  { value: '15', label: '15 min' },
  { value: '60', label: '1 hour' },
  { value: '240', label: '4 hours' },
  { value: '1440', label: '1 day' },
]
const done = {
  run: 'Backup requested.',
  pause: 'Backup paused.',
  resume: 'Backup resumed.',
}

export function BackupActions({
  actions,
  backupSetId,
  destinationId,
  small,
}: {
  actions: BackupStatus['actions']
  backupSetId?: string
  destinationId?: string
  small?: boolean
}) {
  const client = useQueryClient()
  const [asking, setAsking] = useState(false)
  const [minutes, setMinutes] = useState('60')
  const scope = backupSetId && destinationId ? { backupSetId, destinationId } : backupSetId ? { backupSetId } : {}
  const control = useMutation({
    mutationFn: (action: 'run' | 'pause' | 'resume') =>
      action === 'run'
        ? api.runBackup(scope)
        : api.backupControl(action, action === 'pause' ? { ...scope, durationMinutes: minutes } : scope),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['backup-progress'] })
      void client.invalidateQueries({ queryKey: ['backup-summary'] })
    },
  })
  // A confirmation is a moment, not a state: it fades after a few seconds.
  useEffect(() => {
    if (!control.isSuccess) return
    const timer = setTimeout(() => control.reset(), 4000)
    return () => clearTimeout(timer)
  }, [control.isSuccess]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!actions.run && !actions.pause && !actions.resume) return null
  const size = small ? ' btn-sm' : ''
  const busy = control.isPending
  const where = backupSetId ? 'this backup set' : 'all backups'
  return (
    <div className="backup-actions">
      <div className="button-row">
        {actions.resume && (
          <button type="button" className={'btn btn-primary' + size} disabled={busy} onClick={() => control.mutate('resume')}>
            <Play size={14} aria-hidden="true" /> Resume
          </button>
        )}
        {actions.run && !actions.resume && (
          <button type="button" className={'btn' + size} disabled={busy} onClick={() => control.mutate('run')}>
            <Upload size={14} aria-hidden="true" /> Back up now
          </button>
        )}
        {actions.pause && !actions.resume && (
          <button type="button" className={'btn' + size} disabled={busy} onClick={() => setAsking(true)}>
            <Pause size={14} aria-hidden="true" /> Pause…
          </button>
        )}
        {control.isSuccess && control.variables && (
          <span className="row-detail" role="status">
            {done[control.variables]}
          </span>
        )}
      </div>
      <ErrorNotice error={control.error} />
      <Dialog
        open={asking}
        title={'Pause ' + where}
        onClose={() => setAsking(false)}
        actions={
          <>
            <button type="button" className="btn" onClick={() => setAsking(false)}>
              Cancel
            </button>
            <button
              type="button"
              className="btn btn-primary"
              autoFocus
              onClick={() => {
                setAsking(false)
                control.mutate('pause')
              }}
            >
              Pause
            </button>
          </>
        }
      >
        <p className="dialog-message">Backup resumes automatically after the chosen time. You can resume earlier.</p>
        <Segmented label="Pause for" value={minutes} options={pauseOptions} onChange={setMinutes} />
      </Dialog>
    </div>
  )
}
