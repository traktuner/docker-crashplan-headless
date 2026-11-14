import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../shared/api'
import type {
  ArchivePassword,
  ArchiveUnlock,
  SecurityType,
  SecurityUpgrade,
} from '../../shared/api'
import {
  ErrorNotice,
  Loading,
  Notice,
  Page,
  Row,
  Section,
  Segmented,
} from '../../shared/components'
import {
  CustomKeyMaterial,
  FormField,
  ReplaceCustomKey,
  Success,
  downloadKeyBlob,
  hasMaterial,
} from './CustomKeys'
import './security.css'

const protection: Record<SecurityType, { label: string; detail: string }> = {
  AccountPassword: {
    label: 'Account password',
    detail:
      'Backups are encrypted with a key that your CrashPlan account password protects.',
  },
  PrivatePassword: {
    label: 'Archive password',
    detail:
      'Backups are encrypted with a key that a separate archive password protects. You need this password to restore.',
  },
  CustomKey: {
    label: 'Custom key',
    detail:
      "Backups are encrypted with your own key. CrashPlan's servers don't keep a copy, so you need it to restore.",
  },
}

function Unlock() {
  const client = useQueryClient()
  const devices = useQuery({
    queryKey: ['devices'],
    queryFn: () => api.devices(),
  })
  const [input, setInput] = useState<ArchiveUnlock>({
    sourceId: '',
    type: 'PrivatePassword',
    secret: '',
  })
  const [submitted, setSubmitted] = useState(false)
  const unlock = useMutation({
    mutationFn: () => api.unlockArchive(input),
    onSuccess: () => client.invalidateQueries({ queryKey: ['archive'] }),
    onSettled: () => {
      setSubmitted(false)
      setInput((old) => ({ ...old, secret: '', passphrase: undefined }))
    },
  })
  const [backupSecret, setBackupSecret] = useState('')
  const [backupSubmitted, setBackupSubmitted] = useState(false)
  const backup = useMutation({
    mutationFn: () => api.unlockBackup(backupSecret),
    onSettled: () => {
      setBackupSecret('')
      setBackupSubmitted(false)
      void client.invalidateQueries()
    },
  })

  const sourceMissing = !input.sourceId
  const secretMissing =
    input.type === 'CustomKey'
      ? !hasMaterial({ key: input.secret, passphrase: input.passphrase })
      : !input.secret
  const sources = devices.data?.filter((d) => d.source || d.self) ?? []

  return (
    <>
      <Section
        title="Unlock a backup for restore"
        description="Needed before you can restore from a backup that has an archive password or custom key. The unlock lasts until CrashPlan restarts."
        plain
      >
        <div className="group group-pad">
          <form
            noValidate
            className="security-form"
            onSubmit={(e) => {
              e.preventDefault()
              setSubmitted(true)
              if (sourceMissing || secretMissing) return
              unlock.mutate()
            }}
          >
            <ErrorNotice error={devices.error ?? unlock.error} />
            <FormField
              label="Computer"
              hint="The computer whose backup you want to restore."
              error={
                submitted && sourceMissing ? 'Choose a computer.' : undefined
              }
            >
              <select
                className="select"
                value={input.sourceId}
                onChange={(e) => {
                  unlock.reset()
                  setInput((old) => ({ ...old, sourceId: e.target.value }))
                }}
              >
                <option value="" disabled>
                  {devices.isPending ? 'Loading computers…' : 'Choose…'}
                </option>
                {sources.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name || d.id}
                  </option>
                ))}
              </select>
            </FormField>
            <FormField label="Protected with" group>
              <Segmented
                label="Protected with"
                value={input.type}
                options={[
                  { value: 'PrivatePassword', label: 'Archive password' },
                  { value: 'CustomKey', label: 'Custom key' },
                ]}
                onChange={(type) => {
                  if (unlock.isPending) return
                  unlock.reset()
                  setInput((old) => ({
                    ...old,
                    type,
                    secret: '',
                    passphrase: undefined,
                  }))
                }}
              />
            </FormField>
            {input.type === 'CustomKey' ? (
              <CustomKeyMaterial
                value={{ key: input.secret, passphrase: input.passphrase }}
                sourceId={input.sourceId}
                disabled={unlock.isPending}
                error={
                  submitted && secretMissing
                    ? 'Enter the key or passphrase.'
                    : undefined
                }
                change={(material) =>
                  setInput((old) => ({
                    ...old,
                    secret: material.key,
                    passphrase: material.passphrase,
                  }))
                }
              />
            ) : (
              <FormField
                label="Archive password"
                error={
                  submitted && secretMissing
                    ? 'Enter the archive password.'
                    : undefined
                }
              >
                <input
                  className="input"
                  type="password"
                  autoComplete="off"
                  value={input.secret ?? ''}
                  disabled={unlock.isPending}
                  onChange={(event) =>
                    setInput((old) => ({ ...old, secret: event.target.value }))
                  }
                />
              </FormField>
            )}
            <div className="button-row">
              <button
                type="submit"
                className="btn btn-primary"
                disabled={unlock.isPending}
              >
                Unlock
              </button>
              {unlock.isSuccess && (
                <Success>Unlocked until CrashPlan restarts.</Success>
              )}
            </div>
          </form>
        </div>
      </Section>
      <Section
        title="Unlock backups on this computer"
        description="Use this when CrashPlan asks for the archive password or custom key before it can continue backing up."
        plain
      >
        <div className="group group-pad">
          <form
            noValidate
            className="security-form"
            onSubmit={(e) => {
              e.preventDefault()
              setBackupSubmitted(true)
              if (!backupSecret) return
              backup.mutate()
            }}
          >
            <ErrorNotice error={backup.error} />
            <FormField
              label="Archive password or custom key"
              error={
                backupSubmitted && !backupSecret
                  ? 'Enter the archive password or custom key.'
                  : undefined
              }
            >
              <input
                className="input"
                type="password"
                autoComplete="off"
                value={backupSecret}
                disabled={backup.isPending}
                onChange={(e) => {
                  backup.reset()
                  setBackupSecret(e.target.value)
                }}
              />
            </FormField>
            <div className="button-row">
              <button
                type="submit"
                className="btn btn-primary"
                disabled={backup.isPending}
              >
                Unlock backups
              </button>
              {backup.isSuccess && <Success>CrashPlan accepted the unlock.</Success>}
            </div>
          </form>
        </div>
      </Section>
    </>
  )
}

