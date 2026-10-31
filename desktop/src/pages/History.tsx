import { type Job, type JobStatus } from '@danlu/core'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FilterMenu } from '../components/Filters'
import { JobList } from '../components/JobList'
import { Button, Empty, ErrorText } from '../components/ui'
import { apiFor, type FleetView } from '../lib/backend'
import { isOffline, jobKey, useReadSet, type FleetJob } from '../lib/fleet'
import { useListKeys } from '../lib/keys'
import { useWorkspace } from '../lib/workspace'

const PAGE = 50

interface Cursor {
  jobs: Job[]
  /** 这台已经没有更早的了 */
  done: boolean
  error?: string
}

/**
 * 所有服务器上已结束的任务，按结束时间合并。每台服务器各自用游标往前翻；
 * 只显示"每台都已经翻到"的时间点之后的任务，合并出来的顺序才是对的。
 */
function useHistory(names: string[], q: string, status: JobStatus | null) {
  const [cursors, setCursors] = useState<Record<string, Cursor>>({})
  const [loading, setLoading] = useState(false)
  const gen = useRef(0)

  const loadMore = useCallback(
    async (reset = false) => {
      const g = reset ? ++gen.current : gen.current
      setLoading(true)
      const base = reset ? {} : cursors
      const next: Record<string, Cursor> = { ...base }
      await Promise.all(
        names.map(async (n) => {
          const c = base[n] ?? { jobs: [], done: false }
          if (c.done) return
          const last = c.jobs[c.jobs.length - 1]
          try {
            const r = await apiFor(n).jobs({
              order: 'ended',
              status: status ?? 'succeeded,failed,cancelled',
              q: q || undefined,
              size: PAGE,
              before: last?.ended_at ?? undefined,
              before_id: last?.id,
            })
            next[n] = { jobs: [...c.jobs, ...r.jobs], done: r.jobs.length < PAGE }
          } catch (e) {
            next[n] = { ...c, error: e instanceof Error ? e.message : String(e) }
          }
        }),
      )
      if (g === gen.current) {
        setCursors(next)
        setLoading(false)
      }
    },
    [names, q, status, cursors],
  )

  // 条件变了从头取
  const key = `${names.join(',')}|${q}|${status}`
  useEffect(() => {
    loadMore(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  const merged = useMemo(() => {
    // 还没翻完的服务器里，最后一条最晚的那个时间就是安全线
    const cutoff = Math.max(
      0,
      ...Object.values(cursors)
        .filter((c) => !c.done && c.jobs.length)
        .map((c) => c.jobs[c.jobs.length - 1].ended_at ?? 0),
    )
    const out: FleetJob[] = []
    for (const [server, c] of Object.entries(cursors)) {
      for (const job of c.jobs) if ((job.ended_at ?? 0) >= cutoff) out.push({ server, job, key: jobKey(server, job.id) })
    }
    return out.sort((a, b) => (b.job.ended_at ?? 0) - (a.job.ended_at ?? 0) || b.job.id - a.job.id)
  }, [cursors])

  const more = Object.values(cursors).some((c) => !c.done && !c.error)
  const errors = Object.entries(cursors).filter(([, c]) => c.error)
  return { list: merged, loading, more, errors, loadMore: () => loadMore(false) }
}

export function History({ view }: { view: FleetView }) {
  const { pane, openJob, search } = useWorkspace()
  const read = useReadSet(view)
  const [server, setServer] = useState<string | null>(null)
  const [status, setStatus] = useState<JobStatus | null>(null)
  const [q, setQ] = useState(search)
  // 搜索框停顿 300ms 再请求
  useEffect(() => {
    const t = setTimeout(() => setQ(search.trim()), 300)
    return () => clearTimeout(t)
  }, [search])
  const names = useMemo(
    () => view.servers.filter((s) => !isOffline(s) && (!server || s.name === server)).map((s) => s.name),
    [view.servers, server],
  )
  const h = useHistory(names, q, status)
  const selected = pane?.kind === 'job' ? `${pane.server}-${pane.id}` : null
  const list = h.list
  const cursor = useListKeys(list, selected, (f) => openJob(f.server, f.job.id))

  // 滚到底自动加载更早的
  const sentinel = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver((es) => es[0].isIntersecting && h.more && !h.loading && h.loadMore())
    io.observe(el)
    return () => io.disconnect()
  }, [h])

  return (
    <section className="flex h-full min-w-0 flex-1 flex-col overflow-hidden rounded-[10px] border border-line bg-panel shadow-panel">
      <div className="flex shrink-0 items-center gap-1 px-2.5 pt-2.5 pb-1.5">
        <h1 className="mr-2 pl-1 text-[13px] font-semibold">历史</h1>
        {view.servers.length > 1 && (
          <FilterMenu label="服务器" value={server} onChange={setServer} options={view.servers.map((s) => ({ value: s.name, label: s.name }))} />
        )}
        <FilterMenu
          label="状态"
          value={status}
          onChange={setStatus}
          options={[
            { value: 'failed' as const, label: '失败' },
            { value: 'succeeded' as const, label: '成功' },
            { value: 'cancelled' as const, label: '已取消' },
          ]}
        />
        <span className="ml-auto pr-1 text-[11.5px] text-fg-3">{q ? `名称、命令或目录含"${q}"` : '所有服务器上已结束的任务，按结束时间'}</span>
      </div>
      {h.errors.map(([n, c]) => (
        <ErrorText key={n} error={`${n}：${c.error}`} className="px-4 pb-1" />
      ))}
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-1.5">
        {list.length === 0 && !h.loading ? (
          <Empty title={q ? '没有找到' : '还没有结束的任务'} />
        ) : (
          <JobList jobs={list} selected={selected} cursor={cursor} read={read} onSelect={(f) => openJob(f.server, f.job.id)} />
        )}
        <div ref={sentinel} className="flex h-10 items-center justify-center text-fg-3">
          {h.loading ? '读取中…' : h.more ? <Button variant="ghost" onClick={h.loadMore}>加载更早的</Button> : list.length > 0 ? '没有更早的了' : null}
        </div>
      </div>
    </section>
  )
}
