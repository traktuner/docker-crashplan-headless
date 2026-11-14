// Editing controls shared by Settings and Backup sets, so a schedule, a list
// of patterns, a duration or a Save bar look and behave the same everywhere.
import { useId } from 'react'
import type { ReactNode } from 'react'
import { CheckCircle2, Plus, Trash2 } from 'lucide-react'
import { Segmented } from './components'
import type { DurationUnit } from './format'
import { weekDays, windowProblem } from '../features/backup/presentation'
import type { RunWindow } from '../features/backup/presentation'

export function InlineError({ id, children }: { id?: string; children?: string }) {
  return children ? (
    <span className="inline-error" id={id} role="alert">
      {children}
    </span>
  ) : null
}

const unitWords: Record<DurationUnit, string> = {
  seconds: 'second',
  minutes: 'minute',
  hours: 'hour',
  days: 'day',
}
/** "1 day", "2 days": the unit menu follows the typed amount. */
export function unitLabel(unit: DurationUnit, amount: string) {
  return unitWords[unit] + (amount.trim() === '1' ? '' : 's')
}

/** Amount and unit, e.g. "7" + "days". Purely presentational. */
export function DurationFields({
  amount,
  unit,
  units,
  label,
  disabled,
  invalid,
  describedBy,
  prefix,
  onChange,
}: {
  amount: string
  unit: DurationUnit
  units: readonly DurationUnit[]
  label: string
  disabled?: boolean
  invalid?: boolean
  describedBy?: string
  prefix?: string
  onChange: (next: { amount: string; unit: DurationUnit }) => void
}) {
  return (
    <div className="input-group">
      {prefix && <span className="input-affix">{prefix}</span>}
      <input
        className="input amount-input"
        inputMode="decimal"
        aria-label={label}
        aria-invalid={invalid ? true : undefined}
        aria-describedby={describedBy}
        value={amount}
        disabled={disabled}
        onChange={(e) => onChange({ amount: e.target.value, unit })}
      />
      <select
        className="select unit-select"
        aria-label={label + ' unit'}
        value={unit}
        disabled={disabled}
        onChange={(e) => onChange({ amount, unit: e.target.value as DurationUnit })}
      >
        {units.map((u) => (
          <option key={u} value={u}>
            {unitLabel(u, amount)}
          </option>
        ))}
      </select>
    </div>
  )
}

/** Weekday pills that wrap on narrow screens. */
export function DayPicker({
  days,
  disabled,
  label,
  onChange,
}: {
  days: string[]
  disabled?: boolean
  label: string
  onChange: (days: string[]) => void
}) {
  return (
    <div className="day-picker" role="group" aria-label={label}>
      {weekDays.map((day) => {
        const on = days.includes(day.id)
        return (
          <button
            key={day.id}
            type="button"
            className="day-pill"
            aria-label={day.label}
            aria-pressed={on}
            disabled={disabled}
            onClick={() =>
              onChange(
                on
                  ? days.filter((d) => d !== day.id)
                  : weekDays.map((d) => d.id as string).filter((d) => d === day.id || days.includes(d)),
              )
            }
          >
            {day.short}
          </button>
        )
      })}
    </div>
  )
}

/**
 * One schedule editor: always, or only inside a window. Times and days exist
 * only for the second choice, so an always-on window never shows times that
 * do not apply.
 */
export function WindowFields({
  value,
  disabled,
  label,
  change,
}: {
  value: RunWindow
  disabled?: boolean
  label: string
  change: (value: RunWindow) => void
}) {
  const problem = windowProblem(value)
  const errorId = useId()
  return (
    <div className="window-fields">
      <Segmented
        label={label}
        disabled={disabled}
        value={value.alwaysRun ? 'always' : 'window'}
        options={[
          { value: 'always', label: 'Always' },
          { value: 'window', label: 'Only at certain times' },
        ]}
        onChange={(choice) =>
          change(
            choice === 'always'
              ? { ...value, alwaysRun: true }
              : {
                  alwaysRun: false,
                  startTimeOfDay: value.startTimeOfDay || '01:00',
                  endTimeOfDay: value.endTimeOfDay || '06:00',
                  days: value.days.length ? value.days : weekDays.map((d) => d.id),
                },
          )
        }
      />
      {!value.alwaysRun && (
        <>
          <div className="time-range">
            <span className="input-affix">From</span>
            <input
              className="input"
              type="time"
              aria-label={label + ': from'}
              value={value.startTimeOfDay}
              disabled={disabled}
              onChange={(e) => change({ ...value, startTimeOfDay: e.target.value })}
            />
            <span className="input-affix">to</span>
            <input
              className="input"
              type="time"
              aria-label={label + ': until'}
              value={value.endTimeOfDay}
              disabled={disabled}
              onChange={(e) => change({ ...value, endTimeOfDay: e.target.value })}
            />
          </div>
          <DayPicker
            label={label + ': days'}
            days={value.days}
            disabled={disabled}
            onChange={(days) => change({ ...value, days })}
          />
          <p className="field-hint">An end time earlier than the start time runs past midnight.</p>
          <InlineError id={errorId}>{problem}</InlineError>
        </>
      )}
    </div>
  )
}

