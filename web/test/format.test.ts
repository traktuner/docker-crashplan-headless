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
  assert.equal(s.title, 'Synchronizing block information')
  assert.equal(s.progress?.value, 25)
  const unknown = backupStatus({ engineReady: true, signedIn: true, progress: [dest({ validating: true, sync: { current: '60800000', total: '15161306', percent: -1, files: false } })] })
  assert.equal(unknown.progress?.value, undefined)
  assert.match(unknown.progress!.text, /60.8 million blocks checked/)
})

test('a finished backup reads complete; sign-in and offline win', () => {
  assert.equal(backupStatus({ engineReady: true, signedIn: true, progress: [dest({})] }).kind, 'complete')
  assert.equal(backupStatus({ engineReady: true, signedIn: false, progress: undefined }).kind, 'sign-in')
  assert.equal(backupStatus({ engineReady: false, signedIn: true, progress: undefined }).kind, 'offline')
})
