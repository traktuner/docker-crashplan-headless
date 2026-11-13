import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { APIError, api } from '../../shared/api'
import type { Setting, SettingsArea } from '../../shared/api'

type WindowValue = {
  alwaysRun: boolean
  startTimeOfDay: string
  endTimeOfDay: string
  days: string[]
}
type PatternValue = { pattern: string; regularExpression: boolean }
const days = [
  'MONDAY',
  'TUESDAY',
  'WEDNESDAY',
  'THURSDAY',
  'FRIDAY',
  'SATURDAY',
  'SUNDAY',
]

function WindowControl({
  value,
  disabled,
  change,
}: {
  value: unknown
  disabled: boolean
  change: (v: unknown) => void
}) {
  const window = (value ?? {
    alwaysRun: true,
    startTimeOfDay: '00:00',
    endTimeOfDay: '23:59',
    days,
  }) as WindowValue
  return (
    <div className="setting-window">
      <label className="checkbox">
        <input
          type="checkbox"
          checked={window.alwaysRun}
          disabled={disabled}
          onChange={(e) => change({ ...window, alwaysRun: e.target.checked })}
        />{' '}
        Always available
      </label>
      {!window.alwaysRun && (
        <>
          <div className="columns">
            <label>
              From
              <input
                type="time"
                value={window.startTimeOfDay ?? ''}
                disabled={disabled}
                onChange={(e) =>
                  change({ ...window, startTimeOfDay: e.target.value })
                }
              />
            </label>
            <label>
              Until
              <input
                type="time"
                value={window.endTimeOfDay ?? ''}
                disabled={disabled}
                onChange={(e) =>
                  change({ ...window, endTimeOfDay: e.target.value })
                }
              />
            </label>
          </div>
          <div className="setting-days">
            {days.map((day) => (
              <label className="checkbox" key={day}>
                <input
                  type="checkbox"
                  disabled={disabled}
                  checked={window.days?.includes(day) ?? false}
                  onChange={(e) =>
                    change({
                      ...window,
                      days: e.target.checked
                        ? [...(window.days ?? []), day]
                        : window.days.filter((d) => d !== day),
                    })
                  }
                />
                {day.slice(0, 3)}
              </label>
            ))}
          </div>
        </>
      )}
    </div>
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
}) {
  const patterns = (value ?? []) as PatternValue[]
  return (
    <div className="setting-patterns">
      {patterns.map((pattern, index) => (
        <div className="setting-pattern" key={index}>
          <input
            aria-label="Pattern"
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
          <label className="checkbox">
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
            />{' '}
            Regex
          </label>
          <button
            type="button"
            className="secondary"
            disabled={disabled}
            onClick={() => change(patterns.filter((_, i) => i !== index))}
          >
            Remove
          </button>
        </div>
      ))}
      <button
        type="button"
        className="secondary"
        disabled={disabled}
        onClick={() =>
          change([...patterns, { pattern: '', regularExpression: false }])
        }
      >
        Add pattern
      </button>
    </div>
  )
}
export function SettingControl({
  field,
  value,
  disabled,
  change,
}: {
  field: Setting
  value: unknown
  disabled: boolean
  change: (v: unknown) => void
}) {
  if (
    field.kind === 'compression' ||
    field.kind === 'dedup' ||
    field.kind === 'notification'
  )
    return (
      <select
        aria-label={field.label}
        disabled={disabled}
        value={String(value ?? '')}
        onChange={(e) => change(e.target.value)}
      >
        <option value="">Choose a value</option>
        {(field.kind === 'notification'
          ? ['ANY', 'MORNING', 'AFTERNOON', 'EVENING', 'NIGHT', 'NEVER']
          : field.kind === 'compression'
            ? ['ON', 'OFF', 'AUTOMATIC']
            : ['FULL', 'MINIMAL', 'AUTOMATIC']
        ).map((v) => (
          <option key={v} value={v}>
            {v}
          </option>
        ))}
      </select>
    )
  if (field.kind === 'boolean')
    return (
      <input
        aria-label={field.label}
        type="checkbox"
        checked={value === true}
        disabled={disabled}
        onChange={(e) => change(e.target.checked)}
      />
    )
  if (field.kind === 'window')
    return <WindowControl value={value} disabled={disabled} change={change} />
  if (field.kind === 'patterns')
    return <PatternControl value={value} disabled={disabled} change={change} />
  const numeric = [
    'integer',
    'long',
    'cpu',
    'percent',
    'byte',
    'rate',
  ].includes(field.kind)
  return (
    <input
      aria-label={field.label}
      type={field.kind === 'time' ? 'time' : 'text'}
      inputMode={numeric ? 'decimal' : undefined}
      value={value == null ? '' : String(value)}
      disabled={disabled}
      onChange={(e) => change(e.target.value)}
    />
  )
}

