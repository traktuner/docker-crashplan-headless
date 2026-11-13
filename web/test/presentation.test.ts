import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  backupPhase,
  formatBytes,
  displayTime,
} from '../src/features/overview/presentation.ts'
import type { BackupSummary } from '../src/shared/api.ts'
const summary = (activities: number, remainingFiles = 1): BackupSummary => ({
  activities,
  remainingFiles,
  remainingBytes: '1',
  sendRateBytesPerSecond: '0',
  filesBackedUp: 0,
  lastBackup: '0',
  status: 'SAFE',
  systemBusy: false,
  batteryBlocked: false,
})
test('unknown snapshots and waiting files never imply completed protection', () => {
  assert.equal(backupPhase(undefined), 'unknown')
  assert.equal(backupPhase(summary(100)), 'validating')
  assert.equal(backupPhase(summary(69)), 'sending')
  assert.equal(backupPhase(summary(84)), 'scanning')
  assert.equal(backupPhase(summary(68)), 'waiting')
  assert.equal(backupPhase(summary(68, 0)), 'idle')
})
test('large native byte values keep integer precision and explicit binary units', () => {
  assert.equal(formatBytes(undefined), '—')
  assert.equal(formatBytes('not-reported'), '—')
  assert.equal(formatBytes('0'), '0 B')
  assert.equal(formatBytes('1048576'), '1.00 MiB')
  assert.equal(formatBytes('9007199254740993'), '8.00 PiB')
  assert.equal(formatBytes('27577', '/s'), '26.9 KiB/s')
})
test('missing or invalid timestamps remain unavailable', () => {
  for (const x of [undefined, '0', 'bad', '9223372036854775807'])
    assert.equal(displayTime(x), 'Not reported')
})

test('connection and account readiness take precedence over stale transfer data', async () => {
  const { backupPresentation } =
    await import('../src/features/overview/presentation.ts')
  assert.equal(
    backupPresentation(undefined, undefined, summary(69)).key,
    'checking',
  )
  assert.equal(backupPresentation(false, true, summary(69)).key, 'offline')
  assert.equal(backupPresentation(true, false, summary(69)).key, 'sign-in')
  assert.equal(backupPresentation(true, true, undefined).key, 'checking')
  assert.equal(backupPresentation(true, true, summary(69)).key, 'sending')
  assert.equal(backupPresentation(true, true, summary(100)).key, 'validating')
})

test('native lossless counters remain exact and readable', async () => {
  const { parse } = await import('lossless-json')
  const { formatCount } =
    await import('../src/features/overview/presentation.ts')
  const input = parse('{"files":3532184,"large":9007199254740993}') as Record<
    string,
    unknown
  >
  assert.equal(formatCount(input.files), '3,532,184')
  assert.equal(formatCount(input.large), '9,007,199,254,740,993')
  assert.equal(formatCount(undefined), '—')
})

test('a running process is not a ready native connection', async () => {
  const { engineReady } =
    await import('../src/features/overview/presentation.ts')
  assert.equal(engineReady(undefined), undefined)
  assert.equal(engineReady({ status: 'running' }), false)
  assert.equal(
    engineReady({
      status: 'running',
      native: { nativeReady: true, eventsReady: false },
    }),
    false,
  )
  assert.equal(
    engineReady({
      status: 'running',
      native: { nativeReady: true, eventsReady: true },
    }),
    true,
  )
})
