import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, generateArchiveKey } from '../../shared/api'
import type { CustomKeyChange } from '../../shared/api'
import { ErrorNotice } from '../../shared/components'

export function downloadKeyBlob(blob: Blob) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'crashplan-archive-key.txt'
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

type Material = { key?: string; passphrase?: string }
export function CustomKeyMaterial({
  value,
  change,
  disabled = false,
  allowGenerate = false,
  sourceId,
}: {
  value: Material
  change: (value: Material) => void
  disabled?: boolean
  allowGenerate?: boolean
  sourceId?: string
}) {
  const [mode, setMode] = useState<'key' | 'passphrase'>('key')
  const download = useMutation({
    mutationFn: () =>
      api.exportDerivedKey({ passphrase: value.passphrase ?? '', sourceId }),
    onSuccess: downloadKeyBlob,
  })
  return (
    <>
      <label>
        Custom key source
        <select
          value={mode}
          disabled={disabled || download.isPending}
          onChange={(event) => {
            const next = event.target.value as 'key' | 'passphrase'
            setMode(next)
            change(next === 'key' ? { key: '' } : { passphrase: '' })
            download.reset()
          }}
        >
          <option value="key">Saved Base64 key</option>
          <option value="passphrase">Derive from a passphrase</option>
        </select>
      </label>
      {mode === 'passphrase' ? (
        <>
          <label>
            Exact original or new passphrase
            <input
              type="password"
              required
              autoComplete="off"
              value={value.passphrase ?? ''}
              disabled={disabled}
              onChange={(event) => change({ passphrase: event.target.value })}
            />
          </label>
          <p className="muted">
            CrashPlan derives the key using this account and its native
            algorithm setting. Recovery needs the same context and exact
            passphrase.
          </p>
          <button
            type="button"
            className="secondary"
            disabled={
              disabled ||
              download.isPending ||
              !value.passphrase ||
              sourceId === ''
            }
            onClick={() => download.mutate()}
          >
            Download derived key privately
          </button>
          <ErrorNotice error={download.error} />
        </>
      ) : (
        <>
          <label>
            Saved Base64 archive key
            <input
              type="password"
              required
              autoComplete="off"
              value={value.key ?? ''}
              disabled={disabled}
              onChange={(event) => change({ key: event.target.value })}
            />
          </label>
          {allowGenerate && (
            <div className="restore-paging">
              <button
                type="button"
                className="secondary"
                disabled={disabled}
                onClick={() => change({ key: generateArchiveKey() })}
              >
                Generate a new key
              </button>
              <button
                type="button"
                className="secondary"
                disabled={disabled || !value.key}
                onClick={() =>
                  downloadKeyBlob(
                    new Blob([value.key! + '\n'], { type: 'text/plain' }),
                  )
                }
              >
                Download key to keep safely
              </button>
            </div>
          )}
        </>
      )}
    </>
  )
}

export function ReplaceCustomKey() {
  const client = useQueryClient()
  const [input, setInput] = useState<CustomKeyChange>({ confirm: '' })
  const replace = useMutation({
    mutationFn: () => api.replaceCustomKey(input),
    onSettled: () => {
      setInput({ confirm: '' })
      void client.invalidateQueries(
        { queryKey: ['security'] },
        { cancelRefetch: false },
      )
    },
  })
  return (
    <section className="card form-card">
      <h2>Replace the custom archive key</h2>
      <p className="notice">
        Changing the key can make existing backup data inaccessible. Keep the
        old key and save the new key outside this device before applying the
        change.
      </p>
      <ErrorNotice error={replace.error} />
      <form
        onSubmit={(event) => {
          event.preventDefault()
          replace.mutate()
        }}
      >
        <CustomKeyMaterial
          value={input}
          allowGenerate
          disabled={replace.isPending}
          change={(material) => setInput({ ...material, confirm: '' })}
        />
        <label>
          Type REPLACE CUSTOM KEY
          <input
            required
            autoComplete="off"
            value={input.confirm}
            disabled={replace.isPending}
            onChange={(event) =>
              setInput((old) => ({ ...old, confirm: event.target.value }))
            }
          />
        </label>
        <button
          disabled={
            replace.isPending ||
            input.confirm !== 'REPLACE CUSTOM KEY' ||
            !(input.key || input.passphrase)
          }
        >
          Replace custom key
        </button>
      </form>
      {replace.isSuccess && (
        <p className="notice success">
          CrashPlan's current key matches the requested replacement.
        </p>
      )}
    </section>
  )
}
