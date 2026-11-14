import { useState, useEffect, useRef } from 'react'
import type { FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, UserRound } from 'lucide-react'
import { api, APIError } from '../../shared/api'
import type { LoginInfo, LoginResult } from '../../shared/api'
import {
  text,
  Badge,
  ErrorNotice,
  Field,
  Loading,
  Notice,
  Page,
  Row,
  Section,
  Spinner,
} from '../../shared/components'
import { dateTime, words } from '../../shared/format'
import { AccountDevices, DeviceNames } from './AccountDevices'
import './account.css'

type Provider = { name: string; url: string }

// The sign-in page is CrashPlan's own; open it in a new tab when the browser
// still allows it. Without permission the waiting view offers the link.
function openSignInPage(providers: Provider[]): boolean {
  const url = providers.length === 1 ? providers[0]?.url : undefined
  if (!url || typeof window === 'undefined') return false
  if (!/^https?:\/\//i.test(url)) return false
  const tab = window.open(url, '_blank')
  if (!tab) return false
  tab.opener = null
  return true
}

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
    providers: Provider[]
  }>()
  const [opened, setOpened] = useState(false)
  // Validation messages appear only after a submit attempt.
  const [tried, setTried] = useState(false)
  // A signed-in account shows its summary; the form opens on request.
  const [reauth, setReauth] = useState(false)
  // An engine that is still signed in knows its account and server, as in the
  // desktop app: the username is fixed and no server lookup is needed.
  const knownAccount =
    setup.data?.authenticated === true ? text(setup.data?.username) : ''
  const knownServer = text(setup.data?.authorityFullAddress)
  const signedIn = !firstSignIn && setup.data?.authenticated === true
  const showForm = !signedIn || reauth || !!sso
  useEffect(() => {
    if (!setup.data) return
    setInfo((old) => ({
      ...old,
      serverAddress: old.serverAddress || knownServer,
      username: knownAccount || old.username,
    }))
  }, [setup.data, knownAccount, knownServer])
  const devices = useQuery({
    queryKey: ['devices'],
    queryFn: () => api.devices(),
    enabled: signedIn,
  })
  // Newer backends report the full signed-in summary; older ones do not, and
  // the page then falls back to the sign-in setup values.
  const summary = useQuery({
    queryKey: ['account-summary'],
    queryFn: () => api.account(),
    enabled: signedIn,
    retry: false,
  })
  // Password sign-in only for accounts without verification code; the
  // desktop app always uses the browser.
  const [usePassword, setUsePassword] = useState(false)
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
      if (!manualServer && !(knownServer && info.serverAddress === knownServer)) {
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
      // Browser sign-in (password and TOTP on the CrashPlan page) starts at
      // once for every login type, as in the desktop app.
      if (!(usePassword && config.loginType === 'local'))
        beginSSO.mutate(resolved)
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
    onSuccess: (data) => {
      setSSO(data)
      setOpened(openSignInPage(data.providers))
    },
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
  // Complete by itself when CrashPlan returns the entered account, as the
  // desktop app does; a different account still needs explicit confirmation.
  const confirmed = useRef('')
  useEffect(() => {
    const state = ssoState.data
    if (
      state?.state === 'confirm' &&
      !state.differs &&
      state.assertionId &&
      confirmed.current !== state.assertionId
    ) {
      confirmed.current = state.assertionId
      confirmSSO.mutate()
    }
  }, [ssoState.data, confirmSSO])
  // A completed sign-in returns to the account summary.
  useEffect(() => {
    if (result?.complete) setReauth(false)
  }, [result])
  function field(name: keyof LoginInfo, value: string | boolean) {
    setInfo((old) => ({ ...old, [name]: value }))
    setLoginType(undefined)
    setResult(undefined)
  }
  const passwordMode = loginType === 'local' && usePassword
  const problems = {
    username: !info.username.trim()
      ? info.register
        ? 'Enter the email address for the new account.'
        : 'Enter your CrashPlan email address or user name.'
      : '',
    serverAddress:
      manualServer && !info.serverAddress.trim()
        ? 'Enter the address of your CrashPlan server.'
        : '',
    firstName:
      info.register && !info.firstName?.trim() ? 'Enter a first name.' : '',
    lastName:
      info.register && !info.lastName?.trim() ? 'Enter a last name.' : '',
    password: passwordMode && !password ? 'Enter your password.' : '',
  }
  const shown = (key: keyof typeof problems) =>
    tried && problems[key] ? problems[key] : ''
  function submit(e: FormEvent) {
    e.preventDefault()
    setTried(true)
    if (Object.values(problems).some(Boolean)) return
    setTried(false)
    if (!loginType) configuration.mutate()
    else if (loginType === 'local' && usePassword) login.mutate()
    else beginSSO.mutate(info)
  }
  const busy = configuration.isPending || login.isPending || beginSSO.isPending
  const accountName =
    [text(setup.data?.firstName), text(setup.data?.lastName)]
      .filter(Boolean)
      .join(' ') || knownAccount
  const deviceName =
    summary.data?.deviceName ||
    devices.data?.find((d) => d.self)?.name ||
    text(setup.data?.osHostname)
  const server = summary.data?.server || knownServer
  const email = summary.data?.username || knownAccount
  const version = summary.data?.productVersion
  const license = summary.data?.licenseType
    ? words(summary.data.licenseType) +
      (Number(summary.data.licenseExpires) > 0 ? ` · until ${dateTime(summary.data.licenseExpires)}` : '')
    : undefined
  const error =
    configuration.error ??
    login.error ??
    beginSSO.error ??
    ssoState.error ??
    cancelSSO.error ??
    confirmSSO.error ??
    discardSSO.error
  const differs = ssoState.data?.state === 'confirm' && ssoState.data.differs

  const waiting = sso && (
    <div className="account-form" aria-live="polite">
      {differs ? (
        <Notice tone="warning">
          <p>
            The sign-in page returned the account{' '}
            <strong>{ssoState.data?.username}</strong>, which differs from the
            one entered here.
          </p>
          <p>
            Confirm this account to finish signing in. The response can also
            belong to an earlier sign-in attempt in your browser.
          </p>
          <div className="button-row" style={{ marginTop: 8 }}>
            <button
              type="button"
              className="btn btn-primary btn-sm"
              disabled={confirmSSO.isPending || discardSSO.isPending}
              onClick={() => confirmSSO.mutate()}
            >
              Sign in as {ssoState.data?.username}
            </button>
            <button
              type="button"
              className="btn btn-sm"
              disabled={confirmSSO.isPending || discardSSO.isPending}
              onClick={() => discardSSO.mutate()}
            >
              Discard this response
            </button>
          </div>
        </Notice>
      ) : (
        <>
          <div className="account-waiting" role="status">
            <Spinner />
            {confirmSSO.isPending
              ? 'Completing sign-in…'
              : 'Waiting for you to finish signing in…'}
          </div>
          <p className="account-lead">
            Enter your password and verification code on the CrashPlan page in
            the other browser tab. When it says you can close the window, come
            back here. Keep this page open; it continues by itself.
          </p>
        </>
      )}
      <div className="button-row">
        {sso.providers.map((p) => (
          <a
            className={'btn btn-sm' + (opened || differs ? '' : ' btn-primary')}
            key={p.name}
            href={p.url}
            target="_blank"
            rel="noopener noreferrer"
            onClick={() => setOpened(true)}
          >
            {sso.providers.length > 1
              ? p.name
              : opened
                ? 'Open sign-in page again'
                : 'Open CrashPlan sign-in page'}
            <ExternalLink size={13} aria-hidden="true" />
          </a>
        ))}
        <button
          type="button"
          className="btn btn-plain btn-sm"
          disabled={cancelSSO.isPending}
          onClick={() => cancelSSO.mutate()}
        >
          Cancel sign-in
        </button>
      </div>
      <ErrorNotice error={error} />
    </div>
  )

  const form = !sso && (
    <form className="account-form" noValidate onSubmit={submit}>
      <p className="account-lead">
        {passwordMode
          ? 'Enter the password of your CrashPlan account. This works only for accounts without a verification code.'
          : info.register
            ? 'CrashPlan opens its registration in a new browser tab. This page continues by itself when you are done.'
            : 'A CrashPlan sign-in page opens in a new browser tab, where you enter your password and verification code. This page completes by itself.'}
      </p>
      <Field
        label={info.register ? 'Email address' : 'Email or user name'}
        hint={
          knownAccount
            ? 'CrashPlan on this device is linked to this account.'
            : undefined
        }
      >
        <input
          className="input"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          placeholder={knownAccount ? undefined : 'user@example.com'}
          value={knownAccount || info.username}
          readOnly={!!knownAccount}
          aria-invalid={!!shown('username')}
          onChange={(e) => field('username', e.target.value)}
        />
        {shown('username') && (
          <span className="field-error" role="alert">{shown('username')}</span>
        )}
      </Field>
      {manualServer && (
        <Field
          label="Server address"
          hint="Only needed when CrashPlan cannot find your server from the email address."
        >
          <input
            className="input"
            value={info.serverAddress}
            aria-invalid={!!shown('serverAddress')}
            onChange={(e) => field('serverAddress', e.target.value)}
            spellCheck={false}
            autoCapitalize="none"
          />
          {shown('serverAddress') && (
            <span className="field-error" role="alert">{shown('serverAddress')}</span>
          )}
        </Field>
      )}
      {info.register && (
        <>
          <div className="account-two">
            <Field label="First name">
              <input
                className="input"
                autoComplete="given-name"
                value={info.firstName ?? ''}
                aria-invalid={!!shown('firstName')}
                onChange={(e) => field('firstName', e.target.value)}
              />
              {shown('firstName') && (
                <span className="field-error" role="alert">{shown('firstName')}</span>
              )}
            </Field>
            <Field label="Last name">
              <input
                className="input"
                autoComplete="family-name"
                value={info.lastName ?? ''}
                aria-invalid={!!shown('lastName')}
                onChange={(e) => field('lastName', e.target.value)}
              />
              {shown('lastName') && (
                <span className="field-error" role="alert">{shown('lastName')}</span>
              )}
            </Field>
          </div>
          <Field
            label="Registration key"
            hint="Optional. Only if your organization gave you one."
          >
            <input
              className="input"
              type="password"
              value={info.registrationKey ?? ''}
              onChange={(e) => field('registrationKey', e.target.value)}
              autoComplete="off"
            />
          </Field>
        </>
      )}
      {passwordMode && (
        <Field label="CrashPlan password">
          <input
            className="input"
            type="password"
            autoComplete={info.register ? 'new-password' : 'current-password'}
            value={password}
            aria-invalid={!!shown('password')}
            onChange={(e) => setPassword(e.target.value)}
          />
          {shown('password') && (
            <span className="field-error" role="alert">{shown('password')}</span>
          )}
        </Field>
      )}
      <ErrorNotice error={error} />
      <div className="button-row">
        <button className="btn btn-primary" disabled={busy}>
          {busy && <Spinner size={14} />}
          {login.isPending
            ? 'Signing in…'
            : configuration.isPending || beginSSO.isPending
              ? 'Opening sign-in…'
              : passwordMode
                ? info.register
                  ? 'Create account'
                  : 'Sign in'
                : info.register
                  ? 'Create account with CrashPlan'
                  : 'Sign in with CrashPlan'}
        </button>
        {signedIn && (
          <button
            type="button"
            className="btn"
            disabled={busy}
            onClick={() => {
              setReauth(false)
              setTried(false)
            }}
          >
            Cancel
          </button>
        )}
      </div>
      <div className="account-links">
        {loginType === 'local' && (
          <button
            type="button"
            className="btn btn-plain btn-sm"
            onClick={() => {
              setUsePassword(!usePassword)
              setTried(false)
            }}
          >
            {usePassword
              ? 'Sign in through the browser instead'
              : 'Sign in with a password instead'}
          </button>
        )}
        {!manualServer && (
          <button
            type="button"
            className="btn btn-plain btn-sm"
            onClick={() => setManualServer(true)}
          >
            Use a specific server address
          </button>
        )}
        {!signedIn && !knownAccount && (
          <button
            type="button"
            className="btn btn-plain btn-sm"
            onClick={() => {
              field('register', !info.register)
              setTried(false)
            }}
          >
            {info.register
              ? 'Sign in to an existing account instead'
              : 'Create a new account instead'}
          </button>
        )}
      </div>
    </form>
  )

  return (
    <Page
      title="Account & devices"
      subtitle="The CrashPlan account this device backs up to, and the devices that belong to it."
    >
      {firstSignIn && (
        <Notice>
          <p>
            <strong>Sign in to CrashPlan to use this interface.</strong>{' '}
            {setup.data?.authenticated
              ? 'CrashPlan is still signed in on this device and backups keep running. Sign in once more so this interface can manage it.'
              : 'This device has no active CrashPlan session yet, for example after the first start.'}
          </p>
          <p>
            Your settings, backup sets and device identity in <code>/config</code>{' '}
            are kept. Use the same CrashPlan account, so the device keeps its
            identity. The other pages open after sign-in.
          </p>
        </Notice>
      )}
      {result?.complete && <Notice tone="success">Signed in to CrashPlan.</Notice>}
      {result && !result.complete && (
        <Notice tone="warning">
          CrashPlan needs you to choose how this device is set up. Setup is
          not complete until you choose below.
        </Notice>
      )}
      {setup.isPending ? (
        <Section title="Account">
          <Loading />
        </Section>
      ) : (
        <>
          <ErrorNotice error={setup.error} />
          {signedIn && (
            <Section
              title="Account"
              actions={
                !showForm && (
                  <button
                    className="btn btn-sm"
                    onClick={() => {
                      setReauth(true)
                      setResult(undefined)
                    }}
                  >
                    Sign in again…
                  </button>
                )
              }
              footer={
                !showForm &&
                'Sign in again only if CrashPlan asks for it, for example after you changed your password.'
              }
            >
              <Row
                icon={
                  <span className="row-icon">
                    <UserRound size={16} aria-hidden="true" />
                  </span>
                }
                title={accountName || email || 'CrashPlan account'}
                detail={email && email !== accountName ? email : undefined}
                value={<Badge tone="green">Signed in</Badge>}
              />
              {server && <Row title="Server" value={<span className="wrap-anywhere">{server}</span>} />}
              <Row
                title="This device"
                value={
                  deviceName ||
                  (devices.isPending ? 'Loading…' : 'Not reported')
                }
              />
              {version && <Row title="CrashPlan version" value={version} />}
              {license && <Row title="License" value={license} />}
            </Section>
          )}
          {showForm && (
            <Section title={signedIn ? 'Sign in again' : 'Sign in'}>
              <div className="group-pad">{waiting || form}</div>
            </Section>
          )}
        </>
      )}
      {!firstSignIn && (
        <>
          <DeviceNames />
          <AccountDevices />
        </>
      )}
    </Page>
  )
}