export type Pattern = { pattern: string; regularExpression: boolean }

/** An editable list of patterns, each optionally a regular expression. */
export function PatternList({
  patterns,
  disabled,
  showProblems,
  label = 'Pattern',
  empty = 'No patterns yet.',
  add = 'Add pattern',
  placeholder,
  change,
}: {
  patterns: Pattern[]
  disabled?: boolean
  showProblems?: boolean
  label?: string
  empty?: string
  add?: string
  placeholder?: (regularExpression: boolean) => string
  change: (patterns: Pattern[]) => void
}) {
  const update = (index: number, patch: Partial<Pattern>) =>
    change(patterns.map((p, i) => (i === index ? { ...p, ...patch } : p)))
  return (
    <div className="pattern-list">
      {patterns.length === 0 && <p className="field-hint">{empty}</p>}
      {patterns.map((pattern, index) => {
        const missing = !!showProblems && !pattern.pattern.trim()
        return (
          <div className="pattern-line" key={index}>
            <div className="pattern-input">
              <input
                className="input"
                aria-label={`${label} ${index + 1}`}
                placeholder={placeholder?.(pattern.regularExpression)}
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                value={pattern.pattern}
                disabled={disabled}
                aria-invalid={missing ? true : undefined}
                onChange={(e) => update(index, { pattern: e.target.value })}
              />
              {missing && <InlineError>Enter a pattern or remove this line.</InlineError>}
            </div>
            <label className="check">
              <input
                type="checkbox"
                disabled={disabled}
                checked={pattern.regularExpression}
                onChange={(e) => update(index, { regularExpression: e.target.checked })}
              />
              Regular expression
            </label>
            <button
              type="button"
              className="icon-btn"
              aria-label={`Remove ${label.toLowerCase()} ${index + 1}`}
              disabled={disabled}
              onClick={() => change(patterns.filter((_, i) => i !== index))}
            >
              <Trash2 size={15} />
            </button>
          </div>
        )
      })}
      <div>
        <button
          type="button"
          className="btn btn-plain btn-sm"
          disabled={disabled}
          onClick={() => change([...patterns, { pattern: '', regularExpression: false }])}
        >
          <Plus size={14} aria-hidden="true" /> {add}
        </button>
      </div>
    </div>
  )
}

/**
 * Revert and Save for a form. It sticks to the bottom of the screen while
 * there are unsaved changes, so Save stays in reach on long pages.
 */
export function SaveBar({
  dirty,
  busy,
  blocked,
  status,
  saved,
  saveLabel = 'Save',
  revertLabel = 'Revert',
  canRevert,
  onRevert,
  onSave,
}: {
  dirty: boolean
  busy?: boolean
  /** Text explaining why Save is not possible yet. */
  blocked?: string
  status?: ReactNode
  saved?: boolean
  saveLabel?: string
  revertLabel?: string
  canRevert?: boolean
  onRevert: () => void
  /** Without it the Save button submits the surrounding form. */
  onSave?: () => void
}) {
  return (
    <div className={'save-bar' + (dirty ? ' is-dirty' : '')}>
      <span className="save-bar-status" role="status">
        {blocked ? (
          blocked
        ) : busy ? (
          'Saving…'
        ) : status ? (
          status
        ) : saved && !dirty ? (
          <span className="saved">
            <CheckCircle2 size={14} aria-hidden="true" /> Saved
          </span>
        ) : dirty ? (
          'Unsaved changes'
        ) : null}
      </span>
      <button
        type="button"
        className="btn"
        disabled={!(canRevert ?? dirty) || busy}
        onClick={onRevert}
      >
        {revertLabel}
      </button>
      <button
        type={onSave ? 'button' : 'submit'}
        className="btn btn-primary"
        disabled={!dirty || !!blocked || busy}
        onClick={onSave}
      >
        {busy ? 'Saving…' : saveLabel}
      </button>
    </div>
  )
}