const areas: { area: SettingsArea; title: string; description: string }[] = [
  {
    area: 'backup',
    title: 'Backup engine',
    description:
      'CPU limits and archive maintenance intervals. Millisecond values remain exact.',
  },
  {
    area: 'network',
    title: 'Network and notifications',
    description:
      'Transfer rates, network exclusions, inbound availability and email notifications. A rate of 0 uses the native no-limit setting.',
  },
  {
    area: 'general',
    title: 'General',
    description:
      'Native activity preferences. The tray option is retained for configuration compatibility; this image has no desktop tray.',
  },
  {
    area: 'ui',
    title: 'Native session',
    description:
      'Native automatic sign-in, language and path preferences. These do not change the web interface login.',
  },
  {
    area: 'peer',
    title: 'Connection',
    description:
      'Native proxy auto-configuration. Account authority is managed by the account setup flow.',
  },
]
function SettingsForm({ area, title, description }: (typeof areas)[number]) {
  const client = useQueryClient()
  const settings = useQuery({
    queryKey: ['settings', area],
    queryFn: () => api.settings(area),
  })
  const [draft, setDraft] = useState<Record<string, unknown>>({})
  const save = useMutation({
    mutationFn: () => api.saveSettings(area, draft),
    onSuccess: (result) => {
      client.setQueryData(['settings', area], result.settings)
      if (result.complete) setDraft({})
    },
  })
  const error = settings.error ?? save.error
  return (
    <section className="card">
      <h2>{title}</h2>
      <p className="muted">{description}</p>
      {error && (
        <div className="notice error" role="alert">
          {error instanceof APIError
            ? error.message
            : 'Settings are unavailable.'}
        </div>
      )}
      {settings.isPending ? (
        <p role="status">Loading settings…</p>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            save.mutate()
          }}
        >
          <div className="settings-grid">
            {settings.data?.map((field) => (
              <div className="setting-field" key={field.name}>
                <div>
                  <label>
                    {field.label}
                    {field.unit ? ' (' + field.unit + ')' : ''}
                  </label>
                  {!field.available ? (
                    <span className="muted">
                      Unavailable in this engine version
                    </span>
                  ) : field.locked ? (
                    <span className="badge">Locked by native policy</span>
                  ) : null}
                </div>
                <SettingControl
                  field={field}
                  value={
                    Object.hasOwn(draft, field.name)
                      ? draft[field.name]
                      : field.value
                  }
                  disabled={!field.available || field.locked || save.isPending}
                  change={(value) =>
                    setDraft((old) => ({ ...old, [field.name]: value }))
                  }
                />
              </div>
            ))}
          </div>
          <button disabled={Object.keys(draft).length === 0 || save.isPending}>
            Save changes
          </button>
        </form>
      )}
      {save.data && (
        <div
          className={'notice ' + (save.data.complete ? 'success' : 'error')}
          role="status"
        >
          {save.data.complete
            ? 'Changes were confirmed by the native engine.'
            : 'Not applied: ' +
              Object.entries(save.data.applied)
                .filter(([, ok]) => !ok)
                .map(([name]) => name)
                .join(', ') +
              '. Current native values are shown; no automatic retry occurred.'}
        </div>
      )}
    </section>
  )
}
export function Settings() {
  const [area, setArea] = useState<SettingsArea>('backup')
  const selected = areas.find((a) => a.area === area)!
  return (
    <>
      <header className="page-title">
        <h1>Settings</h1>
        <p className="muted">
          Native settings with policy locks and verified write results.
        </p>
      </header>
      <div className="restore-paging">
        {areas.map((item) => (
          <button
            className={area === item.area ? '' : 'secondary'}
            key={item.area}
            onClick={() => setArea(item.area)}
          >
            {item.title}
          </button>
        ))}
      </div>
      <SettingsForm key={area} {...selected} />
    </>
  )
}
