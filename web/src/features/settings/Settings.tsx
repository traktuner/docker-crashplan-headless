import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isLosslessNumber } from 'lossless-json'
import { CheckCircle2 } from 'lucide-react'
import { api } from '../../shared/api'
import type { Setting, SettingsArea } from '../../shared/api'
import {
  Badge,
  ErrorNotice,
  Loading,
  Notice,
  Page,
  Section,
  Segmented,
  Switch,
} from '../../shared/components'
import { joinDuration, splitDuration } from '../../shared/format'
import type { DurationUnit } from '../../shared/format'
import './settings.css'

type Change = (value: unknown) => void
type Validity = (valid: boolean) => void
type WindowValue = {
  alwaysRun: boolean
  startTimeOfDay: string
  endTimeOfDay: string
  days: string[]
}
type PatternValue = { pattern: string; regularExpression: boolean }

const days = [
  { id: 'MONDAY', label: 'Mon' },
  { id: 'TUESDAY', label: 'Tue' },
  { id: 'WEDNESDAY', label: 'Wed' },
  { id: 'THURSDAY', label: 'Thu' },
  { id: 'FRIDAY', label: 'Fri' },
  { id: 'SATURDAY', label: 'Sat' },
  { id: 'SUNDAY', label: 'Sun' },
]

const enumOptions: Record<string, { value: string; label: string }[]> = {
  compression: [
    { value: 'AUTOMATIC', label: 'Automatic' },
    { value: 'ON', label: 'On' },
    { value: 'OFF', label: 'Off' },
  ],
  dedup: [
    { value: 'AUTOMATIC', label: 'Automatic' },
    { value: 'FULL', label: 'Full' },
    { value: 'MINIMAL', label: 'Minimal' },
  ],
  notification: [
    { value: 'ANY', label: 'Any time' },
    { value: 'MORNING', label: 'In the morning' },
    { value: 'AFTERNOON', label: 'In the afternoon' },
    { value: 'EVENING', label: 'In the evening' },
    { value: 'NIGHT', label: 'At night' },
    { value: 'NEVER', label: 'Never' },
  ],
}

// ---------- value helpers ----------

function canonical(value: unknown): unknown {
  if (isLosslessNumber(value)) return value.toString()
  if (Array.isArray(value)) return value.map(canonical)
  if (value && typeof value === 'object')
    return Object.fromEntries(
      Object.entries(value)
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([k, v]) => [k, canonical(v)]),
    )
  return value
}
function identity(value: unknown): string {
  return JSON.stringify(canonical(value)) ?? 'undefined'
}

/** Numeric text of a setting value, normalized so 1024, "1024" and 1024.0 match. */
function numberText(value: unknown): string | undefined {
  const raw = isLosslessNumber(value)
    ? value.toString()
    : typeof value === 'number' && Number.isFinite(value)
      ? String(value)
      : typeof value === 'string'
        ? value.trim()
        : undefined
  if (raw === undefined || !/^-?\d+(\.\d+)?([eE][-+]?\d+)?$/.test(raw))
    return undefined
  if (/^-?\d+$/.test(raw)) return BigInt(raw).toString()
  return String(Number(raw))
}

const numericKinds = new Set([
  'integer',
  'long',
  'cpu',
  'percent',
  'byte',
  'rate',
  'retention-interval',
])

/** True when two values of a setting mean the same thing. */
export function sameSetting(field: Setting, a: unknown, b: unknown): boolean {
  if (numericKinds.has(field.kind)) {
    const x = numberText(a)
    const y = numberText(b)
    if (x !== undefined && y !== undefined) return x === y
  }
  if (field.kind === 'window') {
    const sorted = (v: unknown) => {
      const w = v as WindowValue | null
      return w && Array.isArray(w.days) ? { ...w, days: [...w.days].sort() } : w
    }
    return identity(sorted(a)) === identity(sorted(b))
  }
  return identity(a) === identity(b)
}

function windowProblem(value: unknown): string | undefined {
  const window = value as WindowValue | null | undefined
  if (!window || window.alwaysRun) return undefined
  if (!window.startTimeOfDay || !window.endTimeOfDay)
    return 'Choose a start and an end time.'
  if (!window.days?.length) return 'Choose at least one day.'
  return undefined
}
function patternProblem(value: unknown): string | undefined {
  const patterns = (value ?? []) as PatternValue[]
  return patterns.some((p) => !p.pattern.trim())
    ? 'Enter a pattern or remove the empty line.'
    : undefined
}

