import { useState } from 'react'
import { Monitor, Moon, Sun } from 'lucide-react'

export type Theme = 'system' | 'light' | 'dark'
const key = 'cph-theme'

export function savedTheme(): Theme {
  try {
    const value = localStorage.getItem(key)
    return value === 'light' || value === 'dark' ? value : 'system'
  } catch {
    return 'system'
  }
}

// 'system' leaves the choice to the operating system through CSS color-scheme.
export function applyTheme(theme: Theme) {
  const root = document.documentElement
  if (theme === 'system') delete root.dataset.theme
  else root.dataset.theme = theme
  try {
    if (theme === 'system') localStorage.removeItem(key)
    else localStorage.setItem(key, theme)
  } catch {
    // The choice then lasts for this page only.
  }
}

const options = [
  { value: 'system', label: 'Use system setting', Icon: Monitor },
  { value: 'light', label: 'Light', Icon: Sun },
  { value: 'dark', label: 'Dark', Icon: Moon },
] as const

export function ThemeSwitch() {
  const [theme, setTheme] = useState<Theme>(savedTheme)
  return (
    <div className="theme-switch" role="radiogroup" aria-label="Color theme">
      {options.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={theme === value}
          aria-label={label}
          title={label}
          className={theme === value ? 'is-active' : ''}
          onClick={() => {
            applyTheme(value)
            setTheme(value)
          }}
        >
          <Icon size={15} strokeWidth={1.9} />
        </button>
      ))}
    </div>
  )
}
