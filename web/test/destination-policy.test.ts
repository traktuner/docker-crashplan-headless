import assert from 'node:assert/strict'
import test from 'node:test'
import { allowedArchiveSources } from '../src/features/destinations/policy.ts'
import type { LocalArchivePolicy } from '../src/shared/api.ts'

test('owned incoming archives do not inherit local-folder policy', () => {
  const policy: LocalArchivePolicy = {
    allowed: false,
    ownDevices: true,
    selfId: '7',
    devices: [
      { id: '7', name: 'Self', own: true, self: true },
      { id: '9', name: 'Owned', own: true, self: false },
      { id: '10', name: 'Other', own: false, self: false },
    ],
  }
  assert.deepEqual(
    allowedArchiveSources(policy).map((d) => d.id),
    ['9'],
  )
  assert.deepEqual(
    allowedArchiveSources({ ...policy, allowed: true, ownDevices: false }).map(
      (d) => d.id,
    ),
    ['7'],
  )
  assert.deepEqual(allowedArchiveSources({ ...policy, ownDevices: false }), [])
})

test('owned attachment uses the adoption exception only for attachment', () => {
  const policy = {
    allowed: false,
    ownDevices: false,
    canAttachOwn: true,
    selfId: '7',
    devices: [
      { id: '9', name: 'Owned archive', own: true, self: false },
      { id: '10', name: 'Other', own: false, self: false },
    ],
  }
  assert.deepEqual(
    allowedArchiveSources(policy, 'attach').map((d) => d.id),
    ['9'],
  )
  assert.deepEqual(allowedArchiveSources(policy, 'move'), [])
  assert.deepEqual(
    allowedArchiveSources({ ...policy, canAttachOwn: false }, 'attach'),
    [],
  )
})