/** A message when a value cannot be saved as it is, judged from the value alone. */
export function settingProblem(
  field: Setting,
  value: unknown,
): string | undefined {
  if (field.kind === 'window') return windowProblem(value)
  if (field.kind === 'patterns') return patternProblem(value)
  return undefined
}

/**
 * Local editing state for text-based controls. The text a person types stays
 * local; only valid values are passed to `change`. When the value changes from
 * outside (save, revert), the local text follows it.
 */
function useEditor<T>(value: unknown, init: (value: unknown) => T) {
  const key = identity(value)
  const [state, setState] = useState(() => ({
    key,
    local: init(value),
    error: undefined as string | undefined,
  }))
  let current = state
  if (state.key !== key) {
    current = { key, local: init(value), error: undefined }
    setState(current)
  }
  return {
    local: current.local,
    error: current.error,
    accept(local: T, next: unknown, change: Change, valid?: Validity) {
      setState({ key: identity(next), local, error: undefined })
      valid?.(true)
      change(next)
    },
    reject(local: T, error: string, valid?: Validity) {
      setState((old) => ({ ...old, local, error }))
      valid?.(false)
    },
  }
}

function ControlShell({
  error,
  children,
}: {
  error?: string
  children: ReactNode
}) {
  return (
    <div className="setting-control">
      {children}
      {error && <div className="setting-error">{error}</div>}
    </div>
  )
}

type ControlProps = {
  field: Setting
  value: unknown
  disabled: boolean
  change: Change
  onValidityChange?: Validity
  prefix?: string
}

// ---------- controls ----------

function unitLabel(unit: DurationUnit, amount: string) {
  const singular = amount.trim() === '1'
  return { minutes: 'minute', hours: 'hour', days: 'day' }[unit] + (singular ? '' : 's')
}

function DurationControl({
  field,
  value,
  disabled,
  change,
  onValidityChange,
  prefix,
}: ControlProps) {
  const factor = field.unit === 'minutes' ? 60_000 : 1
  const editor = useEditor(value, (v) => {
    const n = numberText(v)
    if (n === undefined) return { amount: '', unit: 'hours' as DurationUnit }
    return splitDuration(factor === 1 ? n : Number(n) * factor)
  })
  const { amount, unit } = editor.local
  const update = (next: { amount: string; unit: DurationUnit }) => {
    const ms = joinDuration(next.amount, next.unit)
    if (ms === undefined)
      return editor.reject(next, 'Enter a number, for example 7.', onValidityChange)
    if (factor === 1) return editor.accept(next, ms, change, onValidityChange)
    const minutes = Number(ms) / factor
    if (!Number.isInteger(minutes))
      return editor.reject(next, 'Use whole minutes.', onValidityChange)
    if (minutes > 2_147_483_647)
      return editor.reject(next, 'This value is too large.', onValidityChange)
    editor.accept(next, minutes, change, onValidityChange)
  }
  return (
    <ControlShell error={editor.error}>
      <div className="setting-inline">
        {prefix && <span className="setting-suffix">{prefix}</span>}
        <input
          className="input setting-number"
          inputMode="decimal"
          aria-label={field.label}
          aria-invalid={editor.error ? true : undefined}
          value={amount}
          disabled={disabled}
          onChange={(e) => update({ amount: e.target.value, unit })}
        />
        <select
          className="select setting-unit"
          aria-label={field.label + ' unit'}
          value={unit}
          disabled={disabled}
          onChange={(e) =>
            update({ amount, unit: e.target.value as DurationUnit })
          }
        >
          {(['minutes', 'hours', 'days'] as const).map((u) => (
            <option key={u} value={u}>
              {unitLabel(u, amount)}
            </option>
          ))}
        </select>
      </div>
    </ControlShell>
  )
}

