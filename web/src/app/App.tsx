import { useState, useEffect, useSyncExternalStore, lazy, Suspense } from 'react'
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
    let last = 0
    const update = () => {
      if (Date.now() - last < 2000) return
      last = Date.now()
      void refreshStatus(queryClient)
    }
    events.addEventListener('update', update)
    events.onerror = () => {
      void api.session(true).catch(endExpiredSession)
    }
    return () => events.close()
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
      <main className="login">
        <section className="card" role="alert">
          <h1>CrashPlan session changed</h1>
          <p>Reload the interface to review the current account. Unsaved forms will be cleared.</p>
          <button onClick={() => window.location.reload()}>Reload interface</button>
        </section>
      </main>
    )
  if (!ready)
    return (
      <main className="login">
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
    <AppFrame page={page} navigate={navigate} logout={() => void logout()}>
      <ErrorNotice error={sessionError} />
      <Suspense fallback={<Loading />}>
        {page === 'overview' ? (
          <Overview navigate={navigate} />
        ) : page === 'activity' ? (
          <BackupActivity />
        ) : page === 'sets' ? (
          <BackupSets />
        ) : page === 'restore' ? (
          <Restore />
        ) : page === 'settings' ? (
          <Settings />
        ) : page === 'security' ? (
          <Security />
        ) : page === 'account' ? (
          <Account />
        ) : (
          <System />
        )}
      </Suspense>
    </AppFrame>
  )
}
