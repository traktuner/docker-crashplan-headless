import { useState, useEffect, useSyncExternalStore, lazy, Suspense } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AppFrame } from './AppFrame'
import { pageFromHash } from './navigation'
import type { Page } from './navigation'
import { api, APIError } from '../shared/api'
import type { Session } from '../shared/api'
import { ErrorNotice, Loading } from '../shared/components'
import { refreshStatus } from '../shared/invalidation'
import { queryClient } from './query'
const Account = lazy(() =>
  import('../features/account/Account').then((m) => ({ default: m.Account })),
)
const WebLogin = lazy(() =>
  import('../features/account/WebLogin').then((m) => ({ default: m.WebLogin })),
)
const Restore = lazy(() =>
  import('../features/restore/Restore').then((m) => ({ default: m.Restore })),
)
const Settings = lazy(() =>
  import('../features/settings/Settings').then((m) => ({
    default: m.Settings,
  })),
)
const BackupSets = lazy(() =>
  import('../features/backup/BackupSets').then((m) => ({
    default: m.BackupSets,
  })),
)
const Security = lazy(() =>
  import('../features/security/Security').then((m) => ({
    default: m.Security,
  })),
)
const BackupActivity = lazy(() =>
  import('../features/activity/Activity').then((m) => ({
    default: m.Activity,
  })),
)
const Overview = lazy(() =>
  import('../features/overview/Overview').then((m) => ({
    default: m.Overview,
  })),
)
const System = lazy(() =>
  import('../features/system/System').then((m) => ({ default: m.System })),
)

export function App() {
  const engineChanged = useSyncExternalStore(api.subscribeContext, api.contextChanged)
  const [session, setSession] = useState<Session>()
  const [ready, setReady] = useState(false)
  const [page, setPage] = useState<Page>(() => pageFromHash(location.hash))
  const [sessionError, setSessionError] = useState<unknown>()
  function navigate(next: Page) {
    setPage(next)
    history.pushState(null, '', '#' + next)
    requestAnimationFrame(() =>
      document.getElementById('main-content')?.focus({ preventScroll: true }),
    )
    window.scrollTo({ top: 0 })
  }
  // The control needs its own CrashPlan session. The engine can still report an
  // authorized account (for example after moving an existing /config); backups
  // then keep running, but every control needs this session first.
  const native = useQuery({
    queryKey: ['capabilities'],
    queryFn: () => api.capabilities<{ nativeSession?: boolean }>(),
    enabled: !!session && !engineChanged,
    refetchInterval: 15000,
  })
  const signedIn =
    typeof native.data?.nativeSession === 'boolean'
      ? native.data.nativeSession
      : undefined
  const needsSignIn = signedIn === false
  const ungated = (p: Page) => p === 'account' || p === 'system'
  const shown: Page = needsSignIn && !ungated(page) ? 'account' : page
  // Move to the account page itself, so the user stays there after sign-in
  // and sees its result and any device choice CrashPlan still needs.
  useEffect(() => {
    if (needsSignIn && !ungated(page)) {
      setPage('account')
      history.replaceState(null, '', '#account')
    }
  }, [needsSignIn, page])
  // Until the session state is known, gated pages wait instead of starting
  // reads that may fail. If the state cannot be read, pages open as before.
  const checking = !!session && native.isPending && !ungated(page)
  useEffect(() => {
    const pop = () => setPage(pageFromHash(location.hash))
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [])
  useEffect(() => {
    api
      .session()
      .then(setSession)
      .catch((error) => {
        if (!(
          error instanceof APIError &&
          error.code === 'WEB_AUTHORIZATION_REQUIRED'
        ))
          setSessionError(error)
      })
      .finally(() => setReady(true))
  }, [])
  // An expired or ended web session returns the interface to its sign-in page.
  function endExpiredSession(error: unknown) {
    if (
      error instanceof APIError &&
      error.code === 'WEB_AUTHORIZATION_REQUIRED'
    ) {
      queryClient.clear()
      setSession(undefined)
    }
  }
  useEffect(() => {
    if (!session || engineChanged) return
    const events = new EventSource('/api/v1/events')
    // Trailing throttle: one refresh at most every 2 s, and the last burst of
    // notifications is never dropped.
    let timer: ReturnType<typeof setTimeout> | undefined
    const update = () => {
      if (timer) return
      timer = setTimeout(() => {
        timer = undefined
        void refreshStatus(queryClient)
      }, 2000)
    }
    events.addEventListener('update', update)
    events.onerror = () => {
      void api.session(true).catch(endExpiredSession)
    }
    return () => {
      if (timer) clearTimeout(timer)
      events.close()
    }
  }, [session, engineChanged])
  useEffect(() => {
    if (!session || engineChanged) return
    let renewed = Date.now()
    const activity = () => {
      if (Date.now() - renewed < 60_000) return
      renewed = Date.now()
      void api.session().catch(endExpiredSession)
    }
    window.addEventListener('pointerdown', activity, { passive: true })
    window.addEventListener('keydown', activity)
    return () => {
      window.removeEventListener('pointerdown', activity)
      window.removeEventListener('keydown', activity)
    }
  }, [session, engineChanged])
  async function logout() {
    try {
      await api.signOut()
      queryClient.clear()
      setSession(undefined)
    } catch (error) {
      setSessionError(error)
    }
  }
  if (engineChanged)
    return (
      <main className="login-page">
        <section className="login-card" role="alert">
          <h1>CrashPlan session changed</h1>
          <p className="muted">
            Reload to see the current account. Unsaved changes are discarded.
          </p>
          <button className="btn btn-primary" onClick={() => window.location.reload()}>
            Reload
          </button>
        </section>
      </main>
    )
  if (!ready)
    return (
      <main className="login-page">
        <Loading />
      </main>
    )
  if (!session)
    return (
      <>
        <ErrorNotice error={sessionError} />
        <Suspense fallback={<Loading />}>
          <WebLogin
            onSession={(value) => {
              setSession(value)
              setSessionError(undefined)
            }}
          />
        </Suspense>
      </>
    )
  return (
    <AppFrame
      page={shown}
      navigate={navigate}
      logout={() => void logout()}
      signedIn={signedIn}
    >
      <ErrorNotice error={sessionError} />
      <Suspense fallback={<Loading />}>
        {checking ? (
          <Loading />
        ) : shown === 'overview' ? (
          <Overview navigate={navigate} signedIn={signedIn} />
        ) : shown === 'activity' ? (
          <BackupActivity signedIn={signedIn} />
        ) : shown === 'sets' ? (
          <BackupSets />
        ) : shown === 'restore' ? (
          <Restore />
        ) : shown === 'settings' ? (
          <Settings />
        ) : shown === 'security' ? (
          <Security />
        ) : shown === 'account' ? (
          <Account firstSignIn={needsSignIn} />
        ) : (
          <System />
        )}
      </Suspense>
    </AppFrame>
  )
}
