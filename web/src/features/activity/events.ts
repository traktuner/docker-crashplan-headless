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

// Alerts the desktop app shows only as onboarding hints; meaningless here.
const hiddenAlerts = new Set(['HelpIntroAlert'])
export function visibleAlert(name: string) {
  return !hiddenAlerts.has(name) && !/^Help.*Intro/i.test(name)
}
