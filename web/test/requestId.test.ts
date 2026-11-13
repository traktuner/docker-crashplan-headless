import assert from 'node:assert/strict'
import test from 'node:test'
import { newRequestId } from '../src/shared/requestId.ts'

test('request IDs work when randomUUID is unavailable on an HTTP origin', () => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'crypto')!
  try {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: {
        getRandomValues: (bytes: Uint8Array) => {
          assert.equal(bytes.length, 16)
          bytes.fill(15)
          return bytes
        },
      },
    })
    assert.equal(newRequestId(), '0f'.repeat(16))
  } finally {
    Object.defineProperty(globalThis, 'crypto', descriptor)
  }
})
test('request IDs retain independent cryptographic randomness', () => {
  const first = newRequestId(),
    second = newRequestId()
  assert.match(first, /^[a-f0-9]{32}$/)
  assert.notEqual(first, second)
})
