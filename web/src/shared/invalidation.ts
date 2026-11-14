import type { QueryClient } from '@tanstack/react-query'
// Native notifications report state changes, including completed browse reads.
// Browsing and configuration queries refresh only through explicit UI actions.
const statusKeys = new Set([
  'state',
  'health',
  'scans',
  'backup-summary',
  'backup-progress',
  'devices',
  'alerts',
  'restore-jobs',
  'restore-operations',
  'restore-helper',
  'migration',
  'capabilities',
])
export function refreshStatus(client: QueryClient) {
  return client.invalidateQueries(
    {
      // A failed query without data returns to its loading state while it
      // refetches; refreshing it on every notification made the page flicker.
      // Such queries retry on their own interval instead.
      predicate: (query) =>
        statusKeys.has(String(query.queryKey[0])) &&
        !(query.state.status === 'error' && query.state.data === undefined),
    },
    { cancelRefetch: false },
  )
}