const sizeUnits = [
  { id: 'bytes', size: 1 },
  { id: 'KB', size: 1_000 },
  { id: 'MB', size: 1_000_000 },
  { id: 'GB', size: 1_000_000_000 },
]
function SizeControl({
  field,
  value,
  disabled,
  change,
  onValidityChange,
}: ControlProps) {
  const editor = useEditor(value, (v) => {
    const n = Number(numberText(v) ?? '0')
    if (n === 0) return { amount: '0', unit: 'MB' }
    const unit =
      [...sizeUnits]
        .reverse()
        .find((u) => n >= u.size && Number.isInteger((n * 1000) / u.size)) ??
      { id: 'bytes', size: 1 }
    return { amount: String(n / unit.size), unit: unit.id }
  })
  const update = (next: { amount: string; unit: string }) => {
    const n = Number(next.amount)
    if (!next.amount.trim() || !Number.isFinite(n) || n < 0)
      return editor.reject(next, 'Enter a size, for example 10.', onValidityChange)
    const total = Math.round(n * sizeUnits.find((u) => u.id === next.unit)!.size)
    if (!Number.isSafeInteger(total))
      return editor.reject(next, 'This value is too large.', onValidityChange)
    editor.accept(next, String(total), change, onValidityChange)
  }
  return (
    <ControlShell error={editor.error}>
      <div className="setting-inline">
        <input
          className="input setting-number"
          inputMode="decimal"
          aria-label={field.label}
          aria-invalid={editor.error ? true : undefined}
          value={editor.local.amount}
          disabled={disabled}
          onChange={(e) =>
            update({ amount: e.target.value, unit: editor.local.unit })
          }
        />
        <select
          className="select setting-unit"
          aria-label={field.label + ' unit'}
          value={editor.local.unit}
          disabled={disabled}
          onChange={(e) =>
            update({ amount: editor.local.amount, unit: e.target.value })
          }
        >
          {sizeUnits.map((u) => (
            <option key={u.id} value={u.id}>
              {u.id}
            </option>
          ))}
        </select>
      </div>
    </ControlShell>
  )
}

const numberRules: Record<
  string,
  { min: number; max: number; suffix?: string; text?: boolean }
> = {
  cpu: { min: 1, max: 100, suffix: '%' },
  percent: { min: 0, max: 100, suffix: '%' },
  byte: { min: 0, max: 255 },
  integer: { min: 0, max: 2_147_483_647 },
  long: { min: 0, max: Number.MAX_SAFE_INTEGER, text: true },
}
function NumberControl({
  field,
  value,
  disabled,
  change,
  onValidityChange,
}: ControlProps) {
  const rule = numberRules[field.kind] ?? { min: 0, max: 2_147_483_647 }
  const suffix = rule.suffix ?? field.unit
  const editor = useEditor(value, (v) => numberText(v) ?? '')
  const update = (text: string) => {
    const trimmed = text.trim()
    const n = Number(trimmed)
    if (!/^\d+$/.test(trimmed) || n < rule.min || n > rule.max)
      return editor.reject(
        text,
        rule.max >= 1_000_000
          ? 'Enter a whole number.'
          : `Enter a whole number from ${rule.min} to ${rule.max}.`,
        onValidityChange,
      )
    editor.accept(text, rule.text ? trimmed : n, change, onValidityChange)
  }
  return (
    <ControlShell error={editor.error}>
      <div className="setting-inline">
        <input
          className="input setting-number"
          inputMode="numeric"
          aria-label={field.label}
          aria-invalid={editor.error ? true : undefined}
          value={editor.local}
          disabled={disabled}
          onChange={(e) => update(e.target.value)}
        />
        {suffix && <span className="setting-suffix">{suffix}</span>}
      </div>
    </ControlShell>
  )
}

function RateControl({
  field,
  value,
  disabled,
  change,
  onValidityChange,
}: ControlProps) {
  const current = numberText(value)
  const unlimited = current !== undefined && Number(current) === 0
  const editor = useEditor(value, (v) => {
    const n = numberText(v)
    return n === undefined || Number(n) === 0 ? '' : n
  })
  const [remembered, setRemembered] = useState(
    current && Number(current) > 0 ? current : '1000',
  )
  const update = (text: string) => {
    const trimmed = text.trim()
    const n = Number(trimmed)
    if (!trimmed || !/^\d+(\.\d+)?$/.test(trimmed) || !Number.isFinite(n))
      return editor.reject(text, 'Enter a number, for example 1000.', onValidityChange)
    if (n === 0)
      return editor.reject(
        text,
        'Enter a limit above 0, or turn on Unlimited.',
        onValidityChange,
      )
    setRemembered(trimmed)
    editor.accept(text, n, change, onValidityChange)
  }
  return (
    <ControlShell error={unlimited ? undefined : editor.error}>
      <div className="setting-inline">
        {!unlimited && (
          <>
            <input
              className="input setting-number"
              inputMode="decimal"
              aria-label={field.label}
              aria-invalid={editor.error ? true : undefined}
              value={editor.local}
              disabled={disabled}
              onChange={(e) => update(e.target.value)}
            />
            <span className="setting-suffix">{field.unit || 'KB/s'}</span>
          </>
        )}
        <span className="setting-switch-label">
          Unlimited
          <Switch
            label={field.label + ': unlimited'}
            checked={unlimited}
            disabled={disabled}
            onChange={(on) => {
              onValidityChange?.(true)
              change(on ? 0 : Number(remembered))
            }}
          />
        </span>
      </div>
    </ControlShell>
  )
}

