import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUpToLine, ChevronLeft, ChevronRight, ListChecks, Pause, Play, Replace, Search, Star, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router'
import { JobList, JobListHeader, JobRow, type RowSelect } from '../components/JobRow'
import { QueueBanner } from '../components/QueueControls'
import { BatchCancelDialog, idList, QueueOpError, ReplaceDialog } from '../components/QueueOps'
import { StarError } from '../components/StarButton'
import { Button, buttonClass, Card, ErrorBox, Input, Kbd, Segmented, SkeletonRows, StatusIcon, Empty } from '../components/ui'
import { now, type BatchAction, type Job, type JobStatus } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { usePrefs } from '../lib/prefs'
import { RECENT_SEC } from '../lib/queries'

export function Jobs() {
  // 状态筛选项在组件内取文案：模块级常量表会冻结语言。与 status.* 一致的词直接复用 core 键
  const filters: { value: '' | JobStatus; label: string }[] = [
    { value: '', label: t('jobs.filter_all') },
    { value: 'running', label: t('status.running') },
    { value: 'queued', label: t('jobs.filter_queued') },
    { value: 'succeeded', label: t('status.succeeded') },
    { value: 'failed', label: t('status.failed') },
    { value: 'cancelled', label: t('status.cancelled') },
  ]
  // 每页条数在「设置 → 面板偏好」里改，手机和电脑共用。等偏好到了再查，免得先按 20 条查一遍再换
  const { prefs, ready: prefsReady } = usePrefs()
  const SIZE = prefs.jobs_page_size
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
  // 选择模式：整行变成勾选的点击区（见 JobRow），点到行不会跳走；翻页、筛选和搜索都不丢已选的任务。
  // 选中的都是排队任务时可以批量取消、替换命令里的文字、暂缓、恢复、置顶（FEATURES 3.1）；选两个任务时可以对比。
  // 记下选中时的任务，换页以后也知道它们是什么状态
  const [selection, setSelection] = useState<Map<number, Job> | null>(null)
  const [dialog, setDialog] = useState<'cancel' | 'replace' | null>(null)
  const selectJob = (job: Job): RowSelect | undefined => {
    if (!selection) return undefined
    const checked = selection.has(job.id)
    return {
      checked,
      disabled: false,
      onToggle: () =>
        setSelection((s) => {
          if (!s) return s
          const next = new Map(s)
          if (checked) next.delete(job.id)
          else next.set(job.id, job)
          return next
        }),
    }
  }
  const selected = selection ? [...selection.values()] : []
  const selectedIds = selected.map((j) => j.id)
  const allQueued = selected.length > 0 && selected.every((j) => j.status === 'queued')
  const qc = useQueryClient()
  const bulk = useMutation({
    mutationFn: (action: BatchAction) => api.batchJobs({ ids: selectedIds, action }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
  const exitSelection = () => {
    setSelection(null)
    setDialog(null)
    bulk.reset()
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
    const timer = setTimeout(() => text !== q && set({ q: text, page: '' }), 300)
    return () => clearTimeout(timer)
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
    queryKey: ['jobs', 'list', status, q, page, SIZE, recent, starred, finishedFrom, finishedTo],
    queryFn: () => api.jobs({
      status, q, page, size: SIZE, starred: starred || undefined,
      // 只看排队任务时按队列顺序列出（置顶、暂缓后的顺序），和"第 N 位"一致
      order: status === 'queued' ? 'asc' : undefined,
      ended_after: recent ? Math.floor(now() - RECENT_SEC) : undefined,
      finished_from_event: batch ? finishedFrom : undefined, finished_to_event: batch ? finishedTo : undefined,
    }),
    enabled: prefsReady && !q.startsWith('/'),
    refetchInterval: 5000,
    placeholderData: (prev) => prev,
  })
  // 选中的任务状态变了（开始运行、被暂缓）就换成新的，批量按钮跟着变
  useEffect(() => {
    const fresh = res.data?.jobs
    if (!fresh || !selection) return
    const stale = fresh.filter((j) => {
      const old = selection.get(j.id)
      return old && (old.status !== j.status || old.held !== j.held)
    })
    if (stale.length) setSelection((s) => s && new Map([...s, ...stale.map((j) => [j.id, j] as const)]))
  }, [res.data])

  // 快速失败暂停的横幅里点"修改排队中的任务"：选中全部排队任务，直接打开替换
  const replaceAll = params.get('replace') === '1'
  useEffect(() => {
    if (!replaceAll) return
    let live = true
    void api.jobs({ status: 'queued', order: 'asc', size: 200 }).then((r) => {
      if (!live) return
      setSelection(new Map(r.jobs.map((j) => [j.id, j])))
      if (r.jobs.length) setDialog('replace')
    }).finally(() => live && set({ replace: '' }))
    return () => {
      live = false
    }
  }, [replaceAll])

  const pathSearch = q.startsWith('/')
  const trace = useQuery({
    queryKey: ['trace', q],
    queryFn: () => api.trace(q),
    enabled: pathSearch,
    retry: false,
  })
  // 手机上"只看标星"和"对比"一起放在标题行：六个状态的筛选条占满一整行，放在它旁边会多折出一行
  const starToggle = (className: string) => (
    <Button size="sm" variant={starred ? 'secondary' : 'ghost'} className={className} aria-pressed={starred} onClick={() => set({ starred: starred ? '' : '1', page: '' })}>
      <Star aria-hidden className={starred ? 'fill-current' : ''} />{t('jobs.starred_only')}
    </Button>
  )
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
            {t('jobs.title')}
            {(pathSearch ? trace.data : res.data) && <span className="text-sm font-normal text-fg-3">{total}</span>}
          </h1>
          {selection ? (
            <Button size="sm" variant="ghost" onClick={exitSelection}>{t('jobs.exit_selection')}</Button>
          ) : (
            <Button size="sm" variant="ghost" title={t('jobs.select_title')} onClick={() => setSelection(new Map())}><ListChecks aria-hidden />{t('jobs.select')}</Button>
          )}
          {!pathSearch && starToggle('sm:hidden')}
        </div>
        <div className="relative sm:ml-auto sm:w-80">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-fg-3" aria-hidden />
          <Input
            ref={searchRef}
            type="search"
            aria-label={t('jobs.search_label')}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && (setText(''), e.currentTarget.blur())}
            placeholder={t('jobs.search_placeholder')}
            className="pl-8 pr-9 [&::-webkit-search-cancel-button]:appearance-none"
          />
          {text ? (
            <button
              type="button"
              onClick={() => {
                setText('')
                searchRef.current?.focus()
              }}
              className="absolute right-1 top-1/2 grid size-7 -translate-y-1/2 place-items-center rounded text-fg-3 hover:bg-hover hover:text-fg max-sm:size-9"
              aria-label={t('jobs.search_clear')}
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

      {selection && (
        // 选择模式的操作条：选中的都在排队时出现批量操作，正好两个时可以对比
        <div role="toolbar" aria-label={t('jobs.selection_toolbar')} className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
          <span className="mr-1 min-w-0 text-xs text-fg-3 max-sm:basis-full" aria-live="polite">
            {selected.length ? t('jobs.selected', { n: selected.length, list: idList(selectedIds) }) : t('jobs.select_hint')}
          </span>
          {allQueued && (
            <>
              <Button size="sm" onClick={() => setDialog('replace')}><Replace aria-hidden />{t('jobs.batch_replace')}</Button>
              <Button size="sm" variant="ghost" loading={bulk.isPending && bulk.variables === 'top'} onClick={() => bulk.mutate('top')}>
                <ArrowUpToLine aria-hidden />{t('jobs.move_top')}
              </Button>
              {selected.some((j) => !j.held) && (
                <Button size="sm" variant="ghost" loading={bulk.isPending && bulk.variables === 'hold'} onClick={() => bulk.mutate('hold')}>
                  <Pause aria-hidden />{t('jobs.hold')}
                </Button>
              )}
              {selected.some((j) => j.held) && (
                <Button size="sm" variant="ghost" loading={bulk.isPending && bulk.variables === 'release'} onClick={() => bulk.mutate('release')}>
                  <Play aria-hidden />{t('jobs.release')}
                </Button>
              )}
              <Button size="sm" variant="danger" onClick={() => setDialog('cancel')}><X aria-hidden />{t('jobs.batch_cancel')}</Button>
            </>
          )}
          {selected.length > 0 && !allQueued && selected.length !== 2 && <span className="text-xs text-fg-3">{t('jobs.batch_only_queued')}</span>}
          {selected.length === 2 && (
            <Link to={`/compare?a=${selectedIds[0]}&b=${selectedIds[1]}`} className={buttonClass(allQueued ? 'ghost' : 'primary', 'sm')}>{t('jobs.view_compare')}</Link>
          )}
        </div>
      )}
      {selection && <ErrorBox error={bulk.error} />}
      <QueueOpError />

      {!pathSearch && <div className="flex flex-wrap items-center gap-2">
        {/* 手机上六个状态铺满一整行，只写文字不带图标，"已取消"才不会滑出屏幕；更窄的屏幕上仍然可以横滑 */}
        <div className="-mx-4 max-w-[calc(100%+2rem)] overflow-x-auto px-4 max-sm:w-[calc(100%+2rem)] sm:mx-0 sm:max-w-full sm:px-0">
          <Segmented
            label={t('jobs.filter_by_status')}
            value={status}
            onChange={(v) => set({ status: v, page: '' })}
            className="max-sm:flex max-sm:w-full"
            options={filters.map((f) => ({
              value: f.value,
              label: (
                <>
                  {f.value && <StatusIcon status={f.value} className="size-3.5 max-sm:hidden" decorative />}
                  {f.label}
                </>
              ),
            }))}
          />
        </div>
        {starToggle('max-sm:hidden')}
        {recent && (
          <button
            type="button"
            onClick={() => set({ recent: '', page: '' })}
            className="inline-flex h-8 items-center gap-1 rounded-md border border-line-strong bg-surface pr-1.5 pl-2.5 text-ui text-fg-2 hover:bg-hover hover:text-fg"
            aria-label={t('jobs.recent_remove_label')}
          >
            {t('jobs.recent_24h')}
            <X className="size-3.5" aria-hidden />
          </button>
        )}
        {batch && (
          <Button size="sm" aria-label={t('jobs.batch_show_all_label')} onClick={() => set({ finished_from_event: '', finished_to_event: '', page: '' })}>
            {t('jobs.batch_finished')} <X aria-hidden />
          </Button>
        )}
      </div>}

      <ErrorBox error={pathSearch ? trace.error : res.error} />
      <Card className="overflow-hidden">
        {(pathSearch ? trace.isLoading : res.isLoading || !prefsReady) ? (
          <SkeletonRows rows={6} />
        ) : pathSearch ? (
          trace.error ? null : trace.data?.matches.length ? (
            <div className="divide-y divide-line">
              {trace.data.matches.map((match) => (
                <div key={match.job.id}>
                  <div className="px-3 pt-2 text-xs text-fg-3 sm:px-4">
                    {t('jobs.trace_source', { source: match.source === 'output_dir' ? t('jobs.trace_source_output_dir') : match.source === 'run_code' ? t('jobs.trace_source_run_code') : match.source === 'danlu_json' ? 'danlu.json' : t('jobs.trace_source_guess') })}
                  </div>
                  <JobRow job={match.job} showCopy select={selectJob(match.job)} />
                </div>
              ))}
            </div>
          ) : (
            <Empty>{t('jobs.trace_empty')}</Empty>
          )
        ) : (
          <JobList
            header={<JobListHeader selecting={selection !== null} showCopy />}
            select={selection ? (job) => selectJob(job)! : undefined}
            jobs={res.data?.jobs ?? []}
            showCopy
            empty={
              q || status || recent || starred || batch ? (
                <Empty>{t('jobs.filter_empty')}</Empty>
              ) : (
                <Empty action={<Link to="/new" className={buttonClass('primary', 'sm')}>{t('jobs.new_job')}</Link>}>{t('jobs.empty')}</Empty>
              )
            }
          />
        )}
        {!pathSearch && total > 0 && (
          <div className="flex h-11 items-center gap-2 border-t border-line px-3 text-xs text-fg-3 sm:px-4">
            <span>
              {t('jobs.pagination_range', { from, to, total })}
            </span>
            {pages > 1 && (
              <div className="ml-auto flex items-center gap-1">
                <Button size="icon-sm" variant="ghost" disabled={page <= 1} onClick={() => set({ page: String(page - 1) })} aria-label={t('jobs.prev_page')}>
                  <ChevronLeft />
                </Button>
                <span className="min-w-12 text-center text-fg-2">
                  {page} / {pages}
                </span>
                <Button size="icon-sm" variant="ghost" disabled={page >= pages} onClick={() => set({ page: String(page + 1) })} aria-label={t('jobs.next_page')}>
                  <ChevronRight />
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>
      {dialog === 'cancel' && <BatchCancelDialog ids={selectedIds} onDone={exitSelection} onClose={() => setDialog(null)} />}
      {dialog === 'replace' && <ReplaceDialog ids={selectedIds} onDone={exitSelection} onClose={() => setDialog(null)} />}
    </div>
  )
}
