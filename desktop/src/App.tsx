import { type Job } from '@danlu/core'
import { useQueryClient } from '@tanstack/react-query'
import { listen } from '@tauri-apps/api/event'
import { History as HistoryIcon, LayoutDashboard, Server, Settings as SettingsIcon } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { JobDetail } from './components/JobDetail'
import { SubmitPanel } from './components/SubmitPanel'
import { TitleBar } from './components/TitleBar'
import { cx, Empty, ErrorText } from './components/ui'
import { type Focus } from './lib/backend'
import { fleetJobs, useFleetView, useServers } from './lib/fleet'
import { modKey, typingIn } from './lib/platform'
import { draftFrom, WorkspaceCtx, type Draft, type Pane, type Workspace } from './lib/workspace'
import { History } from './pages/History'
import { Overview } from './pages/Overview'
import { Servers } from './pages/Servers'
import { Settings } from './pages/Settings'

type Page = 'overview' | 'history' | 'servers' | 'settings'

/** 主窗口：自绘标题栏，左侧窄导航栏，中间是页面，总览和历史页的右侧分栏放任务详情或提交表单 */
export function App() {
  const servers = useServers()
  const view = useFleetView()
  const qc = useQueryClient()
  const [page, setPage] = useState<Page>('overview')
  const [pane, setPane] = useState<Pane>(null)
  const [search, setSearch] = useState('')
  const [focusServer, setFocusServer] = useState<string | null>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  const openJob = useCallback((server: string, id: number) => setPane({ kind: 'job', server, id }), [])
  const openSubmit = useCallback((draft?: Draft) => setPane({ kind: 'submit', draft: draft ?? null }), [])
  const closePane = useCallback(() => setPane(null), [])
  const ws: Workspace = useMemo(() => ({ pane, openJob, openSubmit, closePane, search }), [pane, openJob, openSubmit, closePane, search])

  const showServer = useCallback((name: string) => {
    setPage('servers')
    setFocusServer(name)
  }, [])

  // 托盘浮窗、系统通知要求定位到某个任务或服务器
  useEffect(() => {
    const off = listen<Focus>('focus', ({ payload: f }) => {
      if (f.kind === 'server') return showServer(f.key)
      const i = f.key.lastIndexOf('-')
      setPage((p) => (p === 'history' ? p : 'overview'))
      openJob(f.key.slice(0, i), Number(f.key.slice(i + 1)))
    })
    return () => {
      off.then((u) => u())
    }
  }, [openJob, showServer])

  // 全局按键：N 新建、C 复制当前任务、/ 或 Ctrl+K 筛选、Esc 关闭右侧
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (modKey(e) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        if (page !== 'overview' && page !== 'history') setPage('overview')
        return searchRef.current?.focus()
      }
      if (typingIn(e) || e.ctrlKey || e.metaKey || e.altKey || document.querySelector('dialog[open]')) return
      if (e.key === '/') {
        e.preventDefault()
        if (page !== 'overview' && page !== 'history') setPage('overview')
        searchRef.current?.focus()
      } else if (e.key === 'Escape' && pane) {
        setPane(null)
      } else if (e.key === 'n' || e.key === 'N') {
        e.preventDefault()
        if (page !== 'history') setPage('overview')
        openSubmit()
      } else if ((e.key === 'c' || e.key === 'C') && pane?.kind === 'job') {
        const job = qc.getQueryData<Job>(['job', pane.server, pane.id])
        if (job) openSubmit(draftFrom(job, pane.server))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [page, pane, qc, openSubmit])

  const list = servers.data ?? []
  const withPane = page === 'overview' || page === 'history'
  const initialJob = pane?.kind === 'job' ? fleetJobs(view.servers).find((f) => f.server === pane.server && f.job.id === pane.id)?.job : undefined

  let content: ReactNode
  if (servers.isLoading) content = null
  else if (servers.error)
    content = (
      <Empty title="读取服务器列表失败">
        <ErrorText error={servers.error} />
      </Empty>
    )
  else if (list.length === 0 && page !== 'settings') content = <Servers servers={list} view={view} focus={null} />
  else if (page === 'overview') content = <Overview view={view} onServer={showServer} />
  else if (page === 'history') content = <History view={view} />
  else if (page === 'servers') content = <Servers servers={list} view={view} focus={focusServer} />
  else content = <Settings servers={list} />

  return (
    <WorkspaceCtx.Provider value={ws}>
      <div className="flex h-full flex-col">
        <TitleBar search={search} onSearch={setSearch} inputRef={searchRef} />
        <div className="flex min-h-0 flex-1 gap-2 px-2 pb-2">
          <nav className="flex w-10 shrink-0 flex-col items-center gap-1 pt-0.5" aria-label="导航">
            <NavButton label="总览" active={page === 'overview'} onClick={() => setPage('overview')}>
              <LayoutDashboard />
            </NavButton>
            <NavButton label="历史" active={page === 'history'} onClick={() => setPage('history')}>
              <HistoryIcon />
            </NavButton>
            <NavButton label="服务器" active={page === 'servers'} onClick={() => setPage('servers')}>
              <Server />
            </NavButton>
            <span className="flex-1" />
            <NavButton label="设置" active={page === 'settings'} onClick={() => setPage('settings')}>
              <SettingsIcon />
            </NavButton>
          </nav>
          <main className="flex min-w-0 flex-1 gap-2">
            {content}
            {withPane && pane && list.length > 0 && (
              <aside className="w-[min(540px,46%)] shrink-0 overflow-hidden rounded-[10px] border border-line bg-panel shadow-panel">
                {pane.kind === 'job' ? (
                  <JobDetail key={`${pane.server}-${pane.id}`} server={pane.server} id={pane.id} initial={initialJob} />
                ) : (
                  <SubmitPanel key={pane.draft?.source ?? 'new'} initial={pane.draft} servers={view.servers} />
                )}
              </aside>
            )}
          </main>
        </div>
      </div>
    </WorkspaceCtx.Provider>
  )
}

function NavButton({ label, active, onClick, children }: { label: string; active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      aria-current={active ? 'page' : undefined}
      onClick={onClick}
      className={cx(
        'grid size-8 place-items-center rounded-[6px] transition-colors duration-[120ms] [&_svg]:size-4',
        active ? 'bg-panel text-fg shadow-[inset_0_0_0_1px_var(--line-strong)]' : 'text-fg-3 hover:bg-hover hover:text-fg',
      )}
    >
      {children}
    </button>
  )
}