function TextControl({
  field,
  value,
  disabled,
  change,
  onValidityChange,
}: ControlProps) {
  const url = field.kind === 'url' || field.kind === 'optional-url'
  const time = field.kind === 'time'
  const editor = useEditor(value, (v) =>
    v == null ? '' : isLosslessNumber(v) ? v.toString() : String(v),
  )
  const update = (text: string) => {
    if (time && !/^\d\d:\d\d$/.test(text))
      return editor.reject(text, 'Choose a time.', onValidityChange)
    if (url && (text || field.kind === 'url')) {
      let ok = false
      try {
        const parsed = new URL(text)
        ok =
          (parsed.protocol === 'https:' || parsed.protocol === 'http:') &&
          !!parsed.hostname &&
          !parsed.username
      } catch {
        ok = false
      }
      if (!ok)
        return editor.reject(
          text,
          'Enter a web address that starts with https:// or http://.',
          onValidityChange,
        )
    }
    editor.accept(text, text, change, onValidityChange)
  }
  return (
    <ControlShell error={editor.error}>
      <input
        className="input"
        type={time ? 'time' : 'text'}
        inputMode={url ? 'url' : undefined}
        placeholder={url ? 'https://' : undefined}
        spellCheck={false}
        aria-label={field.label}
        aria-invalid={editor.error ? true : undefined}
        value={editor.local}
        disabled={disabled}
        onChange={(e) => update(e.target.value)}
      />
    </ControlShell>
  )
}

function EnumControl({ field, value, disabled, change }: ControlProps) {
  const options = enumOptions[field.kind] ?? []
  const current = String(value ?? '')
  const known = options.some((o) => o.value === current)
  return (
    <select
      className="select"
      aria-label={field.label}
      disabled={disabled}
      value={known ? current : ''}
      onChange={(e) => change(e.target.value)}
    >
      {!known && (
        <option value="" disabled>
          Choose…
        </option>
      )}
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  )
}

function WindowControl({
  value,
  disabled,
  change,
  label,
}: {
  value: unknown
  disabled: boolean
  change: Change
  label: string
}) {
  const window = (value ?? {
    alwaysRun: true,
    startTimeOfDay: '00:00',
    endTimeOfDay: '23:59',
    days: days.map((d) => d.id),
  }) as WindowValue
  const selected = window.days ?? []
  return (
    <ControlShell error={windowProblem(window)}>
      <div className="setting-window">
        <div className="setting-inline">
          <span className="setting-switch-label">
            Any time
            <Switch
              label={label + ': any time'}
              checked={window.alwaysRun}
              disabled={disabled}
              onChange={(on) =>
                change({
                  ...window,
                  alwaysRun: on,
                  startTimeOfDay: window.startTimeOfDay || '00:00',
                  endTimeOfDay: window.endTimeOfDay || '23:59',
                })
              }
            />
          </span>
        </div>
        {!window.alwaysRun && (
          <>
            <div className="setting-inline">
              <span className="setting-suffix">From</span>
              <input
                className="input"
                type="time"
                aria-label={label + ': from'}
                value={window.startTimeOfDay ?? ''}
                disabled={disabled}
                onChange={(e) =>
                  change({ ...window, startTimeOfDay: e.target.value })
                }
              />
              <span className="setting-suffix">to</span>
              <input
                className="input"
                type="time"
                aria-label={label + ': until'}
                value={window.endTimeOfDay ?? ''}
                disabled={disabled}
                onChange={(e) =>
                  change({ ...window, endTimeOfDay: e.target.value })
                }
              />
            </div>
            <div className="setting-days" role="group" aria-label={label + ': days'}>
              {days.map((day) => {
                const on = selected.includes(day.id)
                return (
                  <button
                    type="button"
                    className="setting-day"
                    key={day.id}
                    aria-pressed={on}
                    disabled={disabled}
                    onClick={() =>
                      change({
                        ...window,
                        days: on
                          ? selected.filter((d) => d !== day.id)
                          : days
                              .map((d) => d.id)
                              .filter((d) => d === day.id || selected.includes(d)),
                      })
                    }
                  >
                    {day.label}
                  </button>
                )
              })}
            </div>
          </>
        )}
      </div>
    </ControlShell>
  )
}

