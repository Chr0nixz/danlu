import { compactProgress, duration, jobDuration, projectOf, statusLabel, timeAgo, type Job, type JobStatus } from '@danlu/core'
import { useEffect, useRef } from 'react'
import { isUnread, type FleetJob } from '../lib/fleet'
import { cx, StatusIcon, statusVar } from './ui'

/** 卡号写紧凑些：0,1,2,3 → 0-3 */
export function gpuRange(ids: number[]): string {
  if (ids.length === 0) return ''
  const sorted = [...ids].sort((a, b) => a - b)
  const contiguous = sorted.every((v, i) => i === 0 || v === sorted[i - 1] + 1)
  return contiguous && sorted.length > 2 ? `${sorted[0]}-${sorted[sorted.length - 1]}` : sorted.join(',')
}

export function progressOf(j: Job): number | null {
  return j.status === 'running' && j.last_line ? compactProgress(j.last_line).pct : null
}

/** 行尾的进度或状态 */
function stateText(j: Job): { text: string; tone?: string } {
  switch (j.status) {
    case 'running':
      if (j.cancel_requested) return { text: '正在结束…', tone: 'text-warn' }
      return { text: '运行中', tone: 'text-run' }
    case 'queued': {
      const need = j.gpus_pinned.length ? `等卡 ${gpuRange(j.gpus_pinned)}` : `需 ${j.gpus_req} 卡`
      return { text: `排队 #${j.queue_pos ?? '-'} · ${need}` }
    }
    case 'failed':
      return { text: j.exit_code !== null ? `失败 exit ${j.exit_code}` : '失败', tone: 'text-fail' }
    default:
      return { text: statusLabel[j.status], tone: j.status === 'succeeded' ? 'text-ok' : undefined }
  }
}

function timeText(j: Job): string {
  if (j.status === 'running') {
    const d = jobDuration(j)
    return d !== null ? duration(d) : ''
  }
  if (j.status === 'queued') return timeAgo(j.created_at)
  return timeAgo(j.ended_at)
}

/** 状态变了的行闪一下：记住上一次看到的状态 */
function useFlash(list: FleetJob[]) {
  const seen = useRef(new Map<string, JobStatus>())
  const flashing = new Set<string>()
  for (const f of list) {
    const prev = seen.current.get(f.key)
    if (prev && prev !== f.job.status) flashing.add(f.key)
  }
  useEffect(() => {
    for (const f of list) seen.current.set(f.key, f.job.status)
  })
  return flashing
}

export function JobList({
  jobs,
  selected,
  cursor,
  read,
  onSelect,
}: {
  jobs: FleetJob[]
  /** 右侧打开着的任务 */
  selected: string | null
  /** 键盘光标所在的行 */
  cursor: string | null
  read: Set<string>
  onSelect: (f: FleetJob) => void
}) {
  const flashing = useFlash(jobs)
  const cursorRef = useRef<HTMLDivElement>(null)
  useEffect(() => cursorRef.current?.scrollIntoView({ block: 'nearest' }), [cursor])
  return (
    // 列表按自己的宽度取舍列：右侧详情打开、窗口又窄时，先收起项目和卡号，名字始终留着
    <div role="listbox" aria-label="任务" className="@container space-y-px">
      {jobs.map((f) => {
        const j = f.job
        const s = stateText(j)
        const pct = progressOf(j)
        const unread = isUnread(f, read)
        const isSel = selected === f.key
        return (
          <div
            key={`${f.key}:${flashing.has(f.key) ? j.status : ''}`}
            ref={cursor === f.key ? cursorRef : undefined}
            role="option"
            aria-selected={isSel}
            title={j.last_line ? compactProgress(j.last_line).text : undefined}
            onClick={() => onSelect(f)}
            style={flashing.has(f.key) ? ({ '--flash': statusVar[j.status] } as React.CSSProperties) : undefined}
            className={cx(
              'group flex h-[30px] items-center gap-3 rounded-[6px] px-2 transition-colors duration-[120ms]',
              flashing.has(f.key) && 'flash',
              isSel ? 'bg-hover shadow-[inset_0_0_0_1px_var(--line-strong)]' : cursor === f.key ? 'bg-hover/60' : 'hover:bg-hover',
            )}
          >
            <StatusIcon status={j.status} />
            <span className="num w-[76px] shrink-0 truncate text-[11.5px] text-fg-2">{f.key}</span>
            <span className="w-24 shrink-0 truncate text-fg-2 @max-[640px]:hidden">{projectOf(j.workdir)}</span>
            <span className="min-w-[96px] flex-1 truncate font-medium">{j.name}</span>
            <span className="num w-12 shrink-0 truncate text-[11.5px] text-fg-3 @max-[540px]:hidden">{gpuRange(j.gpus_assigned)}</span>
            <span className={cx('num flex w-[150px] shrink-0 items-center gap-2 text-[11.5px] @max-[640px]:w-[118px]', s.tone ?? 'text-fg-3')}>
              {pct !== null ? (
                <>
                  <span className="h-1 flex-1 overflow-hidden rounded-full bg-hover">
                    <span className="block h-full rounded-full bg-run transition-[width] duration-300" style={{ width: `${Math.max(2, pct)}%` }} />
                  </span>
                  <span className="w-9 text-right">{pct}%</span>
                </>
              ) : (
                <span className="truncate">{s.text}</span>
              )}
            </span>
            <span className="num w-[72px] shrink-0 truncate text-right text-[11.5px] text-fg-3 @max-[640px]:w-[60px]">{timeText(j)}</span>
            <span className={cx('size-1.5 shrink-0 rounded-full', unread ? 'bg-fail' : 'bg-transparent')} aria-label={unread ? '未读' : undefined} />
          </div>
        )
      })}
    </div>
  )
}
