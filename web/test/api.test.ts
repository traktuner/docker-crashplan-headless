import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { once } from 'node:events'
import test from 'node:test'
import {
  API,
  APIError,
  identifier,
  archiveSelection,
  generateArchiveKey,
} from '../src/shared/api.ts'

test('new custom keys use the vendor V1 key length and independent secure randomness', () => {
  const first = generateArchiveKey(),
    second = generateArchiveKey()
  assert.equal(Buffer.from(first, 'base64').length, 56)
  assert.notEqual(first, second)
})

test('backup identifiers survive a real HTTP response without rounding', async () => {
  const server = createServer((request, response) => {
    response.setHeader('Content-Type', 'application/json')
    response.end('{"data":[{"id":9007199254740993,"name":"Fixture"}]}')
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    const data = await api.backupSets<Array<{ id: unknown }>>()
    assert.equal(identifier(data[0]?.id), '9007199254740993')
  } finally {
    server.close()
  }
})

test('a potentially started scan never retries and keeps a visible error category', async () => {
  let calls = 0
  const server = createServer((request, response) => {
    calls++
    response.writeHead(409, { 'Content-Type': 'application/json' })
    response.end(
      '{"error":{"code":"NATIVE_OPERATION_INDETERMINATE","message":"The action may have started."}}',
    )
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    await assert.rejects(
      api.scan('7'),
      (error: unknown) =>
        error instanceof APIError &&
        error.code === 'NATIVE_OPERATION_INDETERMINATE',
    )
    assert.equal(calls, 1)
  } finally {
    server.close()
  }
})

test('unknown error bodies are never surfaced as credential-bearing messages', async () => {
  const server = createServer((request, response) => {
    response.writeHead(500)
    response.end('password=private-canary')
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    await assert.rejects(
      api.status(),
      (error: unknown) =>
        error instanceof APIError && !error.message.includes('canary'),
    )
  } finally {
    server.close()
  }
})

test('missing identifiers remain unknown instead of becoming a default backup set', () => {
  assert.throws(() => identifier(undefined))
  assert.throws(() => identifier(null))
  assert.throws(() => identifier(9007199254740992))
  assert.throws(() => identifier('-1'))
  assert.throws(() => identifier('0'))
  assert.throws(() => identifier('01'))
})

test('restore submissions keep the request ID and never retry an uncertain action', async () => {
  let calls = 0
  const server = createServer(async (request, response) => {
    calls++
    assert.equal(request.url, '/api/v1/restore/jobs')
    let body = ''
    for await (const chunk of request) body += chunk
    assert.equal(JSON.parse(body).requestId, 'a'.repeat(32))
    response.writeHead(409, { 'Content-Type': 'application/json' })
    response.end('{"error":{"code":"NATIVE_OPERATION_INDETERMINATE"}}')
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    await assert.rejects(
      api.startRestore({
        requestId: 'a'.repeat(32),
        sourceId: '7',
        targetId: '8',
        target: { rootId: '0', relative: '' },
        existing: 'RENAME_ORIGINAL',
        permissions: 'current',
        includeDeleted: false,
        items: [
          {
            path: '/data/file',
            backupSetId: '1',
            timestamp: '0',
            isFolder: false,
          },
        ],
      }),
      (error: unknown) =>
        error instanceof APIError &&
        error.code === 'NATIVE_OPERATION_INDETERMINATE',
    )
    assert.equal(calls, 1)
  } finally {
    server.close()
  }
})

test('archive browsing sends exact identifiers and an opaque cursor', async () => {
  const server = createServer(async (request, response) => {
    assert.equal(request.url, '/api/v1/restore/browse')
    let body = ''
    for await (const chunk of request) body += chunk
    assert.equal(JSON.parse(body).sourceId, '9007199254740993')
    assert.equal(JSON.parse(body).cursor, 'AQAAAAAAAAAAAAAAAAAAAA==')
    response.end(
      '{"data":{"items":[],"nextCursor":"AgAAAAAAAAAAAAAAAAAAAA=="}}',
    )
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    const page = await api.browseArchive({
      sourceId: '9007199254740993',
      targetId: '8',
      backupSetId: '1',
      cursor: 'AQAAAAAAAAAAAAAAAAAAAA==',
      at: '0',
      includeDeleted: false,
      fileId: '',
      limit: 200,
    })
    assert.equal(page.nextCursor, 'AgAAAAAAAAAAAAAAAAAAAA==')
  } finally {
    server.close()
  }
})

test('normal folder selection uses the requested archive cutoff, not the folder version timestamp', () => {
  const folder = {
    fileId: 'AQAAAAAAAAAAAAAAAAAAAA==',
    path: '/data',
    timestamp: '100',
    modified: '90',
    size: '0',
    isFolder: true,
  }
  assert.equal(archiveSelection(folder, '1', '0').timestamp, '0')
  assert.equal(archiveSelection(folder, '1', '200').timestamp, '200')
})

test('archive unlock uses the product route and never retries a rejected secret', async () => {
  let calls = 0
  const server = createServer(async (request, response) => {
    calls++
    assert.equal(request.url, '/api/v1/security/unlock')
    assert.equal(request.method, 'POST')
    let body = ''
    for await (const chunk of request) body += chunk
    assert.deepEqual(JSON.parse(body), {
      sourceId: '7',
      type: 'PrivatePassword',
      secret: 'fixture-password',
    })
    response.writeHead(409)
    response.end('{"error":{"code":"NATIVE_OPERATION_REJECTED"}}')
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    await assert.rejects(
      api.unlockArchive({
        sourceId: '7',
        type: 'PrivatePassword',
        secret: 'fixture-password',
      }),
      APIError,
    )
    assert.equal(calls, 1)
  } finally {
    server.close()
  }
})

test('settings patches send only changed fields and keep a visible lock error', async () => {
  let calls = 0
  const server = createServer(async (request, response) => {
    calls++
    assert.equal(request.url, '/api/v1/settings/backup')
    assert.equal(request.method, 'PATCH')
    let body = ''
    for await (const chunk of request) body += chunk
    assert.deepEqual(JSON.parse(body), { changes: { idleThrottleRate: '30' } })
    response.writeHead(409)
    response.end('{"error":{"code":"SETTING_LOCKED"}}')
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    await assert.rejects(
      api.saveSettings('backup', { idleThrottleRate: '30' }),
      (error: unknown) =>
        error instanceof APIError && error.code === 'SETTING_LOCKED',
    )
    assert.equal(calls, 1)
  } finally {
    server.close()
  }
})


test('SSO confirmation sends only the explicitly presented identity and assertion', async () => {
 const requests: {url?: string; body: unknown}[] = []
 const server = createServer(async (request, response) => {
  let body = ''; for await (const chunk of request) body += chunk
  requests.push({url: request.url, body: JSON.parse(body)})
  response.setHeader('Content-Type', 'application/json'); response.end('{"data":{"complete":true}}')
 }).listen(0, '127.0.0.1')
 await once(server, 'listening')
 try {
  const address = server.address(); assert(address && typeof address !== 'string')
  const api = new API(`http://127.0.0.1:${address.port}`)
  await api.confirmSSO('attempt', 'assertion', 'canonical@example.invalid')
  await api.discardSSO('attempt', 'assertion')
  assert.deepEqual(requests, [
   {url:'/api/v1/account/sso/attempt/confirm',body:{assertionId:'assertion',username:'canonical@example.invalid'}},
   {url:'/api/v1/account/sso/attempt/discard',body:{assertionId:'assertion'}},
  ])
 } finally {server.close()}
})

// Every category the product API can return (internal/app/server.go, errorCode).
// A category without a browser message becomes the generic REQUEST_FAILED text.
const serverErrorCodes = [
  'ARCHIVE_SEARCH_PENDING',
  'ARCHIVE_SEARCH_SCOPE_UNSUPPORTED',
  'ARCHIVE_STATISTICS_PENDING',
  'ARCHIVE_UNLOCK_REQUIRED',
  'CONTROL_BUSY',
  'CSRF_REJECTED',
  'ENGINE_NOT_RUNNING',
  'INVALID_REQUEST',
  'LOCAL_ARCHIVE_JOURNAL_UNAVAILABLE',
  'LOGIN_ATTEMPT_ALREADY_COMPLETED',
  'NATIVE_AUTHORITY_UNAVAILABLE',
  'NATIVE_AUTHORIZATION_REQUIRED',
  'NATIVE_ENGINE_UNAVAILABLE',
  'NATIVE_LOGIN_IN_PROGRESS',
  'NATIVE_LOGIN_REJECTED',
  'NATIVE_LOGIN_THROTTLED',
  'NATIVE_MFA_BROWSER_REQUIRED',
  'NATIVE_OPERATION_INDETERMINATE',
  'NATIVE_OPERATION_NOT_DISPATCHED',
  'NATIVE_OPERATION_REJECTED',
  'NATIVE_PROTOCOL_MISMATCH',
  'NATIVE_REGISTRATION_KEY_REQUIRED',
  'NATIVE_REGISTRATION_REJECTED',
  'NATIVE_RESPONSE_PENDING',
  'NATIVE_SESSION_CHANGED',
  'PRIVATE_STATE_UNAVAILABLE',
  'PUBLIC_ORIGIN_MISMATCH',
  'RESTORE_DESTINATION_READ_ONLY',
  'RESTORE_DESTINATION_REJECTED',
  'RESTORE_DUPLICATE_NAMES',
  'RESTORE_JOURNAL_FULL',
  'RESTORE_JOURNAL_UNAVAILABLE',
  'RESTORE_MARKER_REJECTED',
  'RESTORE_NO_SPACE',
  'RESTORE_PENDING_JOB_OUTSIDE_ROOTS',
  'SCAN_ALREADY_PENDING',
  'SCAN_JOURNAL_UNAVAILABLE',
  'SETTING_LOCKED',
  'SOURCE_FILESYSTEM_REJECTED',
  'SOURCE_IDENTITY_CHANGED',
  'SOURCE_NOT_MOUNTED',
  'SOURCE_NOT_READY',
  'SOURCE_SENTINEL_MISSING',
  'WEB_AUTHORIZATION_REQUIRED',
  'WEB_LOGIN_RATE_LIMITED',
]

test('every server error category keeps its own code and a specific message', async () => {
  const server = createServer((request, response) => {
    const code = decodeURIComponent(request.url!.split('/').pop()!)
    response.writeHead(409, { 'Content-Type': 'application/json' })
    response.end(JSON.stringify({ error: { code, message: 'server text is never displayed' } }))
  }).listen(0, '127.0.0.1')
  await once(server, 'listening')
  try {
    const address = server.address()
    assert(address && typeof address !== 'string')
    const api = new API(`http://127.0.0.1:${address.port}`)
    const generic = await api.accountSSOStatus('UNLISTED_CATEGORY').catch((error: unknown) => error)
    assert(generic instanceof APIError && generic.code === 'REQUEST_FAILED')
    for (const code of serverErrorCodes) {
      const error = await api.accountSSOStatus(code).catch((error: unknown) => error)
      assert(error instanceof APIError, code)
      assert.equal(error.code, code)
      assert.notEqual(error.message, generic.message, code)
      assert.ok(!error.message.includes('server text'), code)
    }
  } finally {
    server.close()
  }
})