export function PatternControl({
  value,
  disabled,
  change,
}: {
  value: unknown
  disabled: boolean
  change: (v: unknown) => void
  onValidityChange?: Validity
}) {
  const patterns = (value ?? []) as PatternValue[]
  return (
    <ControlShell error={patternProblem(patterns)}>
      <div className="setting-patterns">
        {patterns.length === 0 && (
          <div className="setting-patterns-empty">No patterns.</div>
        )}
        {patterns.map((pattern, index) => (
          <div className="setting-pattern" key={index}>
            <input
              className="input"
              aria-label={`Pattern ${index + 1}`}
              spellCheck={false}
              value={pattern.pattern}
              disabled={disabled}
              onChange={(e) =>
                change(
                  patterns.map((p, i) =>
                    i === index ? { ...p, pattern: e.target.value } : p,
                  ),
                )
              }
            />
            <label className="check">
              <input
                type="checkbox"
                disabled={disabled}
                checked={pattern.regularExpression}
                onChange={(e) =>
                  change(
                    patterns.map((p, i) =>
                      i === index
                        ? { ...p, regularExpression: e.target.checked }
                        : p,
                    ),
                  )
                }
              />
              Regular expression
            </label>
            <button
              type="button"
              className="btn btn-plain btn-sm"
              disabled={disabled}
              onClick={() => change(patterns.filter((_, i) => i !== index))}
            >
              Remove
            </button>
          </div>
        ))}
        <div>
          <button
            type="button"
            className="btn btn-sm"
            disabled={disabled}
            onClick={() =>
              change([...patterns, { pattern: '', regularExpression: false }])
            }
          >
            Add pattern
          </button>
        </div>
      </div>
    </ControlShell>
  )
}

/** Editor for one engine setting, chosen by its kind and unit. */
export function SettingControl({
  field,
  value,
  disabled,
  change,
  onValidityChange,
  prefix,
}: {
  field: Setting
  value: unknown
  disabled: boolean
  change: (v: unknown) => void
  /** Called with false while typed text cannot be saved, true once it can. */
  onValidityChange?: (valid: boolean) => void
  /** Short word before interval controls, such as "Every". */
  prefix?: string
}) {
  const props = { field, value, disabled, change, onValidityChange, prefix }
  if (field.kind === 'retention-interval' && numberText(value) === '-1')
    return (
      <output className="row-value" aria-label={field.label}>
        Never
      </output>
    )
  if (Object.hasOwn(enumOptions, field.kind)) return <EnumControl {...props} />
  if (field.kind === 'boolean')
    return (
      <Switch
        label={field.label}
        checked={value === true}
        disabled={disabled}
        onChange={change}
      />
    )
  if (field.kind === 'window')
    return (
      <WindowControl
        value={value}
        disabled={disabled}
        change={change}
        label={field.label}
      />
    )
  if (field.kind === 'patterns')
    return <PatternControl value={value} disabled={disabled} change={change} />
  if (
    (field.kind === 'long' && field.unit === 'ms') ||
    ((field.kind === 'integer' || field.kind === 'retention-interval') &&
      field.unit === 'minutes')
  )
    return <DurationControl {...props} />
  if (field.kind === 'long' && field.unit === 'bytes')
    return <SizeControl {...props} />
  if (field.kind === 'rate') return <RateControl {...props} />
  if (Object.hasOwn(numberRules, field.kind) || field.kind === 'retention-interval')
    return <NumberControl {...props} />
  return <TextControl {...props} />
}

// ---------- page ----------

