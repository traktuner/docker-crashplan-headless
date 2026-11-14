import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import type { ComponentType } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createServer } from 'vite'

async function withModules(
  run: (load: (path: string) => Promise<Record<string, unknown>>) => Promise<void>,
) {
  const server = await createServer({
    configFile: false,
    root: process.cwd(),
    server: { middlewareMode: true },
    appType: 'custom',
    logLevel: 'silent',
  })
  try {
    await run((path) => server.ssrLoadModule(path))
  } finally {
    await server.close()
  }
}
function render(
  client: QueryClient,
  component: unknown,
  props: Record<string, unknown> = {},
) {
  return renderToStaticMarkup(
    createElement(
      QueryClientProvider,
      { client },
      createElement(component as ComponentType<Record<string, unknown>>, props),
    ),
  )
}
const newClient = () =>
  new QueryClient({ defaultOptions: { queries: { retry: false } } })

test('a signed-in account shows its summary instead of the sign-in form', async () => {
  await withModules(async (load) => {
    const { Account } = await load('/src/features/account/Account.tsx')
    const client = newClient()
    client.setQueryData(['account-setup'], {
      authenticated: true,
      username: 'user@example.com',
      firstName: 'Example',
      lastName: 'User',
      authorityFullAddress: 'https://console.example.com:443',
      osHostname: 'host-name',
    })
    client.setQueryData(['devices'], [
      { id: '7', name: 'Backup box', self: true, source: true, target: false, availableTarget: false, blocked: false, deleted: false },
    ])
    const html = render(client, Account)
    assert.match(html, /Signed in/)
    assert.ok(html.includes('Example User'))
    assert.ok(html.includes('user@example.com'))
    assert.ok(html.includes('https://console.example.com:443'))
    assert.ok(html.includes('Backup box'))
    assert.ok(html.includes('Sign in again…'))
    assert.ok(!html.includes('Continue'))
    assert.ok(!html.includes('Create a new'))
    assert.ok(!html.includes('<form'))
    assert.ok(!html.includes('required'))
  })
})

test('without a session the form explains the browser sign-in and offers registration separately', async () => {
  await withModules(async (load) => {
    const { Account } = await load('/src/features/account/Account.tsx')
    const client = newClient()
    client.setQueryData(['account-setup'], {
      authenticated: false,
      authorityFullAddress: '',
    })
    const html = render(client, Account, { firstSignIn: true })
    assert.match(html, /<form[^>]*novalidate/i)
    assert.ok(!html.includes('required'))
    assert.match(html, /<button[^>]*btn-primary[^>]*>Sign in with CrashPlan<\/button>/)
    assert.ok(html.includes('new browser tab'))
    assert.ok(html.includes('Create a new account instead'))
    assert.ok(!html.includes('type="checkbox"'))
    // Nothing is prefilled.
    assert.match(html, /autoComplete="username"|autocomplete="username"/)
    assert.match(html, /<input[^>]*value=""[^>]*>/)
    assert.ok(!html.includes('Signed in'))
  })
})

test('an engine that is still signed in fixes the account and hides registration', async () => {
  await withModules(async (load) => {
    const { Account } = await load('/src/features/account/Account.tsx')
    const client = newClient()
    client.setQueryData(['account-setup'], {
      authenticated: true,
      username: 'user@example.com',
      authorityFullAddress: 'https://console.example.com:443',
    })
    const html = render(client, Account, { firstSignIn: true })
    assert.match(html, /<input[^>]*readonly=""[^>]*value="user@example.com"/i)
    assert.ok(!html.includes('Create a new account'))
    assert.ok(html.includes('Sign in with CrashPlan'))
  })
})

test('device names use inline rename, confirmed maintenance and tell same-named destinations apart', async () => {
  await withModules(async (load) => {
    const { DeviceNames } = await load('/src/features/account/AccountDevices.tsx')
    const client = newClient()
    const policy = (connected: boolean) => ({
      allowed: true,
      ownDevices: true,
      selfId: '7',
      devices: [
        { id: '7', name: 'Self', own: true, self: true, connected: true },
        { id: '9', name: 'Mac', own: true, self: false, connected: true },
        { id: '8', name: 'Cloud', own: false, self: false, connected },
        { id: '10', name: 'Cloud', own: false, self: false, connected: true },
      ],
    })
    client.setQueryData(['local-archive-policy'], policy(true))
    client.setQueryData(['set-details'], [
      { id: '1', fields: [], destinations: [{ id: '8', locked: false }, { id: '10', locked: false }] },
    ])
    client.setQueryData(['devices'], [
      { id: '8', name: 'Cloud', self: false, source: false, target: true, availableTarget: true, blocked: false, deleted: false },
      { id: '10', name: 'Cloud', self: false, source: false, target: false, availableTarget: false, blocked: false, deleted: false },
    ])
    const html = render(client, DeviceNames)
    assert.equal((html.match(/Run archive maintenance…/g) || []).length, 2)
    assert.equal((html.match(/btn btn-plain btn-sm"[^>]*>Rename</g) || []).length, 2)
    assert.ok(html.includes('This device'))
    assert.ok(html.includes('Other device in your account'))
    assert.ok(html.includes('Backup destination'))
    assert.ok(html.includes('Not in use'))
    assert.ok(html.includes('Used by a backup set'))
    assert.ok(html.includes('a Mac'))
    assert.ok(!html.includes('btn-primary'))
    assert.ok(!html.includes('required'))

    client.setQueryData(['local-archive-policy'], policy(false))
    const offline = render(client, DeviceNames)
    assert.match(offline, /<button[^>]*disabled=""[^>]*>Run archive maintenance…/)
    assert.ok(offline.includes('Connect this destination before requesting maintenance.'))
  })
})

test('the replacement workflow stays hidden when nothing is unfinished', async () => {
  await withModules(async (load) => {
    const { ReplacementWorkflow } = await load('/src/features/account/ReplacementWorkflow.tsx')
    const client = newClient()
    client.setQueryData(['replacement'], {
      available: false,
      inProgress: false,
      deviceSelectedGuid: '',
      screenState: '',
    })
    assert.equal(render(client, ReplacementWorkflow), '')
    client.setQueryData(['replacement'], {
      available: true,
      inProgress: false,
      deviceSelectedGuid: '42',
      screenState: '',
    })
    const html = render(client, ReplacementWorkflow)
    assert.ok(html.includes('FINISH REPLACEMENT'))
    assert.match(html, /<form[^>]*novalidate/i)
    assert.ok(!html.includes('required'))
  })
})