const passwordActions: {
  value: ArchivePassword['action']
  label: string
  description: string
}[] = [
  {
    value: 'change',
    label: 'Change',
    description: 'Replace the archive password with a new one.',
  },
  {
    value: 'create',
    label: 'Create',
    description: 'Set an archive password, optionally with a recovery question.',
  },
  {
    value: 'reset',
    label: 'Forgot password',
    description:
      'Set a new archive password by answering your recovery question.',
  },
  {
    value: 'question',
    label: 'Recovery question',
    description: 'Change the question and answer used to reset the password.',
  },
]

function Password() {
  const client = useQueryClient()
  const [input, setInput] = useState<ArchivePassword>({ action: 'change' })
  const [submitted, setSubmitted] = useState(false)
  const change = useMutation({
    mutationFn: () => api.archivePassword(input),
    onSettled: () => {
      setSubmitted(false)
      setInput((old) => ({ action: old.action }))
      void client.invalidateQueries({ queryKey: ['security'] })
    },
  })
  const action = input.action
  const errors: Record<string, string | undefined> = {
    currentPassword:
      (action === 'change' || action === 'question') && !input.currentPassword
        ? 'Enter the current archive password.'
        : undefined,
    currentAnswer:
      action === 'reset' && !input.currentAnswer
        ? 'Enter the answer to your recovery question.'
        : undefined,
    password:
      action === 'question'
        ? undefined
        : !input.password
          ? 'Enter a new archive password.'
          : input.password.length < 8
            ? 'Use at least 8 characters.'
            : undefined,
    question:
      action === 'question' && !input.question
        ? 'Enter a recovery question.'
        : undefined,
    answer:
      (action === 'question' || (action === 'create' && !!input.question)) &&
      !input.answer
        ? 'Enter the answer to the recovery question.'
        : undefined,
  }
  const show = (name: string) => (submitted ? errors[name] : undefined)
  const set = (patch: Partial<ArchivePassword>) => {
    change.reset()
    setInput((old) => ({ ...old, ...patch }))
  }
  const selected = passwordActions.find((a) => a.value === action)
  return (
    <Section title="Archive password" plain>
      <div className="group group-pad">
        <form
          noValidate
          className="security-form"
          onSubmit={(e) => {
            e.preventDefault()
            setSubmitted(true)
            if (Object.values(errors).some(Boolean)) return
            change.mutate()
          }}
        >
          <FormField label="What do you want to do?" hint={selected?.description} group>
            <Segmented
              label="Archive password action"
              value={action}
              options={passwordActions}
              onChange={(next) => {
                if (change.isPending) return
                setSubmitted(false)
                setInput({ action: next })
                change.reset()
              }}
            />
          </FormField>
          <ErrorNotice error={change.error} />
          {(action === 'change' || action === 'question') && (
            <FormField
              label="Current archive password"
              error={show('currentPassword')}
            >
              <input
                className="input"
                type="password"
                autoComplete="off"
                value={input.currentPassword ?? ''}
                onChange={(e) => set({ currentPassword: e.target.value })}
              />
            </FormField>
          )}
          {action === 'reset' && (
            <FormField
              label="Answer to your recovery question"
              error={show('currentAnswer')}
            >
              <input
                className="input"
                type="password"
                autoComplete="off"
                value={input.currentAnswer ?? ''}
                onChange={(e) => set({ currentAnswer: e.target.value })}
              />
            </FormField>
          )}
          {action !== 'question' && (
            <FormField
              label="New archive password"
              hint="At least 8 characters."
              error={show('password')}
            >
              <input
                className="input"
                type="password"
                autoComplete="new-password"
                value={input.password ?? ''}
                onChange={(e) => set({ password: e.target.value })}
              />
            </FormField>
          )}
          {(action === 'create' || action === 'question') && (
            <>
              <FormField
                label="Recovery question"
                hint={
                  action === 'create'
                    ? 'Optional. Lets you reset the password if you forget it.'
                    : undefined
                }
                error={show('question')}
              >
                <input
                  className="input"
                  autoComplete="off"
                  value={input.question ?? ''}
                  onChange={(e) => set({ question: e.target.value })}
                />
              </FormField>
              <FormField label="Recovery answer" error={show('answer')}>
                <input
                  className="input"
                  type="password"
                  autoComplete="off"
                  value={input.answer ?? ''}
                  onChange={(e) => set({ answer: e.target.value })}
                />
              </FormField>
            </>
          )}
          <div className="button-row">
            <button
              type="submit"
              className="btn btn-primary"
              disabled={change.isPending}
            >
              {action === 'question' ? 'Save recovery question' : 'Save password'}
            </button>
            {change.isSuccess && <Success>CrashPlan accepted the change.</Success>}
          </div>
        </form>
      </div>
    </Section>
  )
}