type Meta = { label: string; detail?: string; prefix?: string }
const meta: Record<string, Meta> = {
  activeThrottleRate: {
    label: 'CPU limit while in use',
    detail:
      'The most processor time backups may use while someone is using this computer.',
  },
  idleThrottleRate: {
    label: 'CPU limit while idle',
    detail: 'The most processor time backups may use while this computer is idle.',
  },
  maintenanceInterval: {
    label: 'Archive maintenance',
    detail:
      'How often CrashPlan checks the stored backup at each destination and removes data that is no longer needed.',
    prefix: 'Every',
  },
  verifyBlocksInterval: {
    label: 'Verify stored data',
    detail:
      'How often CrashPlan re-reads stored backup data to confirm it is complete and undamaged.',
    prefix: 'Every',
  },
  lowBandwidthRate: { label: 'Internet, while in use' },
  highBandwidthRate: { label: 'Internet, while idle' },
  lanLowBandwidthRate: { label: 'Local network, while in use' },
  lanHighBandwidthRate: { label: 'Local network, while idle' },
  networkInterfacesExcluded: {
    label: "Don't back up over these network interfaces",
    detail:
      'Interface names such as eth1. Turn on Regular expression to match several names with one pattern.',
  },
  lanTcpTrafficClass: {
    label: 'Traffic class for local backups',
    detail:
      'A number from 0 to 255 that marks local backup traffic so network equipment can prioritize it. Only change it if your network uses traffic classes.',
  },
  inboundRunWindow: {
    label: 'When other computers may back up here',
    detail:
      'Computers that use this one as a backup destination can only send data during this time.',
  },
  minimumBatteryPercent: {
    label: 'Pause backup below battery level',
    detail: 'Only affects computers that run on battery power.',
  },
  notifyDeliveryTime: {
    label: 'Delivery time',
    detail: 'When during the day CrashPlan sends notification emails.',
  },
  warningEmailEnabled: {
    label: 'Warning emails',
    detail: "Email when this computer hasn't completed a backup for a while.",
  },
  minutesUntilWarning: {
    label: 'Send a warning after',
    detail: 'Time without a completed backup.',
  },
  severeEmailEnabled: {
    label: 'Critical alert emails',
    detail: 'Email when backups have been missing for a long time.',
  },
  minutesUntilSevere: {
    label: 'Send a critical alert after',
    detail: 'Time without a completed backup.',
  },
  backupStatusEmailEnabled: {
    label: 'Backup status reports',
    detail: 'A regular email summary of your backups.',
  },
  backupStatusEmailFreqInMinutes: {
    label: 'Send a report',
    prefix: 'Every',
  },
  backupStatus: {
    label: 'Report format',
    detail: 'The format CrashPlan uses for backup status reports.',
  },
  userIdleDelay: {
    label: 'Consider this computer idle after',
    detail:
      'Time without user activity before the “while idle” limits for processor and upload speed apply.',
  },
  useProxy: {
    label: 'Automatic proxy configuration',
    detail:
      "Use a proxy auto-configuration (PAC) file to find the proxy for CrashPlan's connections.",
  },
  remotePACFileUrl: {
    label: 'PAC file address',
    detail:
      "For example https://proxy.example.com/proxy.pac. Leave empty if you don't use one.",
  },
}
/** Human label of a setting, falling back to the engine's label. */
export function settingLabel(field: Pick<Setting, 'name' | 'label'>): string {
  return meta[field.name]?.label ?? field.label
}

/** Settings that make no sense in this headless image. */
const hidden = new Set(['systrayOnStartup'])

