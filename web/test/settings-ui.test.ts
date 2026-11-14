import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createServer } from 'vite'
import type { ViteDevServer } from 'vite'
import { parse } from 'lossless-json'

let server: ViteDevServer
before(async () => {
  server = await createServer({
    configFile: false,
    root: process.cwd(),
    server: { middlewareMode: true, hmr: false },
    appType: 'custom',
  })
})
after(async () => {
  await server.close()
})

const field = (name: string, kind: string, unit = '', label = name) => ({
  name,
  label,
  kind,
  unit,
  locked: false,
  available: true,
})

async function control(f: ReturnType<typeof field>, value: unknown) {
  const { SettingControl } = await server.ssrLoadModule('/src/features/settings/Settings.tsx')
  return renderToStaticMarkup(createElement(SettingControl, { field: f, value, disabled: false, change() {} }))
}

function selected(html: string) {
  return html.match(/<option[^>]*selected=""[^>]*>([^<]*)<\/option>/)?.[1]
}

test('millisecond intervals are edited as amount and unit, never raw milliseconds', async () => {
  const html = await control(field('maintenanceInterval', 'long', 'ms'), parse('604800000'))
  assert.ok(!html.includes('604800000'))
  assert.match(html, /value="7"/)
  assert.equal(selected(html), 'days')
  const month = await control(field('verifyBlocksInterval', 'long', 'ms'), parse('2678400000'))
  assert.match(month, /value="31"/)
  assert.equal(selected(month), 'days')
})

test('minute settings use the same duration editor', async () => {
  const html = await control(field('minutesUntilWarning', 'integer', 'minutes'), 4320)
  assert.match(html, /value="3"/)
  assert.equal(selected(html), 'days')
  const one = await control(field('backupStatusEmailFreqInMinutes', 'integer', 'minutes'), 60)
  assert.equal(selected(one), 'hour')
})

test('transfer rate 0 is an Unlimited switch, other rates show their unit', async () => {
  const unlimited = await control(field('highBandwidthRate', 'rate', 'KB/s'), parse('0'))
  assert.match(unlimited, /role="switch"[^>]*checked=""/)
  assert.ok(!unlimited.includes('inputMode'))
  assert.ok(!/value="0"/.test(unlimited))
  const limited = await control(field('highBandwidthRate', 'rate', 'KB/s'), parse('1024.0'))
  assert.match(limited, /value="1024"/)
  assert.match(limited, /KB\/s/)
  assert.ok(!/role="switch"[^>]*checked=""/.test(limited))
})

test('CPU limits show a percent sign, enums human labels, booleans a switch', async () => {
  assert.match(await control(field('idleThrottleRate', 'cpu', '%'), 80), /value="80"[\s\S]*%/)
  const delivery = await control(field('notifyDeliveryTime', 'notification'), 'MORNING')
  assert.equal(selected(delivery), 'In the morning')
  assert.ok(!delivery.includes('>MORNING<'))
  assert.equal(selected(await control(field('compression', 'compression'), 'AUTOMATIC')), 'Automatic')
  assert.match(await control(field('warningEmailEnabled', 'boolean'), true), /role="switch"[^>]*checked=""/)
})

test('equal values in different number forms are not a change', async () => {
  const { sameSetting } = await server.ssrLoadModule('/src/features/settings/Settings.tsx')
  assert.ok(sameSetting(field('a', 'long', 'ms'), '604800000', parse('604800000')))
  assert.ok(sameSetting(field('a', 'rate', 'KB/s'), 1024, parse('1024.0')))
  assert.ok(!sameSetting(field('a', 'rate', 'KB/s'), 0, parse('1024')))
  const window = { alwaysRun: false, startTimeOfDay: '01:00', endTimeOfDay: '02:00', days: ['MONDAY', 'FRIDAY'] }
  assert.ok(sameSetting(field('w', 'window'), window, { ...window, days: ['FRIDAY', 'MONDAY'] }))
})

test('settings page has tabs, no native session or tray, and no browser validation', async () => {
  const { Settings } = await server.ssrLoadModule('/src/features/settings/Settings.tsx')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(['settings', 'backup'], [
    { ...field('maintenanceInterval', 'long', 'ms', 'Archive maintenance interval'), value: parse('604800000') },
    { ...field('idleThrottleRate', 'cpu', '%', 'CPU limit while idle'), value: 80, locked: true },
  ])
  const html = renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(Settings)))
  for (const tab of ['Backup', 'Network', 'Notifications', 'General', 'Proxy'])
    assert.match(html, new RegExp(`<button[^>]*aria-pressed="[a-z]+"[^>]*>${tab}</button>`))
  assert.ok(!html.includes('Native session'))
  assert.ok(!html.includes('tray'))
  assert.ok(!html.includes('(ms)'))
  assert.ok(!html.includes('604800000'))
  assert.match(html, /<form[^>]*novalidate=""/i)
  assert.ok(!html.includes('required'))
  assert.match(html, /Managed by your organization/)
  assert.match(html, /<button[^>]*type="submit"[^>]*disabled=""[^>]*>Save<\/button>/)
  assert.match(html, /<button[^>]*disabled=""[^>]*>Revert<\/button>/)
  client.clear()
})

test('security forms use inline validation and destructive styling', async () => {
  const { Security } = await server.ssrLoadModule('/src/features/security/Security.tsx')
  for (const type of ['AccountPassword', 'CustomKey']) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
    client.setQueryData(['security'], { type })
    client.setQueryData(['devices'], [])
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(Security)))
    assert.ok(!html.includes('required'), type)
    assert.ok(!/minLength|minlength/.test(html), type)
    for (const form of html.match(/<form[^>]*>/g) ?? []) assert.match(form, /novalidate=""/i)
    if (type === 'AccountPassword') {
      assert.match(html, /class="btn btn-destructive"[^>]*>Upgrade protection</)
      assert.match(html, /<code>UPGRADE TO PrivatePassword<\/code>/)
    } else {
      assert.match(html, /class="btn btn-destructive"[^>]*>Replace custom key</)
      assert.match(html, /<code>REPLACE CUSTOM KEY<\/code>/)
      assert.match(html, /Download key…/)
    }
    client.clear()
  }
})
