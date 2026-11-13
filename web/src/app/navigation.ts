export type Page =
  | 'overview'
  | 'activity'
  | 'sets'
  | 'restore'
  | 'settings'
  | 'security'
  | 'account'
  | 'system'
export const pages: {
  id: Page
  label: string
  group: 'Backup' | 'Configuration'
  keywords: string
}[] = [
  {
    id: 'overview',
    label: 'Overview',
    group: 'Backup',
    keywords: 'dashboard status backup',
  },
  {
    id: 'sets',
    label: 'Backup sets',
    group: 'Backup',
    keywords: 'files folders selection schedules exclusions scan destinations',
  },
  {
    id: 'restore',
    label: 'Restore files',
    group: 'Backup',
    keywords: 'recover archive history versions download',
  },
  {
    id: 'activity',
    label: 'Activity',
    group: 'Backup',
    keywords: 'transfer progress alerts history pause resume',
  },
  {
    id: 'settings',
    label: 'Settings',
    group: 'Configuration',
    keywords: 'preferences bandwidth cpu network',
  },
  {
    id: 'security',
    label: 'Security',
    group: 'Configuration',
    keywords: 'keys encryption password',
  },
  {
    id: 'account',
    label: 'Account & devices',
    group: 'Configuration',
    keywords: 'login sso migration inherit destinations',
  },
  {
    id: 'system',
    label: 'System',
    group: 'Configuration',
    keywords: 'engine restart',
  },
]
export function pageFromHash(hash: string): Page {
  return pages.find((p) => '#' + p.id === hash)?.id ?? 'overview'
}
export function findPages(query: string) {
  const term = query.trim().toLowerCase()
  return pages.filter((p) =>
    (p.label + ' ' + p.keywords).toLowerCase().includes(term),
  )
}
