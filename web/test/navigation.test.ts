import { test } from 'node:test'
import assert from 'node:assert/strict'
import { findPages, pageFromHash, pages } from '../src/app/navigation.ts'
test('deep links resolve only supported pages', () => {
  for (const page of pages) assert.equal(pageFromHash('#' + page.id), page.id)
  for (const input of [
    '',
    '#undefined',
    '#restore/../account',
    '#restore<script>',
  ])
    assert.equal(pageFromHash(input), 'overview')
})
test('quick navigation searches page names and common tasks', () => {
  assert.ok(findPages('files').some((p) => p.id === 'restore'))
  assert.ok(findPages(' SCHEDULES ').some((p) => p.id === 'sets'))
  assert.equal(findPages('unknown destination sentence').length, 0)
  assert.equal(findPages('').length, pages.length)
})
