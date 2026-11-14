import test from 'node:test'
import assert from 'node:assert/strict'
import {
  restoreSources,
  restoreDestinations,
  restoreSets,
  toggleSelection,
  restoreJobView,
} from '../src/features/restore/model.ts'

const device = (over: Record<string, unknown>) => ({
  id: '1', name: 'x', source: false, target: false, availableTarget: false,
  self: false, blocked: false, deleted: false, ...over,
}) as never

test('every computer of the account is a restore source, destinations are not', () => {
  const devices = [
    device({ id: '7', name: 'server', self: true }),
    device({ id: '9', name: 'laptop', owned: true }),
    device({ id: '8', name: 'Cloud', target: true, availableTarget: true, cloud: true }),
    device({ id: '6', name: 'Old cloud entry', cloud: true }),
    device({ id: '4200', name: 'Friend' }),
  ]
  assert.deepEqual(restoreSources(devices).map((d: { id: string }) => d.id), ['7', '9'])
  assert.deepEqual(restoreDestinations(devices).map((d: { id: string }) => d.id), ['8'])
})

test('archives without listed sets fall back to the default set', () => {
  assert.deepEqual(restoreSets([]), [{ id: '1', name: 'All files' }])
  assert.equal(restoreSets(undefined), undefined)
  assert.deepEqual(restoreSets([{ id: '2', name: 'Photos' }]), [{ id: '2', name: 'Photos' }])
})

test('unselecting a folder drops exclusions inside it', () => {
  const folder = { path: '/data', backupSetId: '1', timestamp: '0', isFolder: true }
  const inner = { path: '/data/tmp', backupSetId: '1', timestamp: '0', isFolder: true }
  let state = toggleSelection({}, {}, 'a', folder)
  state = { ...state, excluded: { b: inner } }
  const after = toggleSelection(state.selection, state.excluded, 'a', folder)
  assert.deepEqual(after.selection, {})
  assert.deepEqual(after.excluded, {})
})

test('restore jobs offer only the control that applies', () => {
  const job = (over: Record<string, unknown>) => ({
    id: '1', sourceId: '1', targetId: '2', completed: false, canceled: false, numFiles: '1', filesRestored: '0',
    numBytes: '1', bytesRestored: '0', restorePath: '/restore', imported: false, admissible: true, ...over,
  }) as never
  const running = restoreJobView(job({ state: 'running' }))
  assert.equal(running.label, 'Running')
  assert.ok(running.canStop && !running.canResume)
  const stopped = restoreJobView(job({ state: 'stopped' }))
  assert.ok(!stopped.canStop && stopped.canResume)
  assert.equal(restoreJobView(job({ state: 'stopped', admissible: false })).canResume, false)
  const waiting = restoreJobView(job({ state: 'waiting', blockedReason: 'SOMETHING_ELSE' }))
  assert.equal(waiting.label, 'Waiting')
  assert.equal(waiting.blocked, 'Something else.')
  assert.ok(restoreJobView(job({ state: 'completed' })).finished)
  assert.equal(restoreJobView(job({ state: 'canceled' })).label, 'Canceled')
  // Older backends: derived from the flags; both controls when unknown.
  assert.equal(restoreJobView(job({ completed: true })).state, 'completed')
  const unknown = restoreJobView(job({}))
  assert.ok(unknown.canStop && unknown.canResume)
})