function Upgrade({ current }: { current: SecurityType }) {
  const client = useQueryClient()
  const [input, setInput] = useState<SecurityUpgrade>({
    type: 'PrivatePassword',
    confirm: '',
  })
  const [submitted, setSubmitted] = useState(false)
  // An archive password can only be added on top of the account password.
  const type: SecurityUpgrade['type'] =
    current === 'AccountPassword' ? input.type : 'CustomKey'
  const phrase = 'UPGRADE TO ' + type
  const upgrade = useMutation({
    mutationFn: (payload: SecurityUpgrade) => api.upgradeSecurity(payload),
    onSettled: () => {
      setSubmitted(false)
      setInput((old) => ({ type: old.type, confirm: '' }))
      void client.invalidateQueries({ queryKey: ['security'] })
    },
  })
  const secretError =
    type === 'PrivatePassword'
      ? !input.password
        ? 'Enter a new archive password.'
        : input.password.length < 8
          ? 'Use at least 8 characters.'
          : undefined
      : !hasMaterial(input)
        ? 'Enter or generate the custom key.'
        : undefined
  const confirmed = input.confirm === phrase
  return (
    <Section
      title="Upgrade protection"
      description="Protect your backups with a secret that only you hold."
      plain
    >
      <div className="group group-pad">
        <form
          noValidate
          className="security-form"
          onSubmit={(e) => {
            e.preventDefault()
            setSubmitted(true)
            if (secretError || !confirmed) return
            upgrade.mutate({ ...input, type })
          }}
        >
          <Notice tone="warning">
            This can't be undone. Keep the archive password or custom key
            somewhere other than this computer: if you lose it, your backups
            can't be restored.
          </Notice>
          <ErrorNotice error={upgrade.error} />
          {current === 'AccountPassword' ? (
            <FormField label="New protection" group>
              <Segmented
                label="New protection"
                value={type}
                options={[
                  { value: 'PrivatePassword', label: 'Archive password' },
                  { value: 'CustomKey', label: 'Custom key' },
                ]}
                onChange={(next) => {
                  if (upgrade.isPending) return
                  setSubmitted(false)
                  upgrade.reset()
                  setInput({ type: next, confirm: '' })
                }}
              />
            </FormField>
          ) : (
            <FormField label="New protection" group>
              <span>Custom key</span>
            </FormField>
          )}
          {type === 'PrivatePassword' ? (
            <FormField
              label="New archive password"
              hint="At least 8 characters."
              error={submitted ? secretError : undefined}
            >
              <input
                className="input"
                type="password"
                autoComplete="new-password"
                value={input.password ?? ''}
                disabled={upgrade.isPending}
                onChange={(e) => {
                  upgrade.reset()
                  setInput((old) => ({ ...old, password: e.target.value }))
                }}
              />
            </FormField>
          ) : (
            <CustomKeyMaterial
              value={input}
              allowGenerate
              disabled={upgrade.isPending}
              error={submitted ? secretError : undefined}
              change={(material) => {
                upgrade.reset()
                setInput((old) => ({
                  ...old,
                  key: material.key,
                  passphrase: material.passphrase,
                  confirm: '',
                }))
              }}
            />
          )}
          <FormField
            label={
              <span className="security-confirm">
                To confirm, type <code>{phrase}</code>
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
              disabled={upgrade.isPending}
              onChange={(e) =>
                setInput((old) => ({ ...old, confirm: e.target.value }))
              }
            />
          </FormField>
          <div className="button-row">
            <button
              type="submit"
              className="btn btn-destructive"
              disabled={upgrade.isPending || !confirmed}
            >
              Upgrade protection
            </button>
            {upgrade.isSuccess && (
              <Success>CrashPlan confirmed the new protection.</Success>
            )}
          </div>
        </form>
      </div>
    </Section>
  )
}

export function Security() {
  const state = useQuery({
    queryKey: ['security'],
    queryFn: () => api.security(),
  })
  const exportKey = useMutation({
    mutationFn: () => api.exportArchiveKey(),
    onSuccess: downloadKeyBlob,
  })
  const type = state.data?.type
  const info = type ? protection[type] : undefined
  return (
    <Page
      title="Security"
      subtitle="How your backups are encrypted, and how to unlock them."
    >
      <ErrorNotice error={state.error} />
      <Section title="Encryption">
        {state.isPending ? (
          <div className="row">
            <Loading label="Checking protection…" />
          </div>
        ) : (
          <Row
            title="Protection"
            detail={info?.detail}
            value={info?.label ?? 'Unknown'}
          />
        )}
        {state.data?.question && (
          <Row
            title="Recovery question"
            value={
              <span className="security-question">{state.data.question}</span>
            }
          />
        )}
        {type === 'CustomKey' && (
          <Row
            title="Custom key"
            detail="Download a copy and keep it somewhere other than this computer."
          >
            <button
              type="button"
              className="btn btn-sm"
              disabled={exportKey.isPending}
              onClick={() => {
                if (
                  confirm(
                    'Download the current custom archive key? Keep the file private and store it outside this device.',
                  )
                )
                  exportKey.mutate()
              }}
            >
              Download key…
            </button>
          </Row>
        )}
      </Section>
      <ErrorNotice error={exportKey.error} />
      <Unlock />
      <Password />
      {type === 'CustomKey' && <ReplaceCustomKey />}
      {type && type !== 'CustomKey' && <Upgrade current={type} />}
    </Page>
  )
}
