import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, APIError } from '../../shared/api'
import type {
  ArchivePassword,
  ArchiveUnlock,
  SecurityUpgrade,
} from '../../shared/api'

import {
  CustomKeyMaterial,
  ReplaceCustomKey,
  downloadKeyBlob,
} from './CustomKeys'

function Failure({ error }: { error: unknown }) {
  return error ? (
    <p className="notice error" role="alert">
      {error instanceof APIError
        ? error.message
        : 'The security action failed.'}
    </p>
  ) : null
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
  const unlock = useMutation({
    mutationFn: () => api.unlockArchive(input),
    onSuccess: () => client.invalidateQueries({ queryKey: ['archive'] }),
    onSettled: () =>
      setInput((old) => ({ ...old, secret: '', passphrase: undefined })),
  })
  const [backupSecret, setBackupSecret] = useState('')
  const backup = useMutation({
    mutationFn: () => api.unlockBackup(backupSecret),
    onSettled: () => {
      setBackupSecret('')
      void client.invalidateQueries()
    },
  })
  return (
    <>
      <section className="card form-card">
        <h2>Unlock an archive for restore</h2>
        <p className="muted">
          Archive keys stay in the service. This unlock lasts for the current
          CrashPlan session.
        </p>
        <Failure error={devices.error ?? unlock.error} />
        <form
          onSubmit={(e) => {
            e.preventDefault()
            unlock.mutate()
          }}
        >
          <label>
            Source device
            <select
              required
              value={input.sourceId}
              onChange={(e) =>
                setInput((old) => ({ ...old, sourceId: e.target.value }))
              }
            >
              <option value="">Choose a source</option>
              {devices.data
                ?.filter((d) => d.source || d.self)
                .map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name || d.id}
                  </option>
                ))}
            </select>
          </label>
          <label>
            Archive protection
            <select
              value={input.type}
              onChange={(e) =>
                setInput((old) => ({
                  ...old,
                  type: e.target.value as ArchiveUnlock['type'],
                  secret: '',
                  passphrase: undefined,
                }))
              }
            >
              <option value="PrivatePassword">Archive password</option>
              <option value="CustomKey">Custom archive key</option>
            </select>
          </label>
          {input.type === 'CustomKey' ? (
            <CustomKeyMaterial
              value={{ key: input.secret, passphrase: input.passphrase }}
              sourceId={input.sourceId}
              disabled={unlock.isPending}
              change={(material) =>
                setInput((old) => ({
                  ...old,
                  secret: material.key,
                  passphrase: material.passphrase,
                }))
              }
            />
          ) : (
            <label>
              Archive password
              <input
                required
                type="password"
                autoComplete="off"
                value={input.secret ?? ''}
                onChange={(event) =>
                  setInput((old) => ({ ...old, secret: event.target.value }))
                }
              />
            </label>
          )}
          <button disabled={unlock.isPending}>Unlock archive</button>
        </form>
        {unlock.isSuccess && (
          <p className="notice success">Archive unlocked for this session.</p>
        )}
      </section>
      <section className="card form-card">
        <h2>Unlock this device's backup engine</h2>
        <p className="muted">
          Use this when the existing account requires its archive password or
          custom key before backup can continue.
        </p>
        <Failure error={backup.error} />
        <form
          onSubmit={(e) => {
            e.preventDefault()
            backup.mutate()
          }}
        >
          <label>
            Existing archive password or custom key
            <input
              required
              type="password"
              autoComplete="off"
              value={backupSecret}
              onChange={(e) => setBackupSecret(e.target.value)}
            />
          </label>
          <button disabled={backup.isPending}>Unlock backup engine</button>
        </form>
        {backup.isSuccess && (
          <p className="notice success">CrashPlan accepted the unlock.</p>
        )}
      </section>
    </>
  )
}

