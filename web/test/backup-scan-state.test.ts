import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createServer } from 'vite'
import { parse } from 'lossless-json'

test('unresolved scan keeps Full scan disabled and acknowledgement available', async () => {
  const server = await createServer({
    configFile: false,
    root: process.cwd(),
    server: { middlewareMode: true, hmr: false },
    appType: 'custom',
  })
  try {
    const { BackupSets } = await server.ssrLoadModule('/src/features/backup/BackupSets.tsx')
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    client.setQueryData(['set-details'], [{ id: '7', fields: [], paths: [], destinations: [] }])
    for (const state of ['stalled', 'indeterminate', 'completed', 'acknowledged']) {
      client.setQueryData(['scans'], [{ id: 'scan', backupSetId: '7', state, updated: '2026-01-01T00:00:00Z' }])
      const html = renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(BackupSets)))
      const button = html.match(/<button\b[^>]*>(?:(?!<\/button>).)*Full scan<\/button>/s)?.[0]
      assert.ok(button)
      assert.equal(button.includes('disabled=""'), ['stalled', 'indeterminate'].includes(state), state)
      assert.equal(html.includes('Acknowledge</button>'), ['stalled', 'indeterminate'].includes(state), state)
    }
    client.clear()
  } finally {
    await server.close()
  }
})

test('native retention sentinel is presented as Never', async () => {
  const server = await createServer({ configFile: false, root: process.cwd(), server: { middlewareMode: true, hmr: false }, appType: 'custom' })
  try {
    const { SettingControl } = await server.ssrLoadModule('/src/features/settings/Settings.tsx')
    const field = { name: 'versionPrevYearsInterval', label: 'Previous years', kind: 'retention-interval', locked: true, available: true }
    for (const value of [-1, parse('-1')]) {
      const html = renderToStaticMarkup(createElement(SettingControl, { field, value, disabled: true, change() {} }))
      assert.match(html, /<output[^>]*>Never<\/output>/)
      assert.ok(!html.includes('>-1<'))
    }
  } finally {
    await server.close()
  }
})
