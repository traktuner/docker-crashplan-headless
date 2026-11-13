import test from 'node:test'
import assert from 'node:assert/strict'
import { archiveSearchQuery } from '../src/shared/api.ts'

test('V3 search uses the selected archive root and preserves filters', () => {
  const query = { sourceId: '7', targetId: '8', backupSetId: '1', fileId: 'AQAAAAAAAAAAAAAAAAAAAA==', at: '123', includeDeleted: true, limit: 200, pattern: '*.pdf', fullPath: true }
  const scoped = archiveSearchQuery(query, 'ARCHIVE_V3')
  assert.equal(scoped.fileId, 'AAAAAAAAAAAAAAAAAAAAAA==')
  assert.equal(scoped.at, '123')
  assert.equal(scoped.backupSetId, '1')
  assert.equal(scoped.includeDeleted, true)
  assert.equal(scoped.pattern, '*.pdf')
  assert.equal(scoped.fullPath, true)
  assert.equal(query.fileId, 'AQAAAAAAAAAAAAAAAAAAAA==')
  assert.equal(archiveSearchQuery(query, 'ARCHIVE_V1').fileId, query.fileId)
})