type Pane = {
  id: string
  title: string
  area: SettingsArea
  sections: { title: string; description?: string; fields: string[] }[]
  /** Collects fields of the area that no pane names. */
  rest?: boolean
}
const panes: Pane[] = [
  {
    id: 'backup',
    title: 'Backup',
    area: 'backup',
    rest: true,
    sections: [
      {
        title: 'Processor',
        description: 'How much of the processor CrashPlan may use for backups.',
        fields: ['activeThrottleRate', 'idleThrottleRate'],
      },
      {
        title: 'Maintenance',
        description: 'Regular checks that keep stored backups healthy.',
        fields: ['maintenanceInterval', 'verifyBlocksInterval'],
      },
    ],
  },
  {
    id: 'network',
    title: 'Network',
    area: 'network',
    rest: true,
    sections: [
      {
        title: 'Upload speed',
        description:
          'The fastest CrashPlan may send backups. “Local network” applies to destinations on the same network as this computer.',
        fields: [
          'lowBandwidthRate',
          'highBandwidthRate',
          'lanLowBandwidthRate',
          'lanHighBandwidthRate',
        ],
      },
      {
        title: 'Connections',
        fields: ['networkInterfacesExcluded', 'lanTcpTrafficClass'],
      },
      { title: 'Incoming backups', fields: ['inboundRunWindow'] },
      { title: 'Power', fields: ['minimumBatteryPercent'] },
    ],
  },
  {
    id: 'notifications',
    title: 'Notifications',
    area: 'network',
    sections: [
      {
        title: 'Email alerts',
        description: 'CrashPlan can email you when this computer stops backing up.',
        fields: [
          'warningEmailEnabled',
          'minutesUntilWarning',
          'severeEmailEnabled',
          'minutesUntilSevere',
        ],
      },
      {
        title: 'Status reports',
        fields: [
          'backupStatusEmailEnabled',
          'backupStatusEmailFreqInMinutes',
          'backupStatus',
        ],
      },
      { title: 'Delivery', fields: ['notifyDeliveryTime'] },
    ],
  },
  {
    id: 'general',
    title: 'General',
    area: 'general',
    rest: true,
    sections: [{ title: 'Activity', fields: ['userIdleDelay'] }],
  },
  {
    id: 'proxy',
    title: 'Proxy',
    area: 'peer',
    rest: true,
    sections: [
      {
        title: 'Proxy',
        description:
          'Use these when CrashPlan must reach the internet through a proxy server.',
        fields: ['useProxy', 'remotePACFileUrl'],
      },
    ],
  },
]
const named = new Set(panes.flatMap((p) => p.sections.flatMap((s) => s.fields)))

function SettingRow({
  field,
  value,
  disabled,
  change,
  onValidityChange,
}: {
  field: Setting
  value: unknown
  disabled: boolean
  change: Change
  onValidityChange: Validity
}) {
  const info = meta[field.name]
  const labelled = { ...field, label: settingLabel(field) }
  const title = (
    <span className="settings-title">
      {labelled.label}
      {!field.available ? (
        <Badge>Not supported by this CrashPlan version</Badge>
      ) : field.locked ? (
        <Badge>Managed by your organization</Badge>
      ) : null}
    </span>
  )
  const control = (
    <SettingControl
      field={labelled}
      value={value}
      disabled={disabled || !field.available || field.locked}
      change={change}
      onValidityChange={onValidityChange}
      prefix={info?.prefix}
    />
  )
  if (field.kind === 'window' || field.kind === 'patterns')
    return (
      <div className="row-block setting-row">
        <div className="row-main">
          <div className="row-title">{title}</div>
          {info?.detail && <div className="row-detail">{info.detail}</div>}
        </div>
        {control}
      </div>
    )
  return (
    <div className="row setting-row">
      <div className="row-main">
        <div className="row-title">{title}</div>
        {info?.detail && <div className="row-detail">{info.detail}</div>}
      </div>
      <div className="row-control">{control}</div>
    </div>
  )
}

