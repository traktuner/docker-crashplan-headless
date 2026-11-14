// Form controls for backup-set fields that the engine stores in native
// units. Each control edits in human units and reports the native value, or
// undefined while the input is not a valid value yet. The visible parts are
// the shared controls that Settings uses as well.
import { useState } from 'react'
import { durationUnits, joinDuration, splitDuration } from '../../shared/format'
import type { DurationUnit } from '../../shared/format'
import { DurationFields, InlineError, PatternList, WindowFields } from '../../shared/controls'
import type { Pattern } from '../../shared/controls'
import { byteUnits, joinBytes, splitBytes } from './presentation'
import type { ByteUnit, RunWindow } from './presentation'
import './backup.css'

export { InlineError }
export type { Pattern }

const allUnits = durationUnits.map((u) => u.id)

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
  return (
    <DurationFields
      amount={state.amount}
      unit={state.unit}
      units={allUnits}
      label={label}
      disabled={disabled}
      onChange={(next: { amount: string; unit: DurationUnit }) => {
        setState(next)
        const ms = joinDuration(next.amount, next.unit)
        change(ms !== undefined && Number(ms) > 0 ? ms : undefined)
      }}
    />
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
        className="select unit-select"
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

/** The backup schedule of a set, as one block in its section. */
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
  return (
    <div className="row-block">
      <div className="row-main">
        <div className="row-title">Back up</div>
        {locked && <div className="row-detail">Managed by your organization</div>}
      </div>
      <WindowFields value={value} disabled={disabled} label="When to back up" change={change} />
    </div>
  )
}

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
  return (
    <div className="row-block">
      <PatternList
        patterns={patterns}
        disabled={disabled}
        showProblems={showProblems}
        label="Exclusion"
        empty="No exclusions of your own yet."
        add="Add exclusion"
        placeholder={(regex) => (regex ? '(?i).*\\.tmp$' : '*.tmp')}
        change={change}
      />
    </div>
  )
}