function Password() {
  const client = useQueryClient()
  const [input, setInput] = useState<ArchivePassword>({ action: 'change' })
  const change = useMutation({
    mutationFn: () => api.archivePassword(input),
    onSettled: () => {
      setInput((old) => ({ action: old.action }))
      void client.invalidateQueries({ queryKey: ['security'] })
    },
  })
  return (
    <section className="card form-card">
      <h2>Archive password</h2>
      <Failure error={change.error} />
      <form
        onSubmit={(e) => {
          e.preventDefault()
          change.mutate()
        }}
      >
        <label>
          Action
          <select
            value={input.action}
            onChange={(e) => {
              setInput({ action: e.target.value as ArchivePassword['action'] })
              change.reset()
            }}
          >
            <option value="change">Change password</option>
            <option value="create">Create archive password</option>
            <option value="reset">Reset with recovery answer</option>
            <option value="question">Change recovery question</option>
          </select>
        </label>
        {(input.action === 'change' || input.action === 'question') && (
          <label>
            Current archive password
            <input
              required
              type="password"
              autoComplete="off"
              value={input.currentPassword ?? ''}
              onChange={(e) =>
                setInput((old) => ({ ...old, currentPassword: e.target.value }))
              }
            />
          </label>
        )}
        {input.action === 'reset' && (
          <label>
            Current recovery answer
            <input
              required
              type="password"
              autoComplete="off"
              value={input.currentAnswer ?? ''}
              onChange={(e) =>
                setInput((old) => ({ ...old, currentAnswer: e.target.value }))
              }
            />
          </label>
        )}
        {input.action !== 'question' && (
          <label>
            New archive password
            <input
              required
              minLength={8}
              type="password"
              autoComplete="new-password"
              value={input.password ?? ''}
              onChange={(e) =>
                setInput((old) => ({ ...old, password: e.target.value }))
              }
            />
          </label>
        )}
        {(input.action === 'create' || input.action === 'question') && (
          <>
            <label>
              Recovery question
              <input
                required={input.action === 'question'}
                value={input.question ?? ''}
                onChange={(e) =>
                  setInput((old) => ({ ...old, question: e.target.value }))
                }
              />
            </label>
            <label>
              Recovery answer
              <input
                required={input.action === 'question' || !!input.question}
                type="password"
                autoComplete="off"
                value={input.answer ?? ''}
                onChange={(e) =>
                  setInput((old) => ({ ...old, answer: e.target.value }))
                }
              />
            </label>
          </>
        )}
        <button disabled={change.isPending}>Apply password change</button>
      </form>
      {change.isSuccess && (
        <p className="notice success">
          CrashPlan accepted the password change.
        </p>
      )}
    </section>
  )
}

export function Security() {
  const client = useQueryClient()
  const state = useQuery({
    queryKey: ['security'],
    queryFn: () => api.security(),
  })
  const [input, setInput] = useState<SecurityUpgrade>({
    type: 'PrivatePassword',
    confirm: '',
  })
  const upgrade = useMutation({
    mutationFn: () => api.upgradeSecurity(input),
    onSettled: () => {
      setInput((old) => ({ type: old.type, confirm: '' }))
      void client.invalidateQueries({ queryKey: ['security'] })
    },
  })
  const exportKey = useMutation({
    mutationFn: () => api.exportArchiveKey(),
    onSuccess: downloadKeyBlob,
  })
  return (
    <>
      <header className="page-title">
        <h1>Archive security</h1>
        <p className="muted">
          Manage archive protection and unlock protected backups.
        </p>
      </header>
      <Failure error={state.error} />
      <section className="card">
        <h2>Current protection</h2>
        <p>{state.data?.type ?? 'Unavailable'}</p>
        <Failure error={exportKey.error} />
        {state.data?.type === 'CustomKey' && (
          <button
            className="secondary"
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
            Download current custom key
          </button>
        )}
        {state.data?.question && (
          <p>Recovery question: {state.data.question}</p>
        )}
      </section>
      <Unlock />
      <Password />
      {state.data?.type === 'CustomKey' && <ReplaceCustomKey />}
      {state.data && state.data.type !== 'CustomKey' && (
        <section className="card form-card">
          <h2>Upgrade archive protection</h2>
          <p className="notice">
            This change is irreversible. Keep the archive password or custom key
            outside this device. Losing it can make your backups unrecoverable.
          </p>
          <Failure error={upgrade.error} />
          <form
            onSubmit={(e) => {
              e.preventDefault()
              upgrade.mutate()
            }}
          >
            <label>
              Protection
              <select
                value={input.type}
                onChange={(e) =>
                  setInput({
                    type: e.target.value as SecurityUpgrade['type'],
                    confirm: '',
                  })
                }
              >
                <option
                  value="PrivatePassword"
                  disabled={state.data?.type !== 'AccountPassword'}
                >
                  Archive password
                </option>
                <option value="CustomKey">Custom archive key</option>
              </select>
            </label>
            {input.type === 'PrivatePassword' ? (
              <label>
                New archive password
                <input
                  required
                  type="password"
                  minLength={8}
                  autoComplete="new-password"
                  value={input.password ?? ''}
                  onChange={(e) =>
                    setInput((old) => ({ ...old, password: e.target.value }))
                  }
                />
              </label>
            ) : (
              <CustomKeyMaterial
                value={input}
                allowGenerate
                disabled={upgrade.isPending}
                change={(material) =>
                  setInput((old) => ({
                    ...old,
                    key: material.key,
                    passphrase: material.passphrase,
                    confirm: '',
                  }))
                }
              />
            )}
            <label>
              Type UPGRADE TO {input.type}
              <input
                required
                autoComplete="off"
                value={input.confirm}
                onChange={(e) =>
                  setInput((old) => ({ ...old, confirm: e.target.value }))
                }
              />
            </label>
            <button
              disabled={
                upgrade.isPending ||
                input.confirm !== 'UPGRADE TO ' + input.type
              }
            >
              Upgrade protection
            </button>
          </form>
          {upgrade.isSuccess && (
            <p className="notice success">
              The native engine confirmed the new protection type.
            </p>
          )}
        </section>
      )}
    </>
  )
}
