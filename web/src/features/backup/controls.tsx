// Form controls for backup-set fields that the engine stores in native
// units. Each control edits in human units and reports the native value, or
// undefined while the input is not a valid value yet.
import { useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { Segmented } from '../../shared/components'
import { durationUnits, joinDuration, splitDuration } from '../../shared/format'
import type { DurationUnit } from '../../shared/format'
import { byteUnits, joinBytes, splitBytes, weekDays, windowProblem } from './presentation'
import type { ByteUnit, RunWindow } from './presentation'
import './backup.css'

export function InlineError({ children }: { children?: string }) {
  return children ? (
    <span className="inline-error" role="alert">
      {children}
    </span>
  ) : null
}

export function DurationInput({
  value,
  label,
  disabled,
  change,
}: {
  value: unknown
  label: string
  disabled?: boolean
  change: (ms: string | undefined) => void
}) {
  const [state, setState] = useState(() => splitDuration(value))
  const update = (next: { amount: string; unit: DurationUnit }) => {
    setState(next)
    const ms = joinDuration(next.amount, next.unit)
    change(ms !== undefined && Number(ms) > 0 ? ms : undefined)
  }
  return (
    <div className="input-group">
      <input
        className="input amount-input"
        inputMode="decimal"
        aria-label={label + ' amount'}
        value={state.amount}
        disabled={disabled}
        onChange={(e) => update({ ...state, amount: e.target.value })}
      />
      <select
        className="select"
        aria-label={label + ' unit'}
        value={state.unit}
        disabled={disabled}
        onChange={(e) => update({ ...state, unit: e.target.value as DurationUnit })}
      >
        {durationUnits.map((u) => (
          <option key={u.id} value={u.id}>
            {u.label}
          </option>
        ))}
      </select>
    </div>
  )
}

export function ByteInput({
  value,
  label,
  disabled,
  change,
}: {
  value: unknown
  label: string
  disabled?: boolean
  change: (bytes: string | undefined) => void
}) {
  const [state, setState] = useState(() => splitBytes(value))
  const update = (next: { amount: string; unit: ByteUnit }) => {
    setState(next)
    change(joinBytes(next.amount, next.unit))
  }
  return (
    <div className="input-group">
      <input
        className="input amount-input"
        inputMode="decimal"
        aria-label={label + ' amount'}
        value={state.amount}
        disabled={disabled}
        onChange={(e) => update({ ...state, amount: e.target.value })}
      />
      <select
        className="select"
        aria-label={label + ' unit'}
        value={state.unit}
        disabled={disabled}
        onChange={(e) => update({ ...state, unit: e.target.value as ByteUnit })}
      >
        {byteUnits.map((u) => (
          <option key={u.id} value={u.id}>
            {u.id}
          </option>
        ))}
      </select>
    </div>
  )
}

/**
 * One clear choice: back up at any time, or only inside a window. The time
 * and day controls exist only for the second choice, so an always-on window
 * never shows times that do not apply.
 */
export function WindowRows({
  value,
  disabled,
  locked,
  change,
}: {
  value: RunWindow
  disabled: boolean
  locked: boolean
  change: (value: RunWindow) => void
}) {
  const problem = windowProblem(value)
  return (
    <>
      <div className="row">
        <div className="row-main">
          <div className="row-title">Back up</div>
          {locked && <div className="row-detail">Locked by policy</div>}
        </div>
        <div className="row-control">
          <fieldset className="bare" disabled={disabled}>
            <Segmented
              label="When to back up"
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
          </fieldset>
        </div>
      </div>
      {!value.alwaysRun && (
        <>
          <div className="row">
            <div className="row-main">
              <div className="row-title">Between</div>
              <div className="row-detail">
                An end time earlier than the start time runs past midnight.
              </div>
            </div>
            <div className="row-control time-range">
              <input
                className="input"
                type="time"
                aria-label="From"
                value={value.startTimeOfDay}
                disabled={disabled}
                onChange={(e) => change({ ...value, startTimeOfDay: e.target.value })}
              />
              <span className="muted">and</span>
              <input
                className="input"
                type="time"
                aria-label="Until"
                value={value.endTimeOfDay}
                disabled={disabled}
                onChange={(e) => change({ ...value, endTimeOfDay: e.target.value })}
              />
            </div>
          </div>
          <div className="row">
            <div className="row-main">
              <div className="row-title">On</div>
              <InlineError>{problem}</InlineError>
            </div>
            <div className="row-control">
              <fieldset className="bare" disabled={disabled}>
                <div className="segmented" role="group" aria-label="Days">
                  {weekDays.map((day) => {
                    const on = value.days.includes(day.id)
                    return (
                      <button
                        key={day.id}
                        type="button"
                        title={day.label}
                        aria-label={day.label}
                        aria-pressed={on}
                        onClick={() =>
                          change({
                            ...value,
                            days: on
                              ? value.days.filter((d) => d !== day.id)
                              : weekDays.map((d) => d.id).filter((d) => d === day.id || value.days.includes(d)),
                          })
                        }
                      >
                        {day.short}
                      </button>
                    )
                  })}
                </div>
              </fieldset>
            </div>
          </div>
        </>
      )}
    </>
  )
}

export type Pattern = { pattern: string; regularExpression: boolean }

export function PatternRows({
  patterns,
  disabled,
  showProblems,
  change,
}: {
  patterns: Pattern[]
  disabled: boolean
  showProblems: boolean
  change: (patterns: Pattern[]) => void
}) {
  const update = (index: number, patch: Partial<Pattern>) =>
    change(patterns.map((p, i) => (i === index ? { ...p, ...patch } : p)))
  return (
    <>
      {patterns.length === 0 && (
        <div className="row">
          <div className="row-main">
            <div className="row-detail">No exclusions of your own yet.</div>
          </div>
        </div>
      )}
      {patterns.map((pattern, index) => (
        <div className="row pattern-row" key={index}>
          <div className="row-main">
            <input
              className="input"
              aria-label={'Exclusion ' + (index + 1)}
              placeholder={pattern.regularExpression ? '(?i).*\\.tmp$' : '*.tmp'}
              value={pattern.pattern}
              disabled={disabled}
              aria-invalid={showProblems && !pattern.pattern.trim() ? true : undefined}
              onChange={(e) => update(index, { pattern: e.target.value })}
            />
            {showProblems && !pattern.pattern.trim() && (
              <InlineError>Enter a pattern or remove this row.</InlineError>
            )}
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
            title="Remove exclusion"
            aria-label={'Remove exclusion ' + (index + 1)}
            disabled={disabled}
            onClick={() => change(patterns.filter((_, i) => i !== index))}
          >
            <Trash2 size={15} />
          </button>
        </div>
      ))}
      <div className="row">
        <button
          type="button"
          className="btn btn-plain btn-sm"
          disabled={disabled}
          onClick={() => change([...patterns, { pattern: '', regularExpression: false }])}
        >
          <Plus size={14} /> Add exclusion
        </button>
      </div>
    </>
  )
}