function SettingsPane({ pane }: { pane: Pane }) {
  const client = useQueryClient()
  const settings = useQuery({
    queryKey: ['settings', pane.area],
    queryFn: () => api.settings(pane.area),
  })
  const [draft, setDraft] = useState<Record<string, unknown>>({})
  const [invalid, setInvalid] = useState<Record<string, boolean>>({})
  const [revision, setRevision] = useState(0)
  const save = useMutation({
    mutationFn: (changes: Record<string, unknown>) =>
      api.saveSettings(pane.area, changes),
    onSuccess: (result, sent) => {
      client.setQueryData(['settings', pane.area], result.settings)
      // Keep only the edits the engine did not confirm, so they can be retried.
      setDraft((old) =>
        Object.fromEntries(
          Object.entries(old).filter(
            ([name]) => Object.hasOwn(sent, name) && result.applied[name] === false,
          ),
        ),
      )
      setInvalid({})
      setRevision((r) => r + 1)
    },
  })

  const fields = settings.data ?? []
  const byName = useMemo(
    () => new Map(fields.map((f) => [f.name, f])),
    [fields],
  )
  const sections = pane.sections
    .map((s) => ({
      ...s,
      fields: s.fields.flatMap((name) => byName.get(name) ?? []),
    }))
    .concat(
      pane.rest
        ? [
            {
              title: 'Other',
              description: undefined,
              fields: fields.filter(
                (f) => !named.has(f.name) && !hidden.has(f.name),
              ),
            },
          ]
        : [],
    )
    .filter((s) => s.fields.length > 0)
  const paneFields = sections.flatMap((s) => s.fields)

  const valueOf = (field: Setting) =>
    Object.hasOwn(draft, field.name) ? draft[field.name] : field.value
  const changes = Object.fromEntries(
    paneFields
      .filter(
        (f) =>
          Object.hasOwn(draft, f.name) && !sameSetting(f, draft[f.name], f.value),
      )
      .map((f) => [f.name, draft[f.name]]),
  )
  const problems = paneFields.filter(
    (f) => invalid[f.name] || settingProblem(f, valueOf(f)) !== undefined,
  )
  const dirty = Object.keys(changes).length > 0
  const edited = Object.keys(draft).length > 0 || problems.length > 0

  const failed =
    save.data && !save.data.complete
      ? Object.entries(save.data.applied)
          .filter(([, ok]) => !ok)
          .map(([name]) => settingLabel(byName.get(name) ?? { name, label: name }))
      : []

  const revert = () => {
    setDraft({})
    setInvalid({})
    setRevision((r) => r + 1)
    save.reset()
  }

  if (settings.isPending) return <Loading label="Loading settings…" />
  if (settings.error && !settings.data) return <ErrorNotice error={settings.error} />

  return (
    <form
      noValidate
      className="settings-pane"
      onSubmit={(e) => {
        e.preventDefault()
        if (dirty && problems.length === 0) save.mutate(changes)
      }}
    >
      <ErrorNotice error={settings.error ?? save.error} />
      {sections.length === 0 && (
        <Notice>There are no settings here for this CrashPlan version.</Notice>
      )}
      <div className="settings-pane" key={revision}>
        {sections.map((section) => (
          <Section
            key={section.title}
            title={section.title}
            description={section.description}
          >
            {section.fields.map((field) => (
              <SettingRow
                key={field.name}
                field={field}
                value={valueOf(field)}
                disabled={save.isPending}
                change={(value) => {
                  if (save.data || save.error) save.reset()
                  setDraft((old) => ({ ...old, [field.name]: value }))
                }}
                onValidityChange={(valid) =>
                  setInvalid((old) =>
                    old[field.name] === !valid
                      ? old
                      : { ...old, [field.name]: !valid },
                  )
                }
              />
            ))}
          </Section>
        ))}
      </div>
      {failed.length > 0 && (
        <Notice tone="error">
          Couldn't save {failed.join(', ')}. CrashPlan did not accept{' '}
          {failed.length === 1 ? 'this change' : 'these changes'}; your edits are
          still shown and were not retried.
        </Notice>
      )}
      {save.data && !save.data.complete && failed.length === 0 && (
        <Notice tone="error">
          CrashPlan did not confirm every change. Check the values shown.
        </Notice>
      )}
      <div className="settings-actions">
        {problems.length > 0 ? (
          <span className="settings-pending">
            Correct the marked {problems.length === 1 ? 'setting' : 'settings'} to
            save.
          </span>
        ) : save.isPending ? (
          <span className="settings-pending">Saving…</span>
        ) : save.data?.complete && !dirty ? (
          <span className="settings-saved" role="status">
            <CheckCircle2 size={14} aria-hidden="true" /> Saved
          </span>
        ) : null}
        <button
          type="button"
          className="btn btn-plain"
          disabled={!edited || save.isPending}
          onClick={revert}
        >
          Revert
        </button>
        <button
          type="submit"
          className="btn btn-primary"
          disabled={!dirty || problems.length > 0 || save.isPending}
        >
          Save
        </button>
      </div>
    </form>
  )
}

export function Settings() {
  const [paneId, setPaneId] = useState('backup')
  const [visited, setVisited] = useState<string[]>(['backup'])
  return (
    <Page
      title="Settings"
      subtitle="How CrashPlan uses this computer and the network, and when it notifies you."
    >
      <div className="settings-tabs">
        <Segmented
          label="Settings category"
          value={paneId}
          options={panes.map((p) => ({ value: p.id, label: p.title }))}
          onChange={(id) => {
            setPaneId(id)
            setVisited((old) => (old.includes(id) ? old : [...old, id]))
          }}
        />
      </div>
      {panes
        .filter((p) => visited.includes(p.id))
        .map((p) => (
          <div key={p.id} hidden={p.id !== paneId}>
            <SettingsPane pane={p} />
          </div>
        ))}
    </Page>
  )
}
