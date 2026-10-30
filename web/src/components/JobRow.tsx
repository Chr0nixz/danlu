import { useMutation, useQueryClient } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react'
import { Link } from 'react-router'
import { api, type Job } from '../lib/api'
import { duration, fmtTime, isActive, jobDuration, now, timeAgo } from '../lib/format'
import { Button, Commit, cx, Empty, GpuChips, Meter, StatusIcon } from './ui'

export function useCancel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (j: Job) => api.cancel(j.id),
    onSuccess: () => qc.invalidateQueries(),
    onError: (e) => alert((e as Error).message),
  })
}

export function confirmCancel(j: Job) {
  return confirm(`确定取消任务 #${j.id}「${j.name}」吗？${j.status === 'running' ? '\n正在运行的进程会被结束。' : ''}`)
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

/**
 * tqdm 进度条里的方块字符在列表里会变成一大块深色，还会把后面的数字挤出去。
 * "84%|████▍     | 84/100 [loss=0.88]" → "84% · 84/100 [loss=0.88]"，百分比单独拿出来画细条。
 */
export function compactProgress(line: string): { text: string; short: string; pct: number | null } {
  const m = line.match(/(\d{1,3})%\s*\|[▀-▟#\s]*\|/)
  const text = line.replace(/\s*\|[▀-▟#\s]*\|\s*/g, ' · ').replace(/\s{2,}/g, ' ')
  // 手机上宽度不够：有百分比时去掉重复的 75/100（细条也不显示），给 loss 之类的数字留位置
  const short = m ? text.replace(/ · \d+\/\d+\s*/, ' ') : text
  return { text, short, pct: m ? Math.min(100, Number(m[1])) : null }
}

/** 一行任务的说明文字：运行中显示最后一行日志，排队显示位置，失败显示原因 */
function detail(j: Job): { text: ReactNode; short?: string; mono?: boolean; tone?: string; pct?: number | null } {
  if (j.status === 'queued') {
    const pos = j.queue_pos ? `第 ${j.queue_pos} 位` : '排队中'
    const need = j.gpus_pinned.length ? `等 GPU ${j.gpus_pinned.join(',')}` : j.gpus_req ? `等 ${j.gpus_req} 张卡` : '不用 GPU'
    return { text: `${pos} · ${need}` }
  }
  if (j.status === 'running' && j.cancel_requested) return { text: '正在结束进程…', tone: 'text-warn' }
  if (j.status === 'failed') {
    const why = j.fail_reason || (j.exit_code !== null ? `退出码 ${j.exit_code}` : '失败')
    return { text: j.last_line ? `${why} · ${compactProgress(j.last_line).text}` : why, tone: 'text-fail' }
  }
  if (j.last_line) {
    const p = compactProgress(j.last_line)
    return { text: p.text, short: p.short, mono: true, pct: j.status === 'running' ? p.pct : null }
  }
  if (!j.started_at) return { text: '未开始运行' }
  return { text: j.status === 'running' ? '等待输出…' : '没有输出' }
}

export function JobRow({ job: j }: { job: Job }) {
  const cancel = useCancel()
  const flash = useFlash(j.id, j.status)
  const d = jobDuration(j)
  const gpus = j.gpus_assigned.length ? j.gpus_assigned : j.gpus_pinned
  const info = detail(j)
  const time = j.status === 'queued' ? `等 ${duration(now() - j.created_at)}` : d !== null ? duration(d) : ''
  const active = isActive(j) && !j.cancel_requested

  return (
    <div
      className={cx('group flex items-center gap-3 px-3 py-2.5 transition-colors duration-150 hover:bg-subtle sm:px-4', flash && 'animate-flash')}
      style={flash ? ({ '--flash': `var(--${flashTone[j.status] ?? 'run'})` } as CSSProperties) : undefined}
    >
      <StatusIcon status={j.status} cancelling={j.cancel_requested} className="self-start mt-0.5 md:mt-0 md:self-center" />
      <Link to={`/jobs/${j.id}`} className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-col gap-1 md:flex-row md:items-center md:gap-4">
          <div className="flex min-w-0 items-baseline gap-2 md:w-[240px] md:shrink-0 lg:w-[280px]">
            <span className="shrink-0 text-ui text-fg-3">#{j.id}</span>
            <span className="truncate text-sm font-medium text-fg">{j.name}</span>
            <span className="ml-auto shrink-0 pl-2 text-xs text-fg-3 md:hidden">{time}</span>
          </div>
          <div className="flex min-w-0 flex-1 items-center gap-2">
            {gpus.length > 0 && <GpuChips ids={gpus} />}
            {info.pct != null && (
              <span className="w-10 shrink-0 max-md:hidden" title={`${info.pct}%`}>
                <Meter value={info.pct} className="bg-run" />
              </span>
            )}
            <span className={cx('min-w-0 truncate text-xs', info.mono && 'font-mono', info.tone ?? 'text-fg-3')}>{info.short && info.short !== info.text ? (
                <>
                  <span className="md:hidden">{info.short}</span>
                  <span className="max-md:hidden">{info.text}</span>
                </>
              ) : (
                info.text
              )}
            </span>
          </div>
          <div className="hidden shrink-0 items-center gap-4 text-xs text-fg-3 md:flex">
            <span className="w-[5.5rem] text-right">{time}</span>
            <span className="w-[4.5rem] text-right" title={isActive(j) ? undefined : fmtTime(j.ended_at)}>
              {isActive(j) ? '' : timeAgo(j.ended_at)}
            </span>
            <span className="w-[4.5rem]">
              <Commit sha={j.git_commit} dirty={j.git_dirty} />
            </span>
          </div>
        </div>
      </Link>
      {active ? (
        <Button
          size="icon-sm"
          variant="ghost"
          className="text-fg-3 hover:text-fail max-md:-mr-2 max-md:size-11 md:pointer-fine:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100"
          title="取消任务"
          aria-label={`取消任务 #${j.id}`}
          loading={cancel.isPending}
          onClick={() => confirmCancel(j) && cancel.mutate(j)}
        >
          <X />
        </Button>
      ) : (
        <span className="hidden size-8 shrink-0 md:block" />
      )}
    </div>
  )
}

export function JobList({ jobs, empty }: { jobs: Job[]; empty: ReactNode }) {
  if (!jobs.length) return typeof empty === 'string' ? <Empty>{empty}</Empty> : <>{empty}</>
  return (
    <div className="divide-y divide-line">
      {jobs.map((j) => (
        <JobRow key={j.id} job={j} />
      ))}
    </div>
  )
}
