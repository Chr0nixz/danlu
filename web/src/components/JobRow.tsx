import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, X } from 'lucide-react'
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react'
import { Link } from 'react-router'
import { compactProgress, duration, failText, fmtTime, isActive, jobDuration, now, timeAgo, type Job } from '@danlu/core'
import { api } from '../lib/api'
import { StarButton } from './StarButton'
import { Button, buttonClass, Commit, cx, Dialog, Empty, ErrorBox, GpuChips, Meter, StatusIcon } from './ui'

/** 取消任务前的确认框：写清已经跑了多久、结束后不能恢复。出错时留在框里显示原因 */
export function CancelDialog({ job: j, onClose }: { job: Job; onClose: () => void }) {
  const qc = useQueryClient()
  const cancel = useMutation({
    mutationFn: () => api.cancel(j.id),
    onSuccess: () => {
      qc.invalidateQueries()
      onClose()
    },
  })
  const running = j.status === 'running'
  const d = jobDuration(j)
  return (
    <Dialog
      title={`取消任务 #${j.id}？`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{running ? '继续运行' : '保留排队'}</Button>
          <Button variant="danger" loading={cancel.isPending} onClick={() => cancel.mutate()}>
            取消任务
          </Button>
        </>
      }
    >
      <p className="font-medium break-all text-fg">{j.name}</p>
      <p>
        {running
          ? `已经运行 ${d !== null ? duration(d) : '-'}。确定后进程会被结束，不能恢复；日志和代码快照会保留。`
          : `${j.queue_pos ? `排在第 ${j.queue_pos} 位，` : ''}取消后从队列里移除。`}
      </p>
      <ErrorBox error={cancel.error} />
    </Dialog>
  )
}

// 记住每个任务上次显示的状态；状态变了，这一行按新状态的颜色闪一下
const flashTone: Partial<Record<string, string>> = { succeeded: 'ok', failed: 'fail', cancelled: 'fg-3' }
const seen = new Map<number, string>()

function useFlash(id: number, status: string) {
  const [flash, setFlash] = useState(false)
  useEffect(() => {
    const prev = seen.get(id)
    seen.set(id, status)
    if (prev === undefined || prev === status) return
    setFlash(true)
    const t = setTimeout(() => setFlash(false), 1200)
    return () => clearTimeout(t)
  }, [id, status])
  return flash
}

/** 一行任务的说明文字：运行中显示最后一行日志，排队显示位置，失败显示原因 */
function detail(j: Job): { text: string; short?: string; mono?: boolean; tone?: string; pct?: number | null } {
  if (j.status === 'queued') {
    const pos = j.queue_pos ? `第 ${j.queue_pos} 位` : '排队中'
    const need = j.gpus_pinned.length ? `等 GPU ${j.gpus_pinned.join(',')}` : j.gpus_req ? `等 ${j.gpus_req} 张卡` : '不用 GPU'
    return { text: `${pos} · ${j.wait_reason || need}` }
  }
  if (j.status === 'running' && j.cancel_requested) return { text: '正在结束进程…', tone: 'text-warn' }
  if (j.status === 'failed') return { text: failText(j), tone: 'text-fail' }
  if (j.pruned_at) return { text: '日志和代码快照已清理' }
  if (j.last_line) {
    const p = compactProgress(j.last_line)
    return { text: p.text, short: p.short, mono: true, pct: j.status === 'running' ? p.pct : null }
  }
  if (!j.started_at) return { text: '未开始运行' }
  return { text: j.status === 'running' ? '等待输出…' : '没有输出' }
}

/**
 * 一行任务。整行可点（名字是链接，用 ::after 铺满整行），commit、复制和取消操作浮在上面单独可点。
 * 电脑上是固定列；手机上折成两行：第一行名字和时间，第二行卡号、说明文字和 commit，字段一个不少。
 */
