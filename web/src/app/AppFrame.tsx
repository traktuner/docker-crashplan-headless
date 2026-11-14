import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import {
  FolderClosed,
  History,
  LayoutDashboard,
  LogOut,
  Menu,
  Activity as ActivityIcon,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  UserRound,
  X,
} from 'lucide-react'
import { findPages, pages } from './navigation'
import type { Page } from './navigation'
import { ProjectNotice } from '../shared/ProjectNotice'
import { ThemeSwitch } from '../shared/ThemeSwitch'

const icons = {
  overview: LayoutDashboard,
  sets: FolderClosed,
  restore: History,
  activity: ActivityIcon,
  settings: Settings2,
  security: ShieldCheck,
  account: UserRound,
  system: Server,
}

export function AppFrame({
  page,
  navigate,
  logout,
  signedIn,
  children,
}: {
  page: Page
  navigate: (page: Page) => void
  logout: () => void
  signedIn?: boolean
  children: ReactNode
}) {
  // Pages that work before the CrashPlan account is signed in.
  const locked = (id: Page) =>
    signedIn === false && id !== 'account' && id !== 'system'
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const command = useRef<HTMLDialogElement>(null)
  const sidebar = useRef<HTMLElement>(null)
  const menu = useRef<HTMLButtonElement>(null)
  const [mobile, setMobile] = useState(
    () => matchMedia('(max-width: 720px)').matches,
  )
  useEffect(() => {
    const media = matchMedia('(max-width: 720px)')
    const change = () => setMobile(media.matches)
    media.addEventListener('change', change)
    return () => media.removeEventListener('change', change)
  }, [])
  useEffect(() => {
    if (!mobile || !open) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    sidebar.current?.querySelector<HTMLButtonElement>('button')?.focus()
    const keys = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        setOpen(false)
        requestAnimationFrame(() => menu.current?.focus())
      }
    }
    window.addEventListener('keydown', keys)
    return () => {
      document.body.style.overflow = previous
      window.removeEventListener('keydown', keys)
    }
  }, [mobile, open])
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        if (command.current?.open) command.current.close()
        else command.current?.showModal()
      }
    }
    window.addEventListener('keydown', shortcut)
    return () => window.removeEventListener('keydown', shortcut)
  }, [])
  function go(next: Page) {
    setOpen(false)
    command.current?.close()
    setSearch('')
    navigate(next)
  }
  const current = pages.find((p) => p.id === page)!
  return (
    <div className="shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      {open && mobile && (
        <button
          className="nav-scrim"
          aria-label="Close navigation"
          onClick={() => setOpen(false)}
        />
      )}
      <aside
        ref={sidebar}
        inert={mobile && !open}
        role={mobile && open ? 'dialog' : undefined}
        aria-modal={mobile && open ? true : undefined}
        className={open ? 'sidebar is-open' : 'sidebar'}
        aria-label="Navigation"
      >
        <div style={{ display: 'flex', alignItems: 'center' }}>
          <a
            className="brand"
            href="#overview"
            onClick={(e) => {
              e.preventDefault()
              go('overview')
            }}
          >
            <span className="brand-mark">
              <ShieldCheck size={18} strokeWidth={2} />
            </span>
            <span>
              CrashPlan
              <small>Headless</small>
            </span>
          </a>
          <button
            className="icon-btn mobile-only"
            style={{ marginLeft: 'auto' }}
            aria-label="Close navigation"
            onClick={() => setOpen(false)}
          >
            <X size={18} />
          </button>
        </div>
        <nav aria-label="Main">
          {(['Backup', 'Configuration'] as const).map((group) => (
            <div className="nav-group" key={group}>
              <p className="nav-label">{group}</p>
              {pages
                .filter((p) => p.group === group)
                .map((item) => {
                  const Icon = icons[item.id]
                  return (
                    <button
                      key={item.id}
                      className="nav-item"
                      aria-current={page === item.id ? 'page' : undefined}
                      disabled={locked(item.id)}
                      aria-describedby={locked(item.id) ? 'nav-locked-hint' : undefined}
                      onClick={() => go(item.id)}
                    >
                      <Icon size={16} strokeWidth={2} />
                      {item.label}
                    </button>
                  )
                })}
            </div>
          ))}
        </nav>
        {signedIn === false && (
          <p className="nav-hint" id="nav-locked-hint">
            Sign in to CrashPlan to open the other pages.
          </p>
        )}
        <div className="sidebar-bottom">
          {signedIn !== undefined && (
            <button className="account-chip" onClick={() => go('account')}>
              <span className={'dot ' + (signedIn ? 'green' : 'orange')} />
              {signedIn ? 'Signed in to CrashPlan' : 'Not signed in to CrashPlan'}
            </button>
          )}
          <button
            className="account-chip"
            onClick={logout}
            title="Ends this browser session. CrashPlan and backups keep running."
          >
            <LogOut size={15} />
            Sign out of web interface
          </button>
        </div>
      </aside>
      <div className="app-body" inert={mobile && open}>
        <header className="topbar">
          <button
            ref={menu}
            className="icon-btn mobile-only"
            aria-label="Open navigation"
            aria-expanded={open}
            onClick={() => setOpen(!open)}
          >
            <Menu size={19} />
          </button>
          <span className="topbar-title">{current.label}</span>
          <div className="topbar-actions">
            <button
              className="search-trigger desktop-only"
              aria-label="Go to page"
              onClick={() => command.current?.showModal()}
            >
              <Search size={14} />
              Go to page
              <kbd>⌘K</kbd>
            </button>
            <ThemeSwitch />
          </div>
        </header>
        <main className="workspace" id="main-content" tabIndex={-1}>
          {children}
        </main>
        <ProjectNotice />
      </div>
      <dialog
        className="dialog"
        ref={command}
        aria-label="Go to page"
        onClick={(e) => {
          if (e.target === command.current) command.current?.close()
        }}
      >
        <div className="command-input">
          <Search size={18} />
          <input
            aria-label="Page name"
            placeholder="Go to page"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            autoFocus
          />
        </div>
        <div className="command-results">
          {findPages(search).map((item) => {
            const Icon = icons[item.id]
            return (
              <button
                key={item.id}
                className="nav-item"
                disabled={locked(item.id)}
                onClick={() => go(item.id)}
              >
                <Icon size={16} />
                {item.label}
              </button>
            )
          })}
          {findPages(search).length === 0 && (
            <p className="row-detail" style={{ padding: 12 }}>
              No page matches.
            </p>
          )}
        </div>
      </dialog>
    </div>
  )
}
