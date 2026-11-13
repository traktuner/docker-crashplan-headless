import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
test('restore submission reset remains available after rebrowsing', async () => {
  const req = createRequire(import.meta.url)
  const { rolldown } = await import('rolldown')
  const jsx = req('react/jsx-runtime')
  let states = [],
    cursor = 0,
    mutationCursor = 0,
    mutations = []
  const events = []
  const mockReact = {
    useState(initial) {
      const i = cursor++
      if (!(i in states))
        states[i] = typeof initial === 'function' ? initial() : initial
      return [
        states[i],
        (v) => {
          states[i] = typeof v === 'function' ? v(states[i]) : v
        },
      ]
    },
    useRef(value) {
      return { current: value }
    },
  }
  const item = {
    fileId: 'AQAAAAAAAAAAAAAAAAAAAA==',
    path: '/data/file',
    timestamp: '100',
    modified: '100',
    size: '4',
    isFolder: false,
  }
  const queryData = {
    devices: [
      { id: '7', name: 'Fixture source', source: true, self: true },
      { id: '8', name: 'Fixture cloud', target: true },
    ],
    'archive-sets': [{ id: '1', name: 'Fixture' }],
    'restore-roots': [{ id: '0', path: '/restore', writable: true }],
    archive: { items: [item] },
    'restore-jobs': [],
    'restore-operations': [],
    'restore-helper': { state: 'waiting' },
  }
  const queryHooks = {
    useQueryClient() {
      return { invalidateQueries() {} }
    },
    useQuery(options) {
      return { data: queryData[options.queryKey[0]], isFetching: false }
    },
    useMutation(options) {
      const i = mutationCursor++
      let m = mutations[i]
      if (!m) {
        m = {
          isPending: false,
          mutate(v) {
            events.push({ mutation: i, value: v })
            m.data = { state: 'accepted' }
          },
          reset() {
            m.data = undefined
          },
        }
        mutations[i] = m
      }
      return m
    },
  }
  const modules = {
    react: mockReact,
    'react/jsx-runtime': jsx,
    '@tanstack/react-query': queryHooks,
    '@tanstack/react-virtual': {
      useVirtualizer({ count }) {
        return {
          getTotalSize() {
            return count * 56
          },
          getVirtualItems() {
            return Array.from({ length: count }, (_, index) => ({
              index,
              size: 56,
              start: 56 * index,
            }))
          },
        }
      },
    },
    'lucide-react': new Proxy(
      {},
      {
        get() {
          return function Icon() {}
        },
      },
    ),
    '../../shared/requestId': {
      newRequestId() {
        return 'a'.repeat(32)
      },
    },
    '../../shared/api': {
      APIError: class extends Error {},
      api: {},
      identifier: String,
      archiveSelection(item, set, at) {
        return {
          path: item.path,
          backupSetId: set,
          timestamp: at,
          isFolder: item.isFolder,
        }
      },
    },
  }
  const file = resolve('src/features/restore/Restore.tsx')
  const bundle = await rolldown({
    input: file,
    external: (id) => id !== file,
    transform: { jsx: { runtime: 'automatic' } },
  })
  const { output } = await bundle.generate({ format: 'cjs' })
  await bundle.close()
  const code = output[0].code
  let exportsObject = {}
  new Function('require', 'exports', code)((name) => {
    if (!(name in modules)) throw Error('unexpected ' + name)
    return modules[name]
  }, exportsObject)
  function walk(x, out = []) {
    if (Array.isArray(x)) {
      x.forEach((v) => walk(v, out))
      return out
    }
    if (x && typeof x === 'object' && x.props) {
      out.push(x)
      walk(x.props.children, out)
    }
    return out
  }
  function render() {
    cursor = 0
    mutationCursor = 0
    return walk(exportsObject.Restore())
  }
  function plain(x) {
    if (x == null || typeof x === 'boolean') return ''
    if (Array.isArray(x)) return x.map(plain).join('')
    return x.props ? plain(x.props.children) : String(x)
  }
  let nodes = render()
  let selects = nodes.filter((n) => n.type === 'select')
  ;['7', '8', '1'].forEach((value, index) =>
    selects[index].props.onChange({ target: { value } }),
  )
  nodes = render()
  nodes.find((n) => n.type === 'form').props.onSubmit({ preventDefault() {} })
  nodes = render()
  nodes
    .find(
      (n) =>
        n.type === 'input' && n.props['aria-label'] === 'Select ' + item.path,
    )
    .props.onChange()
  nodes = render()
  nodes
    .find((n) => n.type === 'select' && n.props.value === '')
    .props.onChange({ target: { value: '0' } })
  nodes = render()
  nodes
    .filter((n) => n.type === 'form')
    .at(-1)
    .props.onSubmit({ preventDefault() {} })
  nodes = render()
  assert.ok(
    nodes.some(
      (n) =>
        n.type === 'button' && plain(n).includes('Prepare another restore'),
    ),
  )
  nodes.find((n) => n.type === 'form').props.onSubmit({ preventDefault() {} })
  nodes = render()
  assert.equal(
    nodes.find(
      (n) =>
        n.type === 'input' && n.props['aria-label'] === 'Select ' + item.path,
    ).props.disabled,
    true,
  )
  const reset = nodes.find(
    (n) => n.type === 'button' && plain(n).includes('Prepare another restore'),
  )
  assert.ok(reset, 'submission reset disappeared after rebrowsing')
  const previous = globalThis.confirm
  globalThis.confirm = () => true
  try {
    reset.props.onClick()
  } finally {
    globalThis.confirm = previous
  }
  nodes = render()
  assert.equal(
    nodes.find(
      (n) =>
        n.type === 'input' && n.props['aria-label'] === 'Select ' + item.path,
    ).props.disabled,
    false,
  )
  assert.equal(events.length, 1, 'browsing or resetting replayed a restore')
})
