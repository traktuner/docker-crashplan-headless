import { useState } from 'react'
import { Monitor, Moon, Sun } from 'lucide-react'

export type Theme = 'system' | 'light' | 'dark'
const key = 'cph-theme'

// The page attribute is the source of truth; storage can be unavailable.
export function currentTheme(): Theme {
  const value = document.documentElement.dataset.theme
  return value === 'light' || value === 'dark' ? value : 'system'
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
  const [theme, setTheme] = useState<Theme>(currentTheme)
  return (
    <div className="theme-switch" role="group" aria-label="Color theme">
      {options.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          aria-pressed={theme === value}
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