export function JobRow({ job: j, showCopy = false }: { job: Job; showCopy?: boolean }) {
  const [confirming, setConfirming] = useState(false)
  const flash = useFlash(j.id, j.status)
  const d = jobDuration(j)
  const gpus = j.gpus_assigned.length ? j.gpus_assigned : j.gpus_pinned
  const info = detail(j)
  const ended = !isActive(j)
  const time = j.status === 'queued' ? `等 ${duration(now() - j.created_at)}` : d !== null ? duration(d) : ''
  const timeLabel = j.status === 'queued' ? '已等待' : j.status === 'running' ? '已运行' : '耗时'
  const canCancel = isActive(j) && !j.cancel_requested
  const code = `/jobs/${j.id}?tab=code`

  return (
    <div
      className={cx('group relative flex items-center gap-3 px-3 py-2.5 transition-colors duration-150 hover:bg-subtle sm:px-4', flash && 'animate-flash')}
      style={flash ? ({ '--flash': `var(--${flashTone[j.status] ?? 'run'})` } as CSSProperties) : undefined}
    >
      <StatusIcon status={j.status} cancelling={j.cancel_requested} className="self-start mt-0.5 md:mt-0 md:self-center" />
      <div className="flex min-w-0 flex-1 flex-col gap-1 md:flex-row md:items-center md:gap-4">
        {/* 首页从名称列调配复制入口的宽度，保留卡号和最新输出列的空间 */}
        <div className={cx('flex min-w-0 items-baseline gap-2 md:shrink-0', showCopy ? 'md:w-[208px] lg:w-[248px]' : 'md:w-[240px] lg:w-[280px]')}>
          <div className="relative h-5 w-11 shrink-0 md:hidden">
            <div className="absolute inset-x-0 top-1/2 z-10 -translate-y-1/2"><StarButton job={j} /></div>
          </div>
          <Link
            to={`/jobs/${j.id}`}
            title={j.name}
            className="flex min-w-0 items-baseline gap-2 after:absolute after:inset-0 focus-visible:outline-none focus-visible:after:outline-2 focus-visible:after:-outline-offset-2 focus-visible:after:outline-run/70"
          >
            <span className="shrink-0 text-ui text-fg-3">#{j.id}</span>
            <span className="truncate text-sm font-medium text-fg">{j.name}</span>
          </Link>
          <span className="ml-auto shrink-0 pl-2 text-xs text-fg-3 md:hidden" title={ended ? fmtTime(j.ended_at) : timeLabel}>
            {ended && j.ended_at ? `${time} · ${timeAgo(j.ended_at)}` : time}
          </span>
        </div>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          {gpus.length > 0 && <GpuChips ids={gpus} />}
          {info.pct != null && (
            <span className="w-10 shrink-0 max-md:hidden" title={`${info.pct}%`}>
              <Meter value={info.pct} className="bg-run" />
            </span>
          )}
          <span
            title={info.text}
            className={cx(
              // 手机上这一行还要放 commit，放不下时折成两行，不让 loss、失败原因这类信息被截掉
              'min-w-0 text-xs max-md:line-clamp-2 max-md:[overflow-wrap:anywhere] md:truncate',
              info.mono && 'font-mono',
              info.tone ?? 'text-fg-3',
            )}
          >
            {info.short && info.short !== info.text ? (
              <>
                <span className="md:hidden">{info.short}</span>
                <span className="max-md:hidden">{info.text}</span>
              </>
            ) : (
              info.text
            )}
          </span>
          <Commit sha={j.git_commit} dirty={j.git_dirty} to={code} className="relative z-10 ml-auto shrink-0 pl-1 md:hidden" />
        </div>
        <div className="hidden shrink-0 items-center gap-4 text-xs text-fg-3 md:flex">
          <span className="w-[5.5rem] text-right" title={timeLabel}>
            {time}
          </span>
          <span className="w-[4.5rem] text-right" title={ended ? fmtTime(j.ended_at) : undefined}>
            {ended ? timeAgo(j.ended_at) : ''}
          </span>
          <span className="w-[4.5rem]">
            <Commit sha={j.git_commit} dirty={j.git_dirty} to={code} className="relative z-10" />
          </span>
        </div>
      </div>
      <div className={cx('relative z-10 flex shrink-0 items-center max-md:-mr-2', !showCopy && !canCancel && 'max-md:hidden')}>
        <div className="max-md:hidden"><StarButton job={j} /></div>
        {showCopy && (
          <Link
            to={`/new?copy=${j.id}`}
            className={cx(buttonClass('ghost', 'icon-sm'), 'text-fg-3 max-md:-my-0.5 max-md:size-11')}
            title="复制任务"
            aria-label={`复制任务 #${j.id}`}
          >
            <Copy aria-hidden />
          </Link>
        )}
        {canCancel ? (
          <Button
            size="icon-sm"
            variant="ghost"
            className="text-fg-3 hover:text-fail max-md:size-11 md:pointer-fine:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100"
            title="取消任务"
            aria-label={`取消任务 #${j.id}`}
            onClick={() => setConfirming(true)}
          >
            <X />
          </Button>
        ) : (
          <span className="hidden size-8 shrink-0 md:block" />
        )}
      </div>
      {confirming && <CancelDialog job={j} onClose={() => setConfirming(false)} />}
    </div>
  )
}

/** 任务页电脑端的列头，列宽和 JobRow 的固定列一致 */
export function JobListHeader() {
  return (
    <div className="hidden h-8 items-center gap-3 border-b border-line bg-subtle px-4 text-xs text-fg-3 md:flex" aria-hidden>
      <span className="w-4 shrink-0" />
      <div className="flex min-w-0 flex-1 items-center gap-4">
        <span className="w-[240px] shrink-0 lg:w-[280px]">任务</span>
        <span className="min-w-0 flex-1">卡 · 最新输出</span>
        <span className="w-[5.5rem] shrink-0 text-right">耗时</span>
        <span className="w-[4.5rem] shrink-0 text-right">结束于</span>
        <span className="w-[4.5rem] shrink-0">代码</span>
      </div>
      <span className="h-8 w-16 shrink-0" />
    </div>
  )
}

export function JobList({ jobs, empty, header, showCopy = false }: { jobs: Job[]; empty: ReactNode; header?: ReactNode; showCopy?: boolean }) {
  if (!jobs.length) return typeof empty === 'string' ? <Empty>{empty}</Empty> : <>{empty}</>
  return (
    <>
      {header}
      <div className="divide-y divide-line">
        {jobs.map((j) => (
          <JobRow key={j.id} job={j} showCopy={showCopy} />
        ))}
      </div>
    </>
  )
}
