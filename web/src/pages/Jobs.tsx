import { useQuery } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, Search, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router'
import { JobList, JobListHeader } from '../components/JobRow'
import { Button, buttonClass, Card, ErrorBox, Input, Kbd, Segmented, SkeletonRows, StatusIcon, Empty } from '../components/ui'
import { api, type JobStatus } from '../lib/api'
import { now } from '../lib/format'
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
  const [text, setText] = useState(params.get('q') ?? '')
  const q = params.get('q') ?? ''
  const searchRef = useRef<HTMLInputElement>(null)
  const location = useLocation()

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
    queryKey: ['jobs', 'list', status, q, page, recent],
    queryFn: () => api.jobs({ status, q, page, size: SIZE, ended_after: recent ? Math.floor(now() - RECENT_SEC) : undefined }),
    refetchInterval: 5000,
    placeholderData: (prev) => prev,
  })
  const total = res.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / SIZE))
  const from = total ? (page - 1) * SIZE + 1 : 0
  const to = Math.min(page * SIZE, total)

  return (
    <div className="space-y-3 sm:space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <h1 className="flex items-baseline gap-2 text-lg font-semibold">
          任务
          {res.data && <span className="text-sm font-normal text-fg-3">{total}</span>}
        </h1>
        <div className="relative sm:ml-auto sm:w-80">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-fg-3" aria-hidden />
          <Input
            ref={searchRef}
            type="search"
            aria-label="搜索任务"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && (setText(''), e.currentTarget.blur())}
            placeholder="搜索任务名、命令、目录"
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

      <div className="flex flex-wrap items-center gap-2">
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
      </div>

      <ErrorBox error={res.error} />
      <Card className="overflow-hidden">
        {res.isLoading ? (
          <SkeletonRows rows={6} />
        ) : (
          <JobList
            header={<JobListHeader />}
            jobs={res.data?.jobs ?? []}
            empty={
              q || status || recent ? (
                <Empty>没有符合条件的任务</Empty>
              ) : (
                <Empty action={<Link to="/new" className={buttonClass('primary', 'sm')}>新建任务</Link>}>还没有任务，炉子还没点火</Empty>
              )
            }
          />
        )}
        {total > 0 && (
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
