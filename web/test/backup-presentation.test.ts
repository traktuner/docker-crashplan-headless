import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createServer } from 'vite'
import type { ViteDevServer } from 'vite'
import { parse } from 'lossless-json'

let server: ViteDevServer
const load = (path: string) => server.ssrLoadModule(path)
before(async () => {
  server = await createServer({
    configFile: false,
    root: process.cwd(),
    server: { middlewareMode: true, hmr: false },
    appType: 'custom',
  })
})
after(() => server.close())

test('native intervals, windows and retention read as words', async () => {
  const p = await load('/src/features/backup/presentation.ts')
  assert.equal(p.every(604800000), 'every week')
  assert.equal(p.every(1209600000), 'every 2 weeks')
  assert.equal(p.every(900000), 'every 15 minutes')
  assert.equal(p.every(86400000), 'every day')
  assert.equal(p.fullScanSummary('604800000', '03:00'), 'Every week at 03:00')
  assert.equal(p.fullScanSummary(parse('86400000'), ''), 'Every day')

  const window = { alwaysRun: true, startTimeOfDay: '01:00', endTimeOfDay: '06:00', days: ['MONDAY'] }
  assert.equal(p.scheduleSummary(window), 'Always')
  assert.equal(
    p.scheduleSummary({ ...window, alwaysRun: false, days: ['MONDAY', 'TUESDAY', 'WEDNESDAY', 'THURSDAY', 'FRIDAY'] }),
    '01:00–06:00, weekdays',
  )
  assert.equal(p.daysSummary(['SUNDAY', 'SATURDAY']), 'weekends')
  assert.equal(p.daysSummary(['FRIDAY', 'MONDAY']), 'Mon, Fri')
  assert.equal(p.windowProblem({ ...window, alwaysRun: false, days: [] }), 'Choose at least one day.')
  assert.equal(p.windowProblem({ ...window, days: [] }), undefined)

  const field = (name: string, kind: string, unit: string, value: unknown) => ({
    name, kind, unit, value, label: name, locked: true, available: true,
  })
  for (const value of [-1, parse('-1'), '-1'])
    assert.equal(p.retentionValue(field('versionPrevYearsInterval', 'retention-interval', 'minutes', value)), 'Never')
  assert.equal(p.retentionValue(field('versionLastWeekInterval', 'integer', 'minutes', 15)), 'Every 15 minutes')
  assert.equal(p.retentionValue(field('backupFrequency', 'long', 'ms', '900000')), 'Every 15 minutes')
  assert.equal(p.retentionValue(field('keepDeletedMinutes', 'integer', 'minutes', 0)), 'Never')
  assert.equal(p.retentionValue(field('keepDeleted', 'boolean', '', true)), 'Yes')

  assert.deepEqual(p.splitBytes('1000000000'), { amount: '1', unit: 'GB' })
  assert.equal(p.joinBytes('2.5', 'MB'), '2500000')
  assert.equal(p.joinBytes('', 'MB'), undefined)
})

test('backup sets show names and human values, never raw units or native validation', async () => {
  const { BackupSets } = await load('/src/features/backup/BackupSets.tsx')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const setting = (name: string, kind: string, value: unknown, unit = '') => ({
    name, label: name, kind, unit, value, locked: false, available: true,
  })
  client.setQueryData(['set-details'], [
    {
      id: '1',
      locked: false,
      fields: [
        setting('name', 'text', 'Documents'),
        setting('scanInterval', 'long', '604800000', 'ms'),
        setting('scanTime', 'time', '03:00'),
        setting('backupRunWindow', 'window', {
          alwaysRun: true,
          startTimeOfDay: '01:00',
          endTimeOfDay: '06:00',
          days: ['MONDAY', 'TUESDAY'],
        }),
        setting('compression', 'compression', 'AUTOMATIC'),
      ],
      paths: [{ path: '/storage/docs', directory: true, selected: true }],
      excludes: [],
      destinations: [{ id: '4200', locked: false }],
      retention: [],
    },
  ])
  client.setQueryData(['devices'], [
    { id: '4200', name: 'Offsite archive', target: true, availableTarget: true, source: false, self: false, blocked: false, deleted: false },
  ])
  client.setQueryData(['scans'], [])
  const html = renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(BackupSets)))
  assert.ok(html.includes('Documents'))
  assert.ok(html.includes('Offsite archive'))
  assert.ok(html.includes('Every week at 03:00'))
  assert.ok(html.includes('>Always<'))
  assert.ok(html.includes('1 folder'))
  assert.ok(html.includes('Automatic'))
  // An always-on schedule must not also show its inactive time window.
  assert.ok(!html.includes('01:00'))
  assert.ok(!html.includes('604800000'))
  assert.ok(!html.includes('AUTOMATIC'))
  // The numeric ID is only a tooltip, never visible text.
  assert.ok(!/>[^<]*4200[^<]*</.test(html))
  assert.ok(!/\srequired(=|\s|>)/.test(html))
  for (const form of html.match(/<form\b[^>]*>/g) ?? []) assert.ok(/novalidate/i.test(form), form)
  client.clear()
})

test('the schedule shows time and day controls only for a limited window', async () => {
  const { WindowRows } = await load('/src/features/backup/controls.tsx')
  const render = (alwaysRun: boolean) =>
    renderToStaticMarkup(
      createElement(WindowRows, {
        value: { alwaysRun, startTimeOfDay: '01:00', endTimeOfDay: '06:00', days: [] },
        disabled: false,
        locked: false,
        change() {},
      }),
    )
  const always = render(true)
  assert.ok(always.includes('aria-pressed="true">Always<'))
  assert.ok(!always.includes('type="time"'))
  const limited = render(false)
  assert.equal((limited.match(/type="time"/g) ?? []).length, 2)
  assert.ok(limited.includes('Choose at least one day.'))
})

test('engine restart is a confirmed destructive action', async () => {
  const { System } = await load('/src/features/system/System.tsx')
  const client = new QueryClient()
  const html = renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(System)))
  assert.match(html, /<button[^>]*class="btn btn-destructive"[^>]*>Restart…<\/button>/)
})
