import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import {
  ArrowUpRight,
  ChevronRight,
  FolderClosed,
  History,
  LayoutDashboard,
  LogOut,
  Menu,
  Radio,
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
  activity: Radio,
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
  const locked = (id: Page) => signedIn === false && id !== 'account' && id !== 'system'
  const [open, setOpen] = useState(false),
    [search, setSearch] = useState('')
  const command = useRef<HTMLDialogElement>(null)
  const sidebar = useRef<HTMLElement>(null),
    menu = useRef<HTMLButtonElement>(null)
  const [mobile, setMobile] = useState(
    () => matchMedia('(max-width: 640px)').matches,
  )
  useEffect(() => {
    const media = matchMedia('(max-width: 640px)')
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
      if (event.key === 'Tab') {
        const items = [
          ...(sidebar.current?.querySelectorAll<HTMLElement>(
            'button:not(:disabled),a[href]',
          ) ?? []),
        ].filter((item) => item.getClientRects().length)
        const first = items[0],
          last = items.at(-1)
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault()
          last?.focus()
        }
        if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault()
          first?.focus()
        }
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
      {open && (
        <button
          className="nav-scrim"
          aria-label="Close navigation"
          onClick={() => {
            setOpen(false)
            requestAnimationFrame(() => menu.current?.focus())
          }}
        />
      )}
      <aside
        ref={sidebar}
        inert={mobile && !open}
        role={mobile && open ? 'dialog' : undefined}
        aria-modal={mobile && open ? true : undefined}
        className={open ? 'sidebar is-open' : 'sidebar'}
        aria-label="Application sidebar"
      >
        <a
          className="brand"
          href="#overview"
          onClick={(e) => {
            e.preventDefault()
            go('overview')
          }}
        >
          <span className="brand-symbol">
            <ShieldCheck size={24} strokeWidth={1.8} />
          </span>
          <span>
            CrashPlan<small>HEADLESS</small>
          </span>
        </a>
        <button
          className="icon-button close-navigation"
          aria-label="Close navigation"
          onClick={() => {
            setOpen(false)
            requestAnimationFrame(() => menu.current?.focus())
          }}
        >
          <X size={20} />
        </button>
        <div className="workspace-switch">
          <span className="workspace-avatar">CP</span>
          <div>
            <strong>Backup service</strong>
            <span>Management console</span>
          </div>
        </div>
        <nav aria-label="Main navigation">
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
                      className={page === item.id ? 'active' : ''}
                      aria-current={page === item.id ? 'page' : undefined}
                      disabled={locked(item.id)}
                      aria-describedby={locked(item.id) ? 'nav-locked-hint' : undefined}
                      onClick={() => go(item.id)}
                    >
                      <Icon size={18} strokeWidth={1.8} />
                      {item.label}
                      {page === item.id && <span className="nav-active-dot" />}
                    </button>
                  )
                })}
            </div>
          ))}
        </nav>
        {signedIn === false && (
          <p className="nav-locked-hint" id="nav-locked-hint">
            Sign in to CrashPlan to open the other pages.
          </p>
        )}
        <div className="sidebar-bottom">
          <div className="sidebar-theme">
            <ThemeSwitch />
          </div>
          {signedIn !== undefined && (
            <button
              className={'account-state ' + (signedIn ? 'is-signed-in' : 'is-signed-out')}
              onClick={() => go('account')}
            >
              <span aria-hidden="true" />
              {signedIn ? 'CrashPlan signed in' : 'CrashPlan not signed in'}
            </button>
          )}
          <button
            className="signout-button"
            onClick={logout}
            title="Ends this browser session. CrashPlan and backups keep running."
          >
            <LogOut size={17} />
            Leave web interface
          </button>
        </div>
      </aside>
      <div className="app-body" inert={mobile && open}>
        <header className="topbar">
          <div className="topbar-location">
            <button
              ref={menu}
              className="icon-button mobile-menu"
              aria-label="Open navigation"
              aria-expanded={open}
              onClick={() => setOpen(!open)}
            >
              <Menu size={21} />
            </button>
            <span>CrashPlan</span>
            <ChevronRight size={14} />
            <strong>{current.label}</strong>
          </div>
          <div className="topbar-actions">
            <div className="topbar-theme">
              <ThemeSwitch />
            </div>
            <button
              className="navigation-search"
              aria-label="Find a page"
              onClick={() => command.current?.showModal()}
            >
              <Search size={16} />
              <span>Find a page</span>
              <kbd>⌘ K</kbd>
            </button>
            {page !== 'restore' && signedIn !== false && (
              <button
                className="topbar-restore secondary"
                onClick={() => go('restore')}
              >
                <History size={16} />
                Restore files
                <ArrowUpRight size={14} />
              </button>
            )}
          </div>
        </header>
        <main className="workspace" id="main-content" tabIndex={-1}>
          {children}
        </main>
        <ProjectNotice />
      </div>
      <dialog className="command-dialog" ref={command}>
        <div className="command-search">
          <Search size={20} />
          <input
            aria-label="Find a page"
            placeholder="Search pages"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            autoFocus
          />
          <button
            className="key-button"
            onClick={() => command.current?.close()}
          >
            Esc
          </button>
        </div>
        <p className="command-caption">QUICK NAVIGATION</p>
        <div className="command-results">
          {findPages(search).map((item) => {
            const Icon = icons[item.id]
            return (
              <button key={item.id} disabled={locked(item.id)} onClick={() => go(item.id)}>
                <Icon size={18} />
                <span>{item.label}</span>
                <ChevronRight size={16} />
              </button>
            )
          })}
          {findPages(search).length === 0 && (
            <p className="empty-copy">
              No pages match. Try “files”, “restore” or “settings”.
            </p>
          )}
        </div>
      </dialog>
    </div>
  )
}
