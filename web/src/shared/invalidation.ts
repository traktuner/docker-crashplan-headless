import type { QueryClient } from '@tanstack/react-query'
// Native notifications report state changes, including completed browse reads.
// Browsing and configuration queries refresh only through explicit UI actions.
const statusKeys = new Set([
  'state',
  'health',
  'scans',
  'backup-summary',
  'alerts',
  'restore-jobs',
  'restore-operations',
  'restore-helper',
  'migration',
])
export function refreshStatus(client: QueryClient) {
  return client.invalidateQueries(
    { predicate: (query) => statusKeys.has(String(query.queryKey[0])) },
    { cancelRefetch: false },
  )
}
