// CrashPlan's history log ("history.log"): one line per event with a level.
export function eventLevel(level: string) {
  switch (level) {
    case 'W':
      return { label: 'Warning', tone: 'orange' as const, dot: 'orange' }
    case 'E':
    case 'S':
      return { label: 'Error', tone: 'red' as const, dot: 'red' }
    default:
      return { label: 'Info', tone: undefined, dot: '' }
  }
}

/** Events about backup, scans, restores — not service starts or settings. */
export function isBackupEvent(message: string) {
  return /backup|scann|restor|synchroniz|maintenance|archive|\[.+\]/i.test(message) &&
    !/^CrashPlan (started|stopped)/i.test(message)
}

/**
 * Keys that stay with their entries when newer ones arrive or older ones drop
 * out of a limited window: time, message length and the position among
 * otherwise identical entries.
 */
export function eventKeys(entries: readonly { timestamp: string; message: string }[]): string[] {
  const seen = new Map<string, number>()
  return entries.map((e) => {
    const base = e.timestamp + ':' + e.message.length
    const n = seen.get(base) ?? 0
    seen.set(base, n + 1)
    return base + ':' + n
  })
}

/** Most important first (lower priority number), as CrashPlan orders them. */
export function sortAlerts<T extends { priority?: number }>(alerts: T[]): T[] {
  return alerts.toSorted((a, b) => (a.priority ?? 1e9) - (b.priority ?? 1e9))
}

// Alerts the desktop app shows only as onboarding hints; meaningless here.
const hiddenAlerts = new Set(['HelpIntroAlert'])
export function visibleAlert(name: string) {
  return !hiddenAlerts.has(name) && !/^Help.*Intro/i.test(name)
}
