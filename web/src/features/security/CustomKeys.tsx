import { useState } from 'react'
import type { ReactNode } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2 } from 'lucide-react'
import { api, generateArchiveKey } from '../../shared/api'
import type { CustomKeyChange } from '../../shared/api'
import { ErrorNotice, Notice, Section, Segmented } from '../../shared/components'
import './security.css'

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

/** Labelled form field with an inline message instead of browser validation. */
export function FormField({
  label,
  hint,
  error,
  children,
  group,
}: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  children: ReactNode
  /** Render as a group (for segmented controls) instead of a <label>. */
  group?: boolean
}) {
  const body = (
    <>
      <span className="field-label">{label}</span>
      {children}
      {error ? (
        <span className="security-error">{error}</span>
      ) : (
        hint && <span className="field-hint">{hint}</span>
      )}
    </>
  )
  return group ? (
    <div className="field">{body}</div>
  ) : (
    <label className="field">{body}</label>
  )
}

export function Success({ children }: { children: ReactNode }) {
  return (
    <span className="security-success" role="status">
      <CheckCircle2 size={14} aria-hidden="true" />
      {children}
    </span>
  )
}

type Material = { key?: string; passphrase?: string }
export function hasMaterial(value: Material) {
  return !!(value.key?.trim() || value.passphrase)
}
export function CustomKeyMaterial({
  value,
  change,
  disabled = false,
  allowGenerate = false,
  sourceId,
  error,
}: {
  value: Material
  change: (value: Material) => void
  disabled?: boolean
  allowGenerate?: boolean
  sourceId?: string
  /** Inline message for the key or passphrase field. */
  error?: string
}) {
  const [mode, setMode] = useState<'key' | 'passphrase'>('key')
  const download = useMutation({
    mutationFn: () =>
      api.exportDerivedKey({ passphrase: value.passphrase ?? '', sourceId }),
    onSuccess: downloadKeyBlob,
  })
  return (
    <>
      <FormField label="Key source" group>
        <Segmented
          label="Key source"
          value={mode}
          options={[
            { value: 'key', label: 'Saved key' },
            { value: 'passphrase', label: 'Passphrase' },
          ]}
          onChange={(next) => {
            if (disabled || download.isPending || next === mode) return
            setMode(next)
            change(next === 'key' ? { key: '' } : { passphrase: '' })
            download.reset()
          }}
        />
      </FormField>
      {mode === 'passphrase' ? (
        <>
          <FormField
            label="Passphrase"
            error={error}
            hint="CrashPlan turns this passphrase into the key for this account. To restore later you need exactly the same passphrase."
          >
            <input
              className="input"
              type="password"
              autoComplete="off"
              value={value.passphrase ?? ''}
              disabled={disabled}
              onChange={(event) => change({ passphrase: event.target.value })}
            />
          </FormField>
          <div className="button-row">
            <button
              type="button"
              className="btn btn-sm"
              disabled={
                disabled ||
                download.isPending ||
                !value.passphrase ||
                sourceId === ''
              }
              onClick={() => download.mutate()}
            >
              Download the resulting key
            </button>
          </div>
          <ErrorNotice error={download.error} />
        </>
      ) : (
        <>
          <FormField
            label="Archive key"
            error={error}
            hint="The key text from your saved key file."
          >
            <input
              className="input mono"
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={value.key ?? ''}
              disabled={disabled}
              onChange={(event) => change({ key: event.target.value })}
            />
          </FormField>
          {allowGenerate && (
            <div className="button-row">
              <button
                type="button"
                className="btn btn-sm"
                disabled={disabled}
                onClick={() => change({ key: generateArchiveKey() })}
              >
                Generate a new key
              </button>
              <button
                type="button"
                className="btn btn-sm"
                disabled={disabled || !value.key}
                onClick={() =>
                  downloadKeyBlob(
                    new Blob([value.key! + '\n'], { type: 'text/plain' }),
                  )
                }
              >
                Download key to keep safe
              </button>
            </div>
          )}
        </>
      )}
    </>
  )
}

const replacePhrase = 'REPLACE CUSTOM KEY'

export function ReplaceCustomKey() {
  const client = useQueryClient()
  const [input, setInput] = useState<CustomKeyChange>({ confirm: '' })
  const [submitted, setSubmitted] = useState(false)
  const replace = useMutation({
    mutationFn: () => api.replaceCustomKey(input),
    onSettled: () => {
      setInput({ confirm: '' })
      setSubmitted(false)
      void client.invalidateQueries(
        { queryKey: ['security'] },
        { cancelRefetch: false },
      )
    },
  })
  const missingKey = !hasMaterial(input)
  const confirmed = input.confirm === replacePhrase
  return (
    <Section
      title="Replace custom key"
      description="Encrypt future backups with a different custom key."
      plain
    >
      <div className="group group-pad">
        <form
          noValidate
          className="security-form"
          onSubmit={(event) => {
            event.preventDefault()
            setSubmitted(true)
            if (missingKey || !confirmed) return
            replace.mutate()
          }}
        >
          <Notice tone="warning">
            Changing the key can make existing backup data unreadable. Keep the
            old key, and save the new key somewhere other than this computer
            before you continue.
          </Notice>
          <ErrorNotice error={replace.error} />
          <CustomKeyMaterial
            value={input}
            allowGenerate
            disabled={replace.isPending}
            error={
              submitted && missingKey ? 'Enter or generate the new key.' : undefined
            }
            change={(material) => {
              replace.reset()
              setInput({ ...material, confirm: '' })
            }}
          />
          <FormField
            label={
              <span className="security-confirm">
                To confirm, type <code>{replacePhrase}</code>
              </span>
            }
            error={
              submitted && !confirmed
                ? 'Type the phrase exactly as shown.'
                : undefined
            }
          >
            <input
              className="input"
              autoComplete="off"
              spellCheck={false}
              value={input.confirm}
              disabled={replace.isPending}
              onChange={(event) =>
                setInput((old) => ({ ...old, confirm: event.target.value }))
              }
            />
          </FormField>
          <div className="button-row">
            <button
              type="submit"
              className="btn btn-destructive"
              disabled={replace.isPending || !confirmed || missingKey}
            >
              Replace custom key
            </button>
            {replace.isSuccess && (
              <Success>CrashPlan now uses the new key.</Success>
            )}
          </div>
        </form>
      </div>
    </Section>
  )
}
