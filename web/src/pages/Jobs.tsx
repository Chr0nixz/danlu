import { useQuery } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, GitCompareArrows, Search, Star, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router'
import { JobList, JobListHeader, JobRow } from '../components/JobRow'
import { QueueBanner } from '../components/QueueControls'
import { StarError } from '../components/StarButton'
import { Button, buttonClass, Card, ErrorBox, Input, Kbd, Segmented, SkeletonRows, StatusIcon, Empty } from '../components/ui'
import { now, type Job, type JobStatus } from '@danlu/core'
import { api } from '../lib/api'
import { RECENT_SEC } from '../lib/queries'

const filters: { value: '' | JobStatus; label: string }[] = [
  { value: '', label: '全部' },
  { value: 'running', label: '运行中' },
  { value: 'queued', label: '排队' },
  { value: 'succeeded', label: '成功' },
  { value: 'failed', label: '失败' },
  { value: 'cancelled', label: '已取消' },
]

const SIZE = 20

export function Jobs() {
  const [params, setParams] = useSearchParams()
  const status = (params.get('status') ?? '') as '' | JobStatus
  const page = Number(params.get('page')) || 1
  // recent=24h：只看最近 24 小时内结束的，总览摘要里的"失败"链接到这里
  const recent = params.get('recent') === '24h'
  const starred = params.get('starred') === '1'
  const finishedFrom = Number(params.get('finished_from_event'))
  const finishedTo = Number(params.get('finished_to_event'))
  const batch = Number.isSafeInteger(finishedFrom) && Number.isSafeInteger(finishedTo) && finishedFrom > 0 && finishedTo >= finishedFrom
  const [text, setText] = useState(params.get('q') ?? '')
  const q = params.get('q') ?? ''
  const searchRef = useRef<HTMLInputElement>(null)
  const location = useLocation()
  const [selection, setSelection] = useState<number[] | null>(null)
  const selectJob = (job: Job) => {
    if (!selection) return undefined
    const checked = selection.includes(job.id)
    return (
      <label className="relative z-10 -ml-2 grid size-10 shrink-0 place-items-center sm:ml-0 sm:size-8">
        <input
          type="checkbox"
          className="size-4 disabled:opacity-40"
          aria-label={`选择任务 #${job.id} ${job.name}`}
          checked={checked}
          disabled={!checked && selection.length === 2}
          onChange={() => setSelection((ids) => ids && (checked ? ids.filter((id) => id !== job.id) : [...ids, job.id]))}
        />
      </label>
    )
  }

  // 从其他页面按 / 跳过来时直接聚焦搜索框
  useEffect(() => {
    if ((location.state as { focusSearch?: boolean } | null)?.focusSearch) searchRef.current?.focus()
  }, [location.state])

  const set = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) v ? next.set(k, v) : next.delete(k)
    setParams(next, { replace: true })
  }

  useEffect(() => {
    const t = setTimeout(() => text !== q && set({ q: text, page: '' }), 300)
    return () => clearTimeout(t)
  })

  // 快捷键：/ 聚焦搜索框
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement
      if (e.key !== '/' || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName)) return
      e.preventDefault()
      searchRef.current?.focus()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const res = useQuery({
    queryKey: ['jobs', 'list', status, q, page, recent, starred, finishedFrom, finishedTo],
    queryFn: () => api.jobs({
      status, q, page, size: SIZE, starred: starred || undefined,
      ended_after: recent ? Math.floor(now() - RECENT_SEC) : undefined,
      finished_from_event: batch ? finishedFrom : undefined, finished_to_event: batch ? finishedTo : undefined,
    }),
    enabled: !q.startsWith('/'),
    refetchInterval: 5000,
    placeholderData: (prev) => prev,
  })
  const pathSearch = q.startsWith('/')
  const trace = useQuery({
    queryKey: ['trace', q],
    queryFn: () => api.trace(q),
    enabled: pathSearch,
    retry: false,
  })
  const total = pathSearch ? trace.data?.matches.length ?? 0 : res.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / SIZE))
  const from = total ? (page - 1) * SIZE + 1 : 0
  const to = Math.min(page * SIZE, total)

  return (
    <div className="space-y-3 sm:space-y-4">
      <QueueBanner />
      <StarError />
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <h1 className="flex items-baseline gap-2 text-lg font-semibold">
            任务
            {(pathSearch ? trace.data : res.data) && <span className="text-sm font-normal text-fg-3">{total}</span>}
          </h1>
          {selection ? (
            <>
              <span className="text-xs text-fg-3" aria-live="polite">{selection.length ? selection.map((id) => `#${id}`).join(' 与 ') : '请选择两个任务'}</span>
              {selection.length === 2 ? (
                <Link to={`/compare?a=${selection[0]}&b=${selection[1]}`} className={buttonClass('primary', 'sm')}>查看对比</Link>
              ) : (
                <Button size="sm" variant="primary" disabled>查看对比</Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => setSelection(null)}>退出选择</Button>
            </>
          ) : (
            <Button size="sm" variant="ghost" onClick={() => setSelection([])}><GitCompareArrows aria-hidden />对比</Button>
          )}
        </div>
        <div className="relative sm:ml-auto sm:w-80">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-fg-3" aria-hidden />
          <Input
            ref={searchRef}
            type="search"
            aria-label="搜索任务"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && (setText(''), e.currentTarget.blur())}
            placeholder="搜索任务，或粘贴产物路径"
            className="pl-8 pr-9 [&::-webkit-search-cancel-button]:appearance-none"
          />
          {text ? (
            <button
              type="button"
              onClick={() => {
                setText('')
                searchRef.current?.focus()
              }}
              className="absolute right-1 top-1/2 grid size-7 -translate-y-1/2 place-items-center rounded text-fg-3 hover:bg-hover hover:text-fg"
              aria-label="清空搜索"
            >
              <X className="size-4" aria-hidden />
            </button>
          ) : (
            <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-fg-3">
              <Kbd>/</Kbd>
            </span>
          )}
        </div>
      </div>

      {!pathSearch && <div className="flex flex-wrap items-center gap-2">
        <div className="-mx-4 max-w-[calc(100%+2rem)] overflow-x-auto px-4 sm:mx-0 sm:max-w-full sm:px-0">
          <Segmented
            label="按状态筛选"
            value={status}
            onChange={(v) => set({ status: v, page: '' })}
            options={filters.map((f) => ({
              value: f.value,
              label: (
                <>
                  {f.value && <StatusIcon status={f.value} className="size-3.5" decorative />}
                  {f.label}
                </>
              ),
            }))}
          />
        </div>
        <Button size="sm" variant={starred ? 'secondary' : 'ghost'} aria-pressed={starred} onClick={() => set({ starred: starred ? '' : '1', page: '' })}>
          <Star aria-hidden className={starred ? 'fill-current' : ''} />只看标星
        </Button>
        {recent && (
          <button
            type="button"
            onClick={() => set({ recent: '', page: '' })}
            className="inline-flex h-8 items-center gap-1 rounded-md border border-line-strong bg-surface pr-1.5 pl-2.5 text-ui text-fg-2 hover:bg-hover hover:text-fg"
            aria-label="去掉条件：最近 24 小时内结束"
          >
            最近 24 小时内结束
            <X className="size-3.5" aria-hidden />
          </button>
        )}
        {batch && (
          <Button size="sm" aria-label="查看全部批次" onClick={() => set({ finished_from_event: '', finished_to_event: '', page: '' })}>
            本批已结束任务 <X aria-hidden />
          </Button>
        )}
      </div>}

      <ErrorBox error={pathSearch ? trace.error : res.error} />
      <Card className="overflow-hidden">
        {(pathSearch ? trace.isLoading : res.isLoading) ? (
          <SkeletonRows rows={6} />
        ) : pathSearch ? (
          trace.error ? null : trace.data?.matches.length ? (
            <div className="divide-y divide-line">
              {trace.data.matches.map((match) => (
                <div key={match.job.id}>
                  <div className="px-3 pt-2 text-xs text-fg-3 sm:px-4">
                    来源：{match.source === 'output_dir' ? '输出目录' : match.source === 'run_code' ? '快照重跑目录' : match.source === 'danlu_json' ? 'danlu.json' : '可能是'}
                  </div>
                  <JobRow job={match.job} selection={selectJob(match.job)} />
                </div>
              ))}
            </div>
          ) : (
            <Empty>没有找到匹配的任务</Empty>
          )
        ) : (
          <JobList
            header={<JobListHeader selecting={selection !== null} />}
            selection={selection ? selectJob : undefined}
            jobs={res.data?.jobs ?? []}
            empty={
              q || status || recent || starred || batch ? (
                <Empty>没有符合条件的任务</Empty>
              ) : (
                <Empty action={<Link to="/new" className={buttonClass('primary', 'sm')}>新建任务</Link>}>还没有任务，炉子还没点火</Empty>
              )
            }
          />
        )}
        {!pathSearch && total > 0 && (
          <div className="flex h-11 items-center gap-2 border-t border-line px-3 text-xs text-fg-3 sm:px-4">
            <span>
              第 {from}–{to} 条，共 {total} 条
            </span>
            {pages > 1 && (
              <div className="ml-auto flex items-center gap-1">
                <Button size="icon-sm" variant="ghost" disabled={page <= 1} onClick={() => set({ page: String(page - 1) })} aria-label="上一页">
                  <ChevronLeft />
                </Button>
                <span className="min-w-12 text-center text-fg-2">
                  {page} / {pages}
                </span>
                <Button size="icon-sm" variant="ghost" disabled={page >= pages} onClick={() => set({ page: String(page + 1) })} aria-label="下一页">
                  <ChevronRight />
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>
    </div>
  )
}
