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
  // Checked on submit: password managers fill fields without input events.
  const [problem, setProblem] = useState('')
  const login = useMutation({
    mutationFn: ({ user, pass }: { user: string; pass: string }) => api.signIn(user, pass),
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
            const form = new FormData(e.currentTarget)
            const user = String(form.get('username') ?? username)
            const pass = String(form.get('password') ?? password)
            if (!user.trim() || !pass) {
              setProblem(!user.trim() ? 'Enter the user name.' : 'Enter the password.')
              return
            }
            setProblem('')
            setUsername(user)
            setPassword(pass)
            login.mutate({ user, pass })
          }}
        >
          <Field label="User name">
            <input
              className="input"
              name="username"
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
              name="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
          </Field>
          {problem && (
            <span className="inline-error" role="alert">
              {problem}
            </span>
          )}
          <ErrorNotice error={login.error} />
          <button className="btn btn-primary btn-lg btn-block" disabled={login.isPending}>
            {login.isPending ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
        <ProjectNotice />
      </div>
    </main>
  )
}
