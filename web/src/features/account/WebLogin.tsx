import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ShieldCheck, ArrowRight } from 'lucide-react'
import { api } from '../../shared/api'
import type { Session } from '../../shared/api'
import { ErrorNotice } from '../../shared/components'
import { ProjectNotice } from '../../shared/ProjectNotice'

export function WebLogin({
  onSession,
}: {
  onSession: (session: Session) => void
}) {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const login = useMutation({
    mutationFn: () => api.signIn(username, password),
    onSuccess: onSession,
    onSettled: () => setPassword(''),
  })
  return (
    <main className="login">
      <div className="brand-mark">
        <ShieldCheck size={30} />
      </div>
      <p className="eyebrow">CRASHPLAN</p>
      <h1>Sign in</h1>
      <p className="muted">Sign in to manage this backup service.</p>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          login.mutate()
        }}
      >
        <label>
          Username
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
          />
        </label>
        <label>
          Password
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>
        <ErrorNotice error={login.error} />
        <button disabled={login.isPending || !password}>
          Sign in <ArrowRight size={16} />
        </button>
      </form>
      <ProjectNotice />
    </main>
  )
}
