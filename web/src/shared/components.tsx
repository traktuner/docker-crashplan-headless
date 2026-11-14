// Shared building blocks. Every page composes these so spacing, type and
// states look the same everywhere. Class names live in style.css.
import { Component, useId } from 'react'
import type { ReactNode } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  Info,
  Loader2,
  XCircle,
} from 'lucide-react'
import { APIError } from './api'

export function message(error: unknown) {
  return error instanceof APIError
    ? error.message
    : 'Something went wrong. Check the container log for details.'
}
export function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

type Tone = 'info' | 'warning' | 'error' | 'success'
const toneIcons = {
  info: Info,
  warning: AlertTriangle,
  error: XCircle,
  success: CheckCircle2,
}
export function Notice({
  tone = 'info',
  children,
}: {
  tone?: Tone
  children: ReactNode
}) {
  const Icon = toneIcons[tone]
  return (
    <div
      className={'notice ' + (tone === 'info' ? '' : tone)}
      role={tone === 'error' ? 'alert' : 'status'}
    >
      <Icon size={16} aria-hidden="true" />
      <div>{children}</div>
    </div>
  )
}
export function ErrorNotice({ error }: { error: unknown }) {
  return error ? <Notice tone="error">{message(error)}</Notice> : null
}

export function Spinner({ size = 16 }: { size?: number }) {
  return <Loader2 size={size} className="spinner" aria-hidden="true" />
}
export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="loading" role="status">
      <Spinner /> {label}
    </div>
  )
}

export function Page({
  title,
  subtitle,
  actions,
  children,
}: {
  title: string
  subtitle?: ReactNode
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <>
      <header className="page-header">
        <div>
          <h1>{title}</h1>
          {subtitle && <p>{subtitle}</p>}
        </div>
        {actions && <div className="button-row">{actions}</div>}
      </header>
      {children}
    </>
  )
}

/** A titled group of rows, like a pane in macOS System Settings. */
export function Section({
  title,
  description,
  actions,
  footer,
  children,
  plain,
}: {
  title?: ReactNode
  description?: ReactNode
  actions?: ReactNode
  footer?: ReactNode
  children: ReactNode
  plain?: boolean
}) {
  const id = useId()
  return (
    <section className="section" aria-labelledby={title ? id : undefined}>
      {(title || actions) && (
        <div className="section-head">
          <div>
            {title && <h2 id={id}>{title}</h2>}
            {description && <p>{description}</p>}
          </div>
          {actions && <div className="button-row">{actions}</div>}
        </div>
      )}
      {plain ? children : <div className="group">{children}</div>}
      {footer && <p className="section-foot">{footer}</p>}
    </section>
  )
}

export function Row({
  title,
  detail,
  value,
  icon,
  children,
}: {
  title: ReactNode
  detail?: ReactNode
  value?: ReactNode
  icon?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className="row">
      {icon}
      <div className="row-main">
        <div className="row-title">{title}</div>
        {detail && <div className="row-detail">{detail}</div>}
      </div>
      {value !== undefined && <div className="row-value">{value}</div>}
      {children && <div className="row-control">{children}</div>}
    </div>
  )
}

export function Badge({
  tone,
  children,
}: {
  tone?: 'blue' | 'green' | 'orange' | 'red'
  children: ReactNode
}) {
  return <span className={'badge ' + (tone ?? '')}>{children}</span>
}

/** value 0..100, or undefined for an indeterminate (busy) bar. */
export function Progress({
  value,
  label,
  tone,
}: {
  value?: number
  label: string
  tone?: 'good'
}) {
  const known = value !== undefined && value >= 0
  return (
    <div
      className={'progress ' + (known ? '' : 'indeterminate ') + (tone ?? '')}
      role="progressbar"
      aria-label={label}
      aria-valuemin={known ? 0 : undefined}
      aria-valuemax={known ? 100 : undefined}
      aria-valuenow={known ? Math.round(value) : undefined}
    >
      <span style={{ width: known ? `${Math.min(100, value)}%` : undefined }} />
    </div>
  )
}

export function Switch({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean
  onChange: (value: boolean) => void
  label: string
  disabled?: boolean
}) {
  return (
    <label className="switch">
      <input
        type="checkbox"
        role="switch"
        aria-label={label}
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span />
    </label>
  )
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T
  options: { value: T; label: string }[]
  onChange: (value: T) => void
  label: string
}) {
  return (
    <div className="segmented" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}

export function Field({
  label,
  hint,
  children,
}: {
  label: ReactNode
  hint?: ReactNode
  children: ReactNode
}) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="field-hint">{hint}</span>}
    </label>
  )
}

export function Empty({
  icon,
  title,
  children,
}: {
  icon?: ReactNode
  title: string
  children?: ReactNode
}) {
  return (
    <div className="empty">
      {icon}
      <h3>{title}</h3>
      {children && <p>{children}</p>}
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
      <main className="login-page">
        <div className="login-card">
          <h1>This page could not be displayed</h1>
          <p className="muted">Reload the page to reconnect.</p>
          <button className="btn btn-primary" onClick={() => location.reload()}>
            Reload
          </button>
        </div>
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
    <header className="page-header">
      <div>
        <h1>{title}</h1>
        <p>{subtitle}</p>
      </div>
    </header>
  )
}
