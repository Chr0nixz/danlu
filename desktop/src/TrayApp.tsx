import { duration, jobDuration } from '@danlu/core'
import { useQuery } from '@tanstack/react-query'
import { listen } from '@tauri-apps/api/event'
import { AlertTriangle, Flame, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { freeCount, MiniCells, shortAgo } from './components/GpuStrip'
import { progressOf } from './components/JobList'
import { cx, Kbd, Progress } from './components/ui'
import { getPrefs, openMain, type Focus } from './lib/backend'
import { fleetJobs, isOffline, isUnread, useFleetView, useReadSet } from './lib/fleet'
import { shortcutLabel } from './lib/platform'

/**
 * 托盘旁的迷你总览：只看不操作，避免在小窗里误点。
 * 从上到下：没看过的失败和异常、各台的空卡、运行中的任务。点任何一项打开主窗口并定位过去。
 */
export function TrayApp() {
  const view = useFleetView()
  const read = useReadSet(view)
  const prefs = useQuery({ queryKey: ['prefs'], queryFn: getPrefs })
  const [shown, setShown] = useState(0)
  // 每次弹出都重新播一遍进场动画
  useEffect(() => {
    const off = listen('popover-shown', () => setShown((n) => n + 1))
    return () => {
      off.then((f) => f())
    }
  }, [])

  const jobs = useMemo(() => fleetJobs(view.servers), [view.servers])
  const failures = jobs.filter((f) => isUnread(f, read)).sort((a, b) => (b.job.ended_at ?? 0) - (a.job.ended_at ?? 0))
  const running = jobs.filter((f) => f.job.status === 'running').sort((a, b) => (b.job.started_at ?? 0) - (a.job.started_at ?? 0))
  const queued = jobs.filter((f) => f.job.status === 'queued').length
  const go = (focus?: Focus) => openMain(focus)

  return (
    <div key={shown} className="pop flex h-full flex-col overflow-hidden bg-panel">
      <div className="flex items-center gap-2 border-b border-line px-3.5 py-2.5">
        <span className="grid size-[18px] place-items-center rounded-[5px] bg-brand text-white" aria-hidden>
          <Flame className="size-3" strokeWidth={2.5} />
        </span>
        <span className="font-semibold">丹炉</span>
        <span className="num ml-auto text-[11.5px] text-fg-3">
          运行 {running.length} · 排队 {queued}
        </span>
      </div>

      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-2 py-2.5">
        {(failures.length > 0 || view.alerts.length > 0) && (
          <div className="space-y-px rounded-[8px] bg-fail/[0.08] p-1">
            {failures.slice(0, 5).map((f) => (
              <button key={f.key} type="button" onClick={() => go({ kind: 'job', key: f.key })} className="flex h-7 w-full items-center gap-2 rounded-[6px] px-2 text-left hover:bg-fail/[0.08]">
                <X className="size-3.5 shrink-0 text-fail" strokeWidth={2.5} />
                <span className="num shrink-0 text-[11.5px] text-fg-2">{f.key}</span>
                <span className="min-w-0 flex-1 truncate">{f.job.name}</span>
                <span className="num shrink-0 text-[11px] text-fail">{f.job.exit_code !== null ? `exit ${f.job.exit_code}` : '失败'}</span>
              </button>
            ))}
            {failures.length > 5 && <p className="px-2 py-1 text-[11.5px] text-fg-3">还有 {failures.length - 5} 个</p>}
            {view.alerts.map((a) => (
              <button key={a.key} type="button" onClick={() => go({ kind: 'server', key: a.server })} className="flex min-h-7 w-full items-center gap-2 rounded-[6px] px-2 py-1 text-left hover:bg-fail/[0.08]">
                <AlertTriangle className={cx('size-3.5 shrink-0', a.kind === 'offline' || a.kind === 'gpu_error' ? 'text-fail' : 'text-warn')} />
                <span className="min-w-0 flex-1 truncate text-fg-2" title={a.text}>
                  {a.text}
                </span>
              </button>
            ))}
          </div>
        )}

        <div className="space-y-1">
          {view.servers.map((s) => {
            const offline = isOffline(s)
            return (
              <button key={s.name} type="button" onClick={() => go({ kind: 'server', key: s.name })} className={cx('flex w-full items-start gap-2 rounded-[6px] px-2 py-1.5 text-left hover:bg-hover', offline && 'opacity-45')}>
                <span className="w-16 shrink-0">
                  <span className="block truncate font-medium">{s.name}</span>
                  <span className={cx('num block text-[11px]', offline ? 'text-fail' : 'text-fg-3')}>
                    {offline ? (s.updated_at ? shortAgo(s.updated_at) : '连不上') : `空 ${freeCount(s.gpus ?? [])}`}
                  </span>
                </span>
                <MiniCells gpus={s.gpus ?? []} />
              </button>
            )
          })}
          {view.servers.length === 0 && <p className="px-2 py-4 text-center text-fg-3">还没有添加服务器</p>}
        </div>

        {running.length > 0 && (
          <div>
            <div className="px-2 pb-1 text-[11px] text-fg-3">运行中</div>
            {running.map((f) => {
              const pct = progressOf(f.job)
              const d = jobDuration(f.job)
              return (
                <button key={f.key} type="button" onClick={() => go({ kind: 'job', key: f.key })} className="block w-full rounded-[6px] px-2 py-1.5 text-left hover:bg-hover">
                  <span className="flex items-center gap-2">
                    <span className="num shrink-0 text-[11.5px] text-fg-2">{f.key}</span>
                    <span className="min-w-0 flex-1 truncate">{f.job.name}</span>
                    <span className="num shrink-0 text-[11px] text-fg-3">{pct !== null ? `${pct}%` : d !== null ? duration(d) : ''}</span>
                  </span>
                  {pct !== null && <Progress pct={pct} className="mt-1" />}
                </button>
              )
            })}
          </div>
        )}
      </div>

      <div className="flex items-center gap-2 border-t border-line px-3 py-2">
        <button type="button" onClick={() => go()} className="h-7 rounded-[6px] bg-primary px-2.5 font-medium text-on-primary hover:opacity-90">
          打开主窗口
        </button>
        <span className="ml-auto flex items-center gap-1.5 text-[11px] text-fg-3">
          呼出 <Kbd>{shortcutLabel(prefs.data?.shortcut ?? 'Ctrl+Alt+D')}</Kbd>
        </span>
      </div>
    </div>
  )
}
