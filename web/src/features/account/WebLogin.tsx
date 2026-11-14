import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ShieldCheck } from 'lucide-react'
import { api } from '../../shared/api'
import type { Session } from '../../shared/api'
import { ErrorNotice, Field } from '../../shared/components'
import { ProjectNotice } from '../../shared/ProjectNotice'
import { ThemeSwitch } from '../../shared/ThemeSwitch'

export function WebLogin({
  onSession,
}: {
  onSession: (session: Session) => void
}) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const login = useMutation({
    mutationFn: () => api.signIn(username, password),
    onSuccess: onSession,
    onSettled: () => setPassword(''),
  })
  return (
    <main className="login-page">
      <div className="login-theme">
        <ThemeSwitch />
      </div>
      <div className="login-card">
        <span className="brand-mark">
          <ShieldCheck size={28} />
        </span>
        <div>
          <h1>CrashPlan</h1>
          <p className="muted" style={{ marginTop: 6 }}>
            Sign in to the web interface of this container. This is not your
            CrashPlan account.
          </p>
        </div>
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            login.mutate()
          }}
        >
          <Field label="User name">
            <input
              className="input"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
            />
          </Field>
          <Field label="Password">
            <input
              className="input"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
          </Field>
          <ErrorNotice error={login.error} />
          <button
            className="btn btn-primary btn-lg btn-block"
            disabled={login.isPending || !username || !password}
          >
            {login.isPending ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
        <ProjectNotice />
      </div>
    </main>
  )
}
