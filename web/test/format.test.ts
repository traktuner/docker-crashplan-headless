import test from 'node:test'
import assert from 'node:assert/strict'
import { bytes, duration, splitDuration, joinDuration, compactCount, words, percent } from '../src/shared/format.ts'
import { backupStatus } from '../src/shared/backupStatus.ts'

test('sizes are human readable in decimal units', () => {
  assert.equal(bytes('2470567748275'), '2.47 TB')
  assert.equal(bytes('93500000000000'), '93.5 TB')
  assert.equal(bytes('999'), '999 bytes')
  assert.equal(bytes('1000'), '1.00 KB')
  assert.equal(bytes('-1'), '—')
})

test('milliseconds read as days and hours', () => {
  assert.equal(duration('604800000'), '7 days')
  assert.equal(duration('2678400000'), '31 days')
  assert.equal(duration('59940000'), '16 hours 39 minutes')
  assert.deepEqual(splitDuration('604800000'), { amount: '7', unit: 'days' })
  assert.equal(joinDuration('7', 'days'), '604800000')
  assert.equal(joinDuration('', 'days'), undefined)
})

test('counts, words and percentages', () => {
  assert.equal(compactCount('60800000'), '60.8 million')
  assert.equal(words('HelpIntroAlert'), 'Help intro alert')
  assert.equal(percent(42.345), '42.3%')
  assert.equal(percent(-1), undefined)
})

const dest = (over: Record<string, unknown>) => ({
  targetId: '8', name: 'Cloud', backingUp: false, validating: false, outOfSpace: false,
  completedRateBytesPerSecond: '0', sets: [], ...over,
}) as never

test('synchronization is reported with progress like the desktop app', () => {
  const s = backupStatus({ engineReady: true, signedIn: true, progress: [dest({ validating: true, sync: { current: '25', total: '100', percent: 25, files: false } })] })
  assert.equal(s.kind, 'sync')
  assert.equal(s.title, 'Syncing backup index')
  assert.equal(s.progress?.value, 25)
  assert.equal(s.progress?.text, '25%')
  const unknown = backupStatus({ engineReady: true, signedIn: true, progress: [dest({ validating: true, sync: { current: '60800000', total: '15161306', percent: -1, files: false } })] })
  assert.equal(unknown.progress?.value, undefined)
  assert.doesNotMatch(unknown.progress!.text, /million|files|blocks|\d{4}/)
  assert.equal(unknown.title, 'Syncing backup index')
})

const set = (over: Record<string, unknown> = {}) => ({
  backupSetId: '1', scanning: false, selected: true, filesScanned: '725748', bytesScanned: '2470567748275',
  remainingFiles: '0', remainingBytes: '0', lastBackup: '1', nextBackup: '0', resumeTime: '0', ...over,
})
const status = (destinations: unknown[], now = 1_000_000) =>
  backupStatus({ engineReady: true, signedIn: true, progress: destinations as never, now })

test('sign-in and offline win; no destination is not complete', () => {
  assert.equal(backupStatus({ engineReady: true, signedIn: false, progress: undefined }).kind, 'sign-in')
  assert.equal(backupStatus({ engineReady: false, signedIn: true, progress: undefined }).kind, 'offline')
  assert.equal(status([dest({})]).kind, 'no-destination')
})

test('"Backup complete" only when CrashPlan says complete for every pair, never as a fallback', () => {
  assert.equal(status([dest({ sets: [set({ complete: true, percent: 100 })] })]).kind, 'complete')
  // Older backend without the field: nothing remaining is still not "complete".
  const unknown = status([dest({ sets: [set()] })])
  assert.notEqual(unknown.kind, 'complete')
  assert.equal(unknown.title, 'Waiting for backup')
  // One pair complete, another not: not complete.
  const mixed = status([
    dest({ sets: [set({ complete: true })] }),
    dest({ targetId: '9', name: 'Local', sets: [set({ complete: false, remainingFiles: '3', remainingBytes: '3000' })] }),
  ])
  assert.equal(mixed.kind, 'waiting')
})

test('no scan statistics yet reads neutral "Preparing…"', () => {
  const s = status([dest({ sets: [set({ filesScanned: '0', bytesScanned: '0' })] })])
  assert.equal(s.kind, 'preparing')
  assert.equal(s.title, 'Preparing…')
  assert.equal(s.tone, 'idle')
})

test('a system pause comes first; disconnected destinations wait for a connection', () => {
  const paused = status([
    dest({ backingUp: true, systemResumeTime: '4600000', sets: [set({ remainingFiles: '5', remainingBytes: '5000', percent: 50 })] }),
  ])
  assert.equal(paused.kind, 'system-paused')
  assert.deepEqual(paused.actions, { run: false, pause: false, resume: true })
  assert.match(paused.detail, /1 hour/)
  assert.equal(status([dest({ connected: false, disconnectCode: 'DISCONNECTED', sets: [set({ complete: true })] })]).title, 'Waiting for connection')
  assert.equal(status([dest({ connected: false, disconnectCode: 'NOT_AUTHORIZED', sets: [set({ complete: true })] })]).title, 'Destination disconnected')
  // Missing connection state is unknown, not disconnected.
  assert.equal(status([dest({ sets: [set({ complete: true })] })]).kind, 'complete')
})

test('backing up shows the engine percentage, amounts left and an ETA only above 10 kB/s', () => {
  const active = set({ percent: 92.5, totalBytes: '2470000000000', remainingBytes: '184000000000', remainingFiles: '18342', sendingPath: '/a/b/c.txt' })
  const fast = status([dest({ backingUp: true, completedRateBytesPerSecond: '3250000', sets: [active] })])
  assert.equal(fast.kind, 'backup')
  assert.equal(fast.progress?.value, 92.5)
  assert.equal(fast.progress?.text, '92.5% · 184 GB of 2.47 TB left')
  assert.match(fast.detail, /^About .* remaining$/)
  assert.deepEqual(fast.actions, { run: false, pause: true, resume: false })
  const slow = status([dest({ backingUp: true, completedRateBytesPerSecond: '9000', sets: [active] })])
  assert.ok(!/remaining/.test(slow.detail))
  // No percent from the backend: the bar is indeterminate, never a guess.
  const old = status([dest({ backingUp: true, completedRateBytesPerSecond: '3250000', sets: [set({ remainingBytes: '5', remainingFiles: '1' })] })])
  assert.equal(old.progress?.value, undefined)
})

test('counts below one million are exact', () => {
  assert.equal(compactCount('18342'), '18,342')
  assert.equal(compactCount('999999'), '999,999')
  assert.equal(compactCount('1200000'), '1.2 million')
})
