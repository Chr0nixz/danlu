import { useQuery } from '@tanstack/react-query'
import { Activity, Flame, LayoutDashboard, ListChecks, Plus, Settings, Terminal as TerminalIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { shortcutsEnabled, useShortcutsEnabled } from '../lib/localPrefs'
import { useActiveJobs, useRecentFailures } from '../lib/queries'
import { PageBoundary } from './ErrorBoundary'
import { PhoneReserve } from './Reservations'
import { ShortcutDialog } from './Shortcuts'
import { Announcer, buttonClass, cx, Kbd } from './ui'

// 导航文案在组件渲染时经 t() 取，不做模块级常量表，免得冻结语言
function navItems() {
  return [
    { to: '/', label: t('layout.nav_overview'), icon: LayoutDashboard, end: true },
    { to: '/resources', label: t('layout.nav_resources'), icon: Activity, end: false },
    { to: '/jobs', label: t('layout.nav_jobs'), icon: ListChecks, end: false },
    { to: '/terminal', label: t('layout.nav_terminal'), icon: TerminalIcon, end: false },
    { to: '/settings', label: t('layout.nav_settings'), icon: Settings, end: false },
  ]
}

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
  const nav = navItems()
  const homeNav = nav.find((n) => n.to === '/')!
  const jobsNav = nav.find((n) => n.to === '/jobs')!
  const me = useQuery({ queryKey: ['me'], queryFn: api.me, staleTime: Infinity })
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, staleTime: 60_000 })
  const showTerminal = settings.data?.terminal_enabled === true

  // 快捷键：N 新建任务；/ 搜索任务（任务页自己处理，其他页面先跳过去）；J / K 在任务行之间移动，
  // 焦点落在行的链接上，Enter 打开；? 打开快捷键一览。确认框开着时、在面板偏好里关掉单键快捷键时都不响应
  const [help, setHelp] = useState(false)
  const shortcuts = useShortcutsEnabled()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target) || document.querySelector('dialog[open]') || !shortcutsEnabled()) return
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
  const failed = useRecentFailures().data?.total ?? 0
  const running = active.data?.runningTotal ?? 0
  useEffect(() => {
    const parts = [running && t('layout.title_running', { n: running }), failed && t('layout.title_failed', { n: failed })].filter(Boolean)
    const base = me.data?.hostname ? t('layout.title_host', { hostname: me.data.hostname }) : t('layout.title')
    document.title = parts.length ? `${parts.join(' · ')} — ${base}` : base
  }, [running, failed, me.data?.hostname])

  return (
    <div className="min-h-dvh pb-[calc(3.5rem+env(safe-area-inset-bottom))] sm:pb-0">
      <a
        href="#main"
        className="sr-only z-40 rounded-md bg-inv px-3 py-2 text-ui font-medium text-inv-fg focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        {t('layout.skip_to_content')}
      </a>
      <header className="sticky top-0 z-30 border-b border-line bg-surface">
        <div className="mx-auto flex h-12 max-w-[1200px] items-center gap-3 px-4 sm:px-6">
          {/* 品牌不收缩：主机名长时截断主机名，"丹炉"两个字不会被挤成竖排 */}
          <Link to="/" className="flex h-full shrink-0 items-center gap-2">
            <Brand />
            <span className="text-sm font-semibold tracking-tight whitespace-nowrap">{t('layout.brand')}</span>
          </Link>
          {me.data?.hostname && (
            <>
              <span className="text-fg-4" aria-hidden>
                /
              </span>
              <span className="min-w-0 truncate text-ui text-fg-2" title={t('layout.current_server')}>
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
          <Link to="/new" className={cx(buttonClass('primary', 'sm'), 'ml-auto max-sm:hidden')} title={shortcuts ? t('layout.new_job_title') : undefined}>
            <Plus />
            {t('layout.new_job')}
            {shortcuts && <Kbd>N</Kbd>}
          </Link>
          {/* 手机顶栏右侧：总览上的预约 GPU、终端、设置 */}
          <div className="-mr-2 ml-auto flex items-center sm:hidden">
            {pathname === '/' && <PhoneReserve />}
            {showTerminal && (
              <NavLink
                to="/terminal"
                aria-label={t('layout.terminal_aria')}
                className={({ isActive }) => cx(buttonClass('ghost', 'icon'), isActive && 'bg-hover text-fg')}
              >
                <TerminalIcon className="size-5" />
              </NavLink>
            )}
            <NavLink
              to="/settings"
              aria-label={t('layout.nav_settings')}
              className={({ isActive }) => cx(buttonClass('ghost', 'icon'), isActive && 'bg-hover text-fg')}
            >
              <Settings className="size-5" />
            </NavLink>
          </div>
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
          {[homeNav, { to: '/new', label: t('layout.nav_new'), icon: Plus, end: false }, jobsNav].map((n) => (
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
