import { test } from 'node:test'
import assert from 'node:assert/strict'
import { engineReady } from '../src/features/overview/presentation.ts'

test('a running process is not a ready native connection', () => {
  assert.equal(engineReady(undefined), undefined)
  assert.equal(engineReady({ status: 'running' }), false)
  assert.equal(engineReady({ status: 'running', native: { nativeReady: true, eventsReady: false } }), false)
  assert.equal(engineReady({ status: 'stopped', native: { nativeReady: true, eventsReady: true } }), false)
  assert.equal(engineReady({ status: 'running', native: { nativeReady: true, eventsReady: true } }), true)
})
