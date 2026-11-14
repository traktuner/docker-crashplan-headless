// Shared building blocks. Every page composes these so spacing, type and
// states look the same everywhere. Class names live in style.css.
import { Component, useEffect, useId, useRef, useState } from 'react'
import type { KeyboardEvent, ReactNode } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  ChevronRight,
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
  valueText,
}: {
  value?: number
  label: string
  tone?: 'good'
  /** Spoken instead of the bare percentage, e.g. "92.5% · 184 GB left". */
  valueText?: string
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
      aria-valuetext={valueText}
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

/**
 * A segmented control. With four or more options it turns into a menu on
 * phones, where the buttons would not fit. `tabs` makes it a tab list with
 * arrow-key navigation; `marked` puts a dot on options with unsaved changes.
 */
export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
  disabled,
  tabs,
  marked,
}: {
  value: T
  options: { value: T; label: string }[]
  onChange: (value: T) => void
  label: string
  disabled?: boolean
  tabs?: boolean
  marked?: readonly T[]
}) {
  // Short labels still fit a phone; longer sets of four or more do not.
  const collapsible =
    options.length >= 4 && options.reduce((n, o) => n + o.label.length, 0) > 24
  const move = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!tabs) return
    const index = options.findIndex((o) => o.value === value)
    const step =
      event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    const target =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? options.length - 1
          : step
            ? (index + step + options.length) % options.length
            : -1
    if (target < 0) return
    event.preventDefault()
    onChange(options[target]!.value)
    const buttons = event.currentTarget.querySelectorAll('button')
    requestAnimationFrame(() => buttons[target]?.focus())
  }
  return (
    <>
      <div
        className={'segmented' + (collapsible ? ' collapsible' : '')}
        role={tabs ? 'tablist' : 'group'}
        aria-label={label}
        onKeyDown={move}
      >
        {options.map((option) => {
          const current = value === option.value
          return (
            <button
              key={option.value}
              type="button"
              role={tabs ? 'tab' : undefined}
              aria-selected={tabs ? current : undefined}
              aria-pressed={tabs ? undefined : current}
              tabIndex={tabs && !current ? -1 : undefined}
              disabled={disabled}
              onClick={() => onChange(option.value)}
            >
              {option.label}
              {marked?.includes(option.value) && (
                <span className="tab-dot" aria-label="(unsaved changes)" />
              )}
            </button>
          )
        })}
      </div>
      {collapsible && (
        <select
          className="select segmented-select"
          aria-label={label}
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value as T)}
        >
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
              {marked?.includes(option.value) ? ' •' : ''}
            </option>
          ))}
        </select>
      )}
    </>
  )
}

/** Raw identifiers stay out of sight unless someone asks for them. */
export function TechnicalDetails({ children }: { children: ReactNode }) {
  return (
    <details className="disclosure technical">
      <summary>
        <ChevronRight size={14} aria-hidden="true" />
        Technical details
      </summary>
      <div className="technical-body">{children}</div>
    </details>
  )
}

/** A modal sheet on the shared .dialog styles. Escape and the backdrop close it. */
export function Dialog({
  open,
  title,
  children,
  actions,
  onClose,
}: {
  open: boolean
  title: string
  children?: ReactNode
  actions: ReactNode
  onClose: () => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const id = useId()
  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])
  if (!open) return null
  return (
    <dialog
      ref={ref}
      className="dialog"
      aria-labelledby={id}
      onClose={onClose}
      onCancel={(e) => {
        e.preventDefault()
        onClose()
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose()
      }}
    >
      <div className="dialog-body">
        <h2 id={id}>{title}</h2>
        {children}
      </div>
      <div className="dialog-actions">{actions}</div>
    </dialog>
  )
}

export interface ConfirmOptions {
  title: string
  message?: ReactNode
  confirm: string
  destructive?: boolean
}

/**
 * Replaces window.confirm(): `const [dialog, ask] = useConfirm()`, render
 * `{dialog}`, then `if (await ask({...})) act()`.
 */
export function useConfirm(): [ReactNode, (options: ConfirmOptions) => Promise<boolean>] {
  const [state, setState] = useState<{
    options: ConfirmOptions
    resolve: (ok: boolean) => void
  }>()
  const finish = (ok: boolean) => {
    state?.resolve(ok)
    setState(undefined)
  }
  const dialog = (
    <Dialog
      open={!!state}
      title={state?.options.title ?? ''}
      onClose={() => finish(false)}
      actions={
        <>
          <button type="button" className="btn" onClick={() => finish(false)}>
            Cancel
          </button>
          <button
            type="button"
            className={'btn ' + (state?.options.destructive ? 'btn-destructive-fill' : 'btn-primary')}
            autoFocus
            onClick={() => finish(true)}
          >
            {state?.options.confirm}
          </button>
        </>
      }
    >
      {state?.options.message && <div className="dialog-message">{state.options.message}</div>}
    </Dialog>
  )
  const ask = (options: ConfirmOptions) =>
    new Promise<boolean>((resolve) => {
      setState((old) => {
        old?.resolve(false)
        return { options, resolve }
      })
    })
  return [dialog, ask]
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
