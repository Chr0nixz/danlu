import { useQuery } from '@tanstack/react-query'
import { Activity, Flame, LayoutDashboard, ListChecks, Plus, Settings, Terminal as TerminalIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { api } from '../lib/api'
import { useActiveJobs, useRecentFailures } from '../lib/queries'
import { PageBoundary } from './ErrorBoundary'
import { ShortcutDialog } from './Shortcuts'
import { Announcer, buttonClass, cx, Kbd } from './ui'

const nav = [
  { to: '/', label: '总览', icon: LayoutDashboard, end: true },
  { to: '/resources', label: '资源', icon: Activity, end: false },
  { to: '/jobs', label: '任务', icon: ListChecks, end: false },
  { to: '/terminal', label: '终端', icon: TerminalIcon, end: false },
  { to: '/settings', label: '设置', icon: Settings, end: false },
]
const homeNav = nav.find((n) => n.to === '/')!
const jobsNav = nav.find((n) => n.to === '/jobs')!

export function Brand({ className }: { className?: string }) {
  return (
    <span className={cx('grid size-6 shrink-0 place-items-center rounded-md bg-brand text-white', className)}>
      <Flame className="size-3.5" strokeWidth={2.25} />
    </span>
  )
}

const isTyping = (t: EventTarget | null) =>
  t instanceof HTMLElement && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName))

export function Layout() {
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const me = useQuery({ queryKey: ['me'], queryFn: api.me, staleTime: Infinity })
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, staleTime: 60_000 })
  const showTerminal = settings.data?.terminal_enabled === true

  // 快捷键：N 新建任务；/ 搜索任务（任务页自己处理，其他页面先跳过去）；J / K 在任务行之间移动，
  // 焦点落在行的链接上，Enter 打开；? 打开快捷键一览。确认框开着时都不响应
  const [help, setHelp] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target) || document.querySelector('dialog[open]')) return
      const key = e.key.toLowerCase()
      if (key === 'n') {
        e.preventDefault()
        navigate('/new')
      } else if (e.key === '/' && pathname !== '/jobs') {
        e.preventDefault()
        navigate('/jobs', { state: { focusSearch: true } })
      } else if (key === 'j' || key === 'k') {
        const rows = [...document.querySelectorAll<HTMLElement>('[data-job-link]')]
        if (!rows.length) return
        e.preventDefault()
        const i = rows.indexOf(document.activeElement as HTMLElement)
        rows[key === 'j' ? Math.min(rows.length - 1, i + 1) : Math.max(0, i - 1)].focus()
      } else if (e.key === '?') {
        e.preventDefault()
        setHelp(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [navigate, pathname])

  // 标签页标题带上运行和失败数，和 IDE 并排开着时不用切过来也能看到
  const active = useActiveJobs()
  const failed = useRecentFailures().data ?? 0
  const running = active.data?.jobs.filter((j) => j.status === 'running').length ?? 0
  useEffect(() => {
    const parts = [running && `${running} 运行`, failed && `${failed} 失败`].filter(Boolean)
    const base = me.data?.hostname ? `丹炉 · ${me.data.hostname}` : '丹炉 Danlu'
    document.title = parts.length ? `${parts.join(' · ')} — ${base}` : base
  }, [running, failed, me.data?.hostname])

  return (
    <div className="min-h-dvh pb-[calc(3.5rem+env(safe-area-inset-bottom))] sm:pb-0">
      <a
        href="#main"
        className="sr-only z-40 rounded-md bg-inv px-3 py-2 text-ui font-medium text-inv-fg focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        跳到主要内容
      </a>
      <header className="sticky top-0 z-30 border-b border-line bg-surface">
        <div className="mx-auto flex h-12 max-w-[1200px] items-center gap-3 px-4 sm:px-6">
          <Link to="/" className="flex h-full min-w-0 items-center gap-2">
            <Brand />
            <span className="text-sm font-semibold tracking-tight">丹炉</span>
          </Link>
          {me.data?.hostname && (
            <>
              <span className="text-fg-4" aria-hidden>
                /
              </span>
              <span className="min-w-0 truncate text-ui text-fg-2" title="当前服务器">
                {me.data.hostname}
              </span>
            </>
          )}
          <nav className="ml-3 hidden h-full items-stretch gap-1 sm:flex">
            {nav.filter((n) => n.to !== '/terminal' || showTerminal).map((n) => (
              <NavLink
                key={n.to}
                to={n.to}
                end={n.end}
                className={({ isActive }) =>
                  cx(
                    '-mb-px flex items-center border-b-2 px-2 text-ui transition-colors duration-150',
                    isActive ? 'border-fg font-medium text-fg' : 'border-transparent text-fg-3 hover:text-fg',
                  )
                }
              >
                {n.label}
              </NavLink>
            ))}
          </nav>
          <Link to="/new" className={cx(buttonClass('primary', 'sm'), 'ml-auto max-sm:hidden')} title="新建任务（N）">
            <Plus />
            新建任务
            <Kbd>N</Kbd>
          </Link>
          {showTerminal && (
            <NavLink
              to="/terminal"
              aria-label="网页终端"
              className={({ isActive }) => cx(buttonClass('ghost', 'icon'), 'ml-auto sm:hidden', isActive && 'bg-hover text-fg')}
            >
              <TerminalIcon className="size-5" />
            </NavLink>
          )}
          <NavLink
            to="/settings"
            aria-label="设置"
            className={({ isActive }) => cx(buttonClass('ghost', 'icon'), '-mr-2 sm:hidden', !showTerminal && 'ml-auto', isActive && 'bg-hover text-fg')}
          >
            <Settings className="size-5" />
          </NavLink>
        </div>
      </header>

      <main id="main" tabIndex={-1} className="mx-auto max-w-[1200px] px-4 py-4 outline-none sm:px-6 sm:py-6">
        <PageBoundary>
          <Outlet />
        </PageBoundary>
      </main>
      <Announcer />
      {help && <ShortcutDialog onClose={() => setHelp(false)} />}

      {/* 手机底部导航：新建放在正中间，终端和设置在顶栏右侧 */}
      <nav className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-surface pb-[env(safe-area-inset-bottom)] sm:hidden">
        <div className="grid h-14 grid-cols-3">
          {[homeNav, { to: '/new', label: '新建', icon: Plus, end: false }, jobsNav].map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              className={({ isActive }) =>
                cx('flex flex-col items-center justify-center gap-1 text-[11px] font-medium', isActive ? 'text-fg' : 'text-fg-3')
              }
            >
              {n.to === '/new' ? (
                <span className="grid h-6 w-9 place-items-center rounded-md bg-inv text-inv-fg">
                  <n.icon className="size-4" strokeWidth={2.5} />
                </span>
              ) : (
                <n.icon className="size-5" />
              )}
              {n.label}
            </NavLink>
          ))}
        </div>
      </nav>
    </div>
  )
}
