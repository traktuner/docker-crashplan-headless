// Wording for the stored outcome of a device or local-archive operation.
import { words } from '../../shared/format'

export type OutcomeTone = 'info' | 'success' | 'warning' | 'error'

const outcomes: Record<string, { title: string; tone: OutcomeTone }> = {
  accepted: { title: 'CrashPlan accepted the request.', tone: 'info' },
  completed: { title: 'Done.', tone: 'success' },
  relocated: { title: 'The archive location was updated.', tone: 'success' },
  'needs-attach': { title: 'An archive already exists in this folder.', tone: 'warning' },
  'source-missing': { title: 'The current archive folder was not found.', tone: 'warning' },
  indeterminate: {
    title: 'CrashPlan did not confirm the outcome. Check the result before trying again.',
    tone: 'warning',
  },
  rejected: { title: 'CrashPlan rejected the request.', tone: 'error' },
}

export function outcome(state: string): { title: string; tone: OutcomeTone } {
  return outcomes[state] ?? { title: words(state) + '.', tone: 'info' }
}
