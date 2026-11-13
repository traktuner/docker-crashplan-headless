import { Component } from 'react'
import type { ReactNode } from 'react'
import { RefreshCw } from 'lucide-react'
import { APIError } from './api'

export function message(error: unknown) {
  return error instanceof APIError
    ? error.message
    : 'The operation failed. Check the service logs.'
}
export function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}
export function ErrorNotice({ error }: { error: unknown }) {
  return error ? (
    <div className="notice error" role="alert">
      {message(error)}
    </div>
  ) : null
}
export function Loading() {
  return (
    <div className="loading" role="status">
      <RefreshCw size={18} className="spin" /> Loading…
    </div>
  )
}

export class Boundary extends Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  render() {
    return this.state.failed ? (
      <main className="login">
        <h1>This page could not be displayed.</h1>
        <p>Reload the page to reconnect.</p>
        <button onClick={() => location.reload()}>Reload</button>
      </main>
    ) : (
      this.props.children
    )
  }
}

export function PageTitle({
  title,
  subtitle,
}: {
  title: string
  subtitle: string
}) {
  return (
    <header className="page-title">
      <h1>{title}</h1>
      <p className="muted">{subtitle}</p>
    </header>
  )
}
