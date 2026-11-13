import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createServer } from 'vite'

test('maintenance uses typed backup-set details and includes selected cloud destinations', async () => {
  const server = await createServer({
    configFile: false,
    root: process.cwd(),
    server: { middlewareMode: true },
    appType: 'custom',
  })
  try {
    const { DeviceManagement } = await server.ssrLoadModule(
      '/src/features/destinations/Devices.tsx',
    )
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(['local-archive-policy'], {
      allowed: true,
      ownDevices: true,
      selfId: '7',
      devices: [
        { id: '7', name: 'Self', own: true, self: true, connected: true },
        {
          id: '8',
          name: 'Cloud destination',
          own: false,
          self: false,
          connected: true,
        },
      ],
    })
    client.setQueryData(
      ['sets'],
      [{ id: '1', name: 'Default', destinations: ['8'] }],
    )
    client.setQueryData(
      ['set-details'],
      [{ id: '1', fields: [], destinations: [{ id: '8', locked: false }] }],
    )
    const html = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(DeviceManagement),
      ),
    )
    assert.equal((html.match(/Run archive maintenance/g) || []).length, 1)
    assert.ok(html.includes('Cloud destination'))
    client.setQueryData(['local-archive-policy'], {
      allowed: true,
      ownDevices: true,
      selfId: '7',
      devices: [
        { id: '7', name: 'Self', own: true, self: true, connected: true },
        {
          id: '8',
          name: 'Cloud destination',
          own: false,
          self: false,
          connected: false,
        },
      ],
    })
    const offline = renderToStaticMarkup(
      createElement(
        QueryClientProvider,
        { client },
        createElement(DeviceManagement),
      ),
    )
    assert.match(
      offline,
      /<button[^>]*disabled=""[^>]*>Run archive maintenance/,
    )
    assert.ok(
      offline.includes(
        'Connect this destination before requesting maintenance.',
      ),
    )
    client.clear()
  } finally {
    await server.close()
  }
})
