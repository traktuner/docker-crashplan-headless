import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { once } from 'node:events'
import test from 'node:test'
import { API, APIError } from '../src/shared/api.ts'

test('an open page keeps its account context and blocks stale mutations and key exports', async () => {
  let context = 'process:1'
  const sent: string[] = []
  const server = createServer((request, response) => {
    response.setHeader('X-CPH-Engine-Context', context)
    if (request.method !== 'GET') {
      sent.push(String(request.headers['x-cph-engine-context']))
      response.end('{"data":{}}')
    } else response.end('{"data":{"type":"AccountPassword"}}')
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    await api.security()
    await api.saveSettings('backup', { idleThrottleRate: '30' })
    assert.deepEqual(sent, ['process:1'])
    context = 'process:2'
    await assert.rejects(api.security(), (error: unknown) => error instanceof APIError && error.code === 'NATIVE_SESSION_CHANGED')
    await assert.rejects(api.saveSettings('backup', { idleThrottleRate: '40' }), APIError)
    await assert.rejects(api.exportArchiveKey(), APIError)
    assert.deepEqual(sent, ['process:1'])
    // A deliberate new page can bind to the newly displayed account.
    const reloaded = new API(`http://127.0.0.1:${address.port}`)
    await reloaded.security()
    await reloaded.saveSettings('backup', { idleThrottleRate: '40' })
    assert.deepEqual(sent, ['process:1', 'process:2'])
  } finally { server.close() }
})
