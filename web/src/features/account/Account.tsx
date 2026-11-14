import { DeviceManagement } from '../destinations/Devices'
import { useState, useEffect } from 'react'
import type { FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, ArrowRight } from 'lucide-react'
import { api, APIError } from '../../shared/api'
import type { LoginInfo, LoginResult } from '../../shared/api'
import { text, ErrorNotice, Loading, PageTitle } from '../../shared/components'
import { AccountDevices } from './AccountDevices'

export function Account({ firstSignIn = false }: { firstSignIn?: boolean }) {
  const client = useQueryClient()
  const setup = useQuery({
    queryKey: ['account-setup'],
    queryFn: () => api.accountSetup(),
  })
  const [info, setInfo] = useState<LoginInfo>({
    username: '',
    serverAddress: '',
  })
  const [password, setPassword] = useState('')
  const [loginType, setLoginType] = useState<'local' | 'cloud_sso'>()
  const [result, setResult] = useState<LoginResult>()
  const [sso, setSSO] = useState<{
    id: string
    providers: { name: string; url: string }[]
  }>()
  useEffect(() => {
    if (setup.data && !info.serverAddress)
      setInfo((old) => ({
        ...old,
        serverAddress: text(setup.data?.authorityFullAddress),
      }))
  }, [setup.data, info.serverAddress])
  const ssoState = useQuery({
    queryKey: ['sso', sso?.id],
    queryFn: () => api.accountSSOStatus(sso!.id),
    enabled: !!sso,
    refetchInterval: (query) =>
      query.state.data?.state === 'completed' || query.state.error
        ? false
        : 2_000,
  })
  useEffect(() => {
    if (ssoState.data?.result) {
      setResult(ssoState.data.result)
      void client.invalidateQueries({ queryKey: ['state'] })
    }
  }, [ssoState.data, client])
  // Like the desktop app, find the account's server from the email address;
  // the address field appears only when that is not possible or wanted.
  const [manualServer, setManualServer] = useState(false)
  const configuration = useMutation({
    mutationFn: async () => {
      let resolved = info
      if (!manualServer) {
        try {
          const found = await api.accountAuthority(info.username)
          resolved = { ...info, serverAddress: found.serverAddress }
        } catch (error) {
          const code = error instanceof APIError ? error.code : ''
          // A saved server still works when the lookup service is unreachable.
          if (!(code === 'NATIVE_AUTHORITY_UNAVAILABLE' && info.serverAddress)) {
            if (code === 'NATIVE_AUTHORITY_REQUIRED') setManualServer(true)
            throw error
          }
        }
        setInfo(resolved)
      }
      const config = await api.accountConfiguration(resolved)
      return { config, resolved }
    },
    onSuccess: ({ config, resolved }) => {
      setLoginType(config.loginType)
      // Browser sign-in (password and TOTP on the CrashPlan page) starts at once.
      if (config.loginType === 'cloud_sso') beginSSO.mutate(resolved)
    },
  })
  const login = useMutation({
    mutationFn: () => api.accountLogin(info, password),
    onSuccess: (data) => {
      setResult(data)
      void client.resetQueries()
    },
    onSettled: () => setPassword(''),
  })
  const beginSSO = useMutation({
    mutationFn: (value: LoginInfo) => api.accountSSO(value),
    onSuccess: setSSO,
  })
  const cancelSSO = useMutation({
    mutationFn: () => api.cancelSSO(sso!.id),
    onSuccess: () => setSSO(undefined),
  })
  const confirmSSO = useMutation({
    mutationFn: () => api.confirmSSO(sso!.id, ssoState.data!.assertionId!, ssoState.data!.username!),
    onSuccess: (data) => {
      setResult(data)
      void client.invalidateQueries({ queryKey: ['sso', sso?.id] })
      void client.resetQueries({ predicate: (query) => query.queryKey[0] !== 'sso' })
    },
  })
  const discardSSO = useMutation({
    mutationFn: () => api.discardSSO(sso!.id, ssoState.data!.assertionId!),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['sso', sso?.id] }) },
  })
  function field(name: keyof LoginInfo, value: string | boolean) {
    setInfo((old) => ({ ...old, [name]: value }))
    setLoginType(undefined)
    setResult(undefined)
  }
  function submit(e: FormEvent) {
    e.preventDefault()
    if (!loginType) configuration.mutate()
    else if (loginType === 'local') login.mutate()
    else beginSSO.mutate(info)
  }
  return (
    <>
      <PageTitle
        title="CrashPlan account"
        subtitle="Connect this device to your account."
      />
      {firstSignIn && (
        <div className="notice" role="status">
          <div>
            <p>
              <strong>Sign in to CrashPlan to continue.</strong>{' '}
              {setup.data?.authenticated
                ? 'CrashPlan is still signed in on this device and backups keep running. Enter your CrashPlan password once so this interface can manage it.'
                : 'This device has no active CrashPlan session yet, for example after the first start.'}
            </p>
            <p>
              Your settings, backup sets and device identity in <code>/config</code>{' '}
              are kept. Use the same CrashPlan account, so the device keeps its
              identity. The other pages open after sign-in.
            </p>
          </div>
        </div>
      )}
      <section className="card form-card">
        <ErrorNotice error={setup.error} />
        {setup.isPending ? (
          <Loading />
        ) : (
          <form onSubmit={submit}>
            <label>
              Email or username
              <input
                autoComplete="username"
                value={info.username}
                onChange={(e) => field('username', e.target.value)}
                required
              />
            </label>
            {manualServer ? (
              <label>
                Server address
                <input
                  value={info.serverAddress}
                  onChange={(e) => field('serverAddress', e.target.value)}
                  required
                  spellCheck={false}
                />
              </label>
            ) : (
              <button
                type="button"
                className="text-button"
                onClick={() => setManualServer(true)}
              >
                Use a specific server address
              </button>
            )}
            <label className="checkbox">
              <input
                type="checkbox"
                checked={!!info.register}
                onChange={(e) => field('register', e.target.checked)}
              />{' '}
              Create a new CrashPlan account
            </label>
            {info.register && (
              <>
                <div className="columns">
                  <label>
                    First name
                    <input
                      value={info.firstName ?? ''}
                      onChange={(e) => field('firstName', e.target.value)}
                      required
                    />
                  </label>
                  <label>
                    Last name
                    <input
                      value={info.lastName ?? ''}
                      onChange={(e) => field('lastName', e.target.value)}
                      required
                    />
                  </label>
                </div>
                <label>
                  Registration key
                  <input
                    type="password"
                    value={info.registrationKey ?? ''}
                    onChange={(e) => field('registrationKey', e.target.value)}
                    autoComplete="off"
                  />
                </label>
              </>
            )}
            {loginType === 'local' && (
              <label>
                CrashPlan password
                <input
                  type="password"
                  autoComplete={
                    info.register ? 'new-password' : 'current-password'
                  }
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </label>
            )}
            <ErrorNotice
              error={
                configuration.error ??
                login.error ??
                beginSSO.error ??
                ssoState.error ??
                cancelSSO.error ??
                confirmSSO.error ??
                discardSSO.error
              }
            />
            {result?.complete && (
              <div className="notice success">
                <Check size={18} /> Signed in to CrashPlan.
              </div>
            )}
            {result && !result.complete && (
              <div className="notice">
                CrashPlan requires a device migration selection. Setup is not
                complete.
              </div>
            )}
            {!sso && (
              <button
                disabled={
                  configuration.isPending ||
                  login.isPending ||
                  beginSSO.isPending
                }
              >
                {!loginType
                  ? 'Continue'
                  : loginType === 'local'
                    ? 'Sign in to CrashPlan'
                    : 'Continue in browser'}
                <ArrowRight size={16} />
              </button>
            )}
            {sso && (
              <div className="sso">
                <p>
                  Open the sign-in page and sign in with your password and
                  verification code. When it says you can close the window,
                  return here and confirm the account. Keep this page open.
                </p>
                {sso.providers.map((p) => (
                  <a
                    className="button"
                    key={p.name}
                    href={p.url}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    {p.name}
                    <ArrowRight size={16} />
                  </a>
                ))}
                {ssoState.data?.state === 'confirm' && (
                  <div className="notice">
                    <p>The sign-in provider returned <strong>{ssoState.data.username}</strong>.</p>
                    {ssoState.data.differs && <p>This differs from the username entered above.</p>}
                    <p>Confirm this account to complete sign-in. A provider response can belong to an earlier browser attempt.</p>
                    <button type="button" disabled={confirmSSO.isPending || discardSSO.isPending} onClick={() => confirmSSO.mutate()}>
                      Sign in as {ssoState.data.username}
                    </button>
                    <button type="button" className="secondary" disabled={confirmSSO.isPending || discardSSO.isPending} onClick={() => discardSSO.mutate()}>
                      Discard this response
                    </button>
                  </div>
                )}
                <button
                  className="secondary"
                  type="button"
                  onClick={() => cancelSSO.mutate()}
                >
                  Cancel sign-in
                </button>
              </div>
            )}
          </form>
        )}
      </section>
      {!firstSignIn && (
        <>
          <AccountDevices />
          <DeviceManagement />
        </>
      )}
    </>
  )
}
