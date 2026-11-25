import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, Star, X } from 'lucide-react'
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react'
import { Link } from 'react-router'
import { compactProgress, duration, exitCodeOnly, failText, fmtTime, isActive, jobDuration, jobStatusText, progressRemaining, stripProgressTiming, timeAgo, timeAgoShort, useNow, waitText, type Job } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { QueueJobMenu } from './QueueOps'
import { StarButton } from './StarButton'
import { announce, Button, buttonClass, Commit, cx, Dialog, Empty, ErrorBox, GpuChips, Meter, StatusIcon } from './ui'

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
      title={t('jobs.cancel_dialog_title', { id: j.id })}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{running ? t('jobs.keep_running') : t('jobs.keep_queued')}</Button>
          <Button variant="danger" loading={cancel.isPending} onClick={() => cancel.mutate()}>
            {t('jobs.cancel_job')}
          </Button>
        </>
      }
    >
      <p className="font-medium break-all text-fg">{j.name}</p>
      <p>
        {running
          ? t('jobs.cancel_running_body', { duration: d !== null ? duration(d) : '-' })
          : `${j.queue_pos ? t('jobs.cancel_queue_pos', { pos: j.queue_pos }) : ''}${t('jobs.cancel_queued_body')}`}
      </p>
      <ErrorBox error={cancel.error} />
    </Dialog>
  )
}

// 记住每个任务上次显示的状态；状态变了，这一行按新状态的颜色闪一下，同时读给读屏听
const flashTone: Partial<Record<string, string>> = { succeeded: 'ok', failed: 'fail', cancelled: 'fg-3' }
const seen = new Map<number, string>()

function useFlash(j: Job) {
  const [flash, setFlash] = useState(false)
  const { id, status } = j
  useEffect(() => {
    const prev = seen.get(id)
    seen.set(id, status)
    if (prev === undefined || prev === status) return
    announce(t('jobs.status_announce', { id, name: j.name, status: jobStatusText(j) }))
    setFlash(true)
    // 局部变量不叫 t：避免遮蔽 i18n 的 t（上面 announce 用到）
    const timer = setTimeout(() => setFlash(false), 1200)
    return () => clearTimeout(timer)
    // 只在状态变化时触发，名字改了不算
  }, [id, status])
  return flash
}

/** 一行任务的说明文字：运行中显示最后一行日志，排队显示位置，失败显示原因 */
function detail(j: Job): { text: string; short?: string; mono?: boolean; tone?: string; pct?: number | null; remaining?: string | null } {
  if (j.status === 'queued') {
    const pos = j.queue_pos ? t('jobs.queue_pos', { pos: j.queue_pos }) : t('status.queued')
    const need = j.gpus_pinned.length ? t('jobs.wait_pinned', { list: j.gpus_pinned.join(',') }) : j.gpus_req ? t('jobs.wait_gpus', { n: j.gpus_req }) : t('gpu.none')
    return { text: `${pos} · ${waitText(j) || need}` }
  }
  if (j.status === 'running' && j.cancel_requested) return { text: t('jobs.cancelling_detail'), tone: 'text-warn' }
  const memo = j.memo?.split(/\r?\n/, 1)[0]?.trim()
  if (!isActive(j) && memo) {
    const why = j.status === 'failed' ? failText({ ...j, error_line: undefined, last_line: undefined }) : ''
    return { text: why ? `${why} · ${memo}` : memo, tone: why ? 'text-fail' : undefined }
  }
  if (j.status === 'failed') {
    // 手机上已经有报错行时不再写"退出码 N ·"：失败图标已经说明了状态，宽度留给报错行
    const exitOnly = exitCodeOnly(j)
    // 没有报错行时用的是最后一行输出；它是 tqdm 进度条的话，已用时间和速率对失败的任务没有用，去掉
    const text = j.error_line ? failText(j) : stripProgressTiming(failText(j))
    return { text, short: j.error_line && exitOnly ? j.error_line : undefined, tone: 'text-fail' }
  }
  if (j.pruned_at) return { text: t('jobs.pruned_detail') }
  if (j.last_line) {
    const p = compactProgress(j.last_line)
    const remaining = j.status === 'running' ? progressRemaining(j.last_line) : null
    return {
      // 剩余时间单独显示在前面，方括号里只去掉时间和速率，loss 这类指标留着
      text: remaining ? stripProgressTiming(p.text) : p.text,
      short: remaining ? stripProgressTiming(p.short) : p.short,
      mono: true,
      pct: j.status === 'running' ? p.pct : null,
      remaining,
    }
  }
  if (!j.started_at) return { text: t('jobs.not_started') }
  return { text: j.status === 'running' ? t('jobs.waiting_output') : t('jobs.no_output') }
}

/** 对比选择模式下这一行的勾选状态 */
export interface RowSelect {
  checked: boolean
  disabled: boolean
  onToggle(): void
}

/**
 * 一行任务。整行可点（名字是链接，用 ::after 铺满整行），commit、复制和取消操作浮在上面单独可点。
 * 1024px 以上是固定列，行高 40px。
 * 更窄时（手机，以及电脑上和 IDE 各占半屏的 768–1023px）折成两段，字段一个不少：
 * 固定列在 1024px 以下放不下最新输出和失败原因，820px 宽时那一列只剩几个像素。
 *   名字（放不下时折两行）和 commit；
 *   卡号、时间、无输出、剩余时间这些短标记，后面连着排说明文字（最新输出、失败原因、排队位置），一共最多两行。
 *   说明文字折行后占满整行，不会被卡号和时间挤成一条窄缝。
 * 手机上右侧只留一个 44px 的操作（在跑或排队的放取消，结束的放复制），标星只显示一个小星标。
 * 传 select 时是对比选择模式：整行变成勾选框的点击区，名字和 commit 不再是链接，点到行不会离开页面；
 * 复制按钮也不显示（点了会离开页面、丢掉已选的任务），但留出位置，列宽不跳。
 */
export function JobRow({ job: j, showCopy = false, select }: { job: Job; showCopy?: boolean; select?: RowSelect }) {
  const [confirming, setConfirming] = useState(false)
  const flash = useFlash(j)
  // 在跑和排队的每秒重画一次：时长、等待时长、无输出时长都在渲染时现算
  const at = useNow(isActive(j))
  const d = jobDuration(j)
  const gpus = j.gpus_assigned.length ? j.gpus_assigned : j.gpus_pinned
  const copy = showCopy && !select
  const info = detail(j)
  const ended = !isActive(j)
  const span = j.status === 'queued' ? duration(at - j.created_at) : d !== null ? duration(d) : ''
  // 手机上时间混在说明里，排队的要写"等"；电脑上是单独一列，排队图标和"第 N 位"已经说明在等，只写时长（英文带前缀会折行）
  const time = j.status === 'queued' ? t('jobs.waited', { duration: span }) : span
  const timeLabel = j.status === 'queued' ? t('jobs.time_waited') : j.status === 'running' ? t('jobs.time_running') : t('jobs.duration')
  const canCancel = isActive(j) && !j.cancel_requested
  // 排队任务行尾是调整队列的 ⋯ 菜单（取消也在里面），占在跑任务取消按钮的那一格
  const queueMenu = j.status === 'queued' && !select
  const code = select ? undefined : `/jobs/${j.id}?tab=code`
  const name = (
    <>
      <span className="shrink-0 text-ui text-fg-3">#{j.id}</span>
      {/* 暂缓的任务留在原位，名字退成灰色 */}
      <span className={cx('text-sm font-medium max-lg:line-clamp-2 max-lg:[overflow-wrap:anywhere] lg:truncate', j.status === 'queued' && j.held ? 'text-fg-3' : 'text-fg')}>{j.name}</span>
    </>
  )

  return (
    <div
      className={cx('group relative flex items-center gap-3 px-3 py-2.5 transition-colors duration-150 hover:bg-subtle sm:px-4', flash && 'animate-flash')}
      style={flash ? ({ '--flash': `var(--${flashTone[j.status] ?? 'run'})` } as CSSProperties) : undefined}
    >
      {select && (
        // 点击区用 ::after 铺满整行，键盘焦点框也画在整行上
        <label className="grid size-5 shrink-0 cursor-pointer place-items-center self-start after:absolute after:inset-0 has-disabled:cursor-not-allowed has-focus-visible:after:outline-2 has-focus-visible:after:-outline-offset-2 has-focus-visible:after:outline-focus lg:self-center">
          <input
            type="checkbox"
            className="size-4 focus-visible:outline-none disabled:opacity-40"
            aria-label={t('jobs.select_job_label', { id: j.id, name: j.name })}
            checked={select.checked}
            disabled={select.disabled}
            onChange={select.onToggle}
          />
        </label>
      )}
      <StatusIcon status={j.status} cancelling={j.cancel_requested} className="self-start mt-0.5 lg:mt-0 lg:self-center" />
      <div className="flex min-w-0 flex-1 flex-col gap-1 lg:flex-row lg:items-center lg:gap-4">
        {/* 带复制图标时（首页和任务页）从名称列调配宽度，保留卡号和最新输出列的空间；1280px 以下名称列收窄，让给失败原因和最新输出 */}
        <div className={cx('flex min-w-0 items-baseline gap-2 lg:shrink-0', showCopy ? 'lg:w-[184px] xl:w-[248px]' : 'lg:w-[216px] xl:w-[280px]')}>
          {select ? (
            <span title={j.name} className="flex min-w-0 items-baseline gap-2">
              {name}
            </span>
          ) : (
            <Link
              to={`/jobs/${j.id}`}
              title={j.name}
              data-job-link
              // scroll-my：用 J / K 移过来时，这一行不会停在粘性顶栏后面
              className="flex min-w-0 scroll-my-16 items-baseline gap-2 after:absolute after:inset-0 focus-visible:outline-none focus-visible:after:outline-2 focus-visible:after:-outline-offset-2 focus-visible:after:outline-focus"
            >
              {name}
            </Link>
          )}
          {j.starred && <Star role="img" aria-label={t('jobs.starred_label')} className="size-3 shrink-0 self-center fill-current text-fg-3 lg:hidden" />}
          <Commit sha={j.git_commit} dirty={j.git_dirty} to={code} className="relative z-10 ml-auto shrink-0 pl-2 lg:hidden" />
        </div>
        {/*
          电脑上是一行弹性列。手机上是行内流式排版：卡号、时间这些短标记和说明文字连着排，文字接在标记后面，
          折行后占满整行，一共最多两行。说明文字不会被标记挤成一条窄缝，行高也不比原来高。
          不能用 flex-1：line-clamp 带 overflow hidden，在纵向的弹性容器里会被压成 0 高
        */}
        <div className="min-w-0 text-xs max-lg:line-clamp-2 max-lg:[overflow-wrap:anywhere] lg:flex lg:flex-1 lg:items-center lg:gap-2">
          {gpus.length > 0 && <GpuChips ids={gpus} className="max-lg:-my-px max-lg:mr-2 max-lg:align-middle" />}
          {(time || ended) && (
            <span className="mr-2 whitespace-nowrap text-fg-3 lg:hidden" title={ended ? fmtTime(j.ended_at) : timeLabel}>
              {time && <span className="sr-only">{timeLabel} </span>}
              {[time, ended && j.ended_at ? timeAgo(j.ended_at) : ''].filter(Boolean).join(' · ')}
            </span>
          )}
          {j.status === 'running' && j.silent_since && (
            <span className="shrink-0 whitespace-nowrap text-warn max-lg:mr-2">{t('jobs.silent_for', { duration: duration(at - j.silent_since) })}</span>
          )}
          {info.pct != null && (
            <span className="w-10 shrink-0 max-lg:hidden" title={`${info.pct}%`}>
              <Meter value={info.pct} className="bg-run" />
            </span>
          )}
          {info.remaining && (
            <span
              className="shrink-0 whitespace-nowrap text-fg-2 max-lg:mr-2"
              title={t('jobs.remaining_title')}
            >
              {t('jobs.remaining', { remaining: info.remaining })}
            </span>
          )}
          <span title={info.text} className={cx('lg:min-w-0 lg:truncate', info.mono && 'font-mono', info.tone ?? 'text-fg-3')}>
            {info.short && info.short !== info.text ? (
              <>
                <span className="lg:hidden">{info.short}</span>
                <span className="max-lg:hidden">{info.text}</span>
              </>
            ) : (
              info.text
            )}
          </span>
        </div>
        {/* 列头对读屏是隐藏的，时长和结束时间各自带上说明 */}
        <div className="hidden shrink-0 items-center gap-4 text-xs text-fg-3 lg:flex">
          <span className="w-[5.5rem] text-right whitespace-nowrap" title={timeLabel}>
            {span && <span className="sr-only">{timeLabel} </span>}
            {span}
          </span>
          <span className="w-[4.5rem] text-right whitespace-nowrap" title={ended ? fmtTime(j.ended_at) : undefined}>
            {ended && (
              <>
                <span className="sr-only">{t('jobs.ended_at')} </span>
                {timeAgoShort(j.ended_at)}
              </>
            )}
          </span>
          <span className="w-[4.5rem]">
            <Commit sha={j.git_commit} dirty={j.git_dirty} to={code} className="relative z-10" />
          </span>
        </div>
      </div>
      {/* 操作按钮比一行字高，用负的上下外边距收回去，不撑高任务行：电脑上行高 40px */}
      <div className={cx('relative z-10 flex shrink-0 items-center max-lg:-my-1 max-lg:-mr-2 lg:-my-1.5', !copy && !canCancel && 'max-lg:hidden')}>
        <div className="max-lg:hidden">
          <StarButton job={j} />
        </div>
        {showCopy &&
          (copy ? (
            <Link
              to={`/new?copy=${j.id}`}
              className={cx(buttonClass('ghost', 'icon-sm'), 'text-fg-3 max-lg:size-11', canCancel && 'max-lg:hidden')}
              title={t('jobs.copy_job')}
              aria-label={t('jobs.copy_job_label', { id: j.id })}
            >
              <Copy aria-hidden />
            </Link>
          ) : (
            // 对比选择模式下只留位置，不让人点到复制把已选的任务丢掉
            <span className="hidden size-8 shrink-0 lg:block" />
          ))}
        {queueMenu ? (
          <QueueJobMenu
            job={j}
            onCancel={() => setConfirming(true)}
            // 电脑上和取消按钮一样悬停才出现；菜单展开时一直显示
            buttonClassName="lg:pointer-fine:opacity-0 lg:group-hover:opacity-100 lg:focus-visible:opacity-100 aria-expanded:opacity-100"
          />
        ) : canCancel ? (
          <Button
            size="icon-sm"
            variant="ghost"
            className="text-fg-3 hover:text-fail max-lg:size-11 lg:pointer-fine:opacity-0 lg:group-hover:opacity-100 lg:focus-visible:opacity-100"
            title={t('jobs.cancel_job')}
            aria-label={t('jobs.cancel_job_label', { id: j.id })}
            onClick={() => setConfirming(true)}
          >
            <X />
          </Button>
        ) : (
          <span className="hidden size-8 shrink-0 lg:block" />
        )}
      </div>
      {confirming && <CancelDialog job={j} onClose={() => setConfirming(false)} />}
    </div>
  )
}

/** 任务页电脑端的列头，列宽和 JobRow 的固定列一致 */
export function JobListHeader({ selecting = false, showCopy = false }: { selecting?: boolean; showCopy?: boolean }) {
  return (
    <div className="hidden h-8 items-center gap-3 border-b border-line bg-subtle px-4 text-xs text-fg-3 lg:flex" aria-hidden>
      {selecting && <span className="w-5 shrink-0" />}
      <span className="w-4 shrink-0" />
      <div className="flex min-w-0 flex-1 items-center gap-4">
        <span className={cx('shrink-0', showCopy ? 'w-[184px] xl:w-[248px]' : 'w-[216px] xl:w-[280px]')}>{t('jobs.col_job')}</span>
        <span className="min-w-0 flex-1">{t('jobs.col_output')}</span>
        <span className="w-[5.5rem] shrink-0 text-right">{t('jobs.duration')}</span>
        <span className="w-[4.5rem] shrink-0 text-right">{t('jobs.ended_at')}</span>
        <span className="w-[4.5rem] shrink-0">{t('jobs.col_code')}</span>
      </div>
      {/* 标星、复制、取消各 32px；没有复制时和行里的占位一起收窄 */}
      <span className={cx('h-8 shrink-0', showCopy ? 'w-24' : 'w-16')} />
    </div>
  )
}

/** 任务列表。读屏读成列表，能报出一共几条 */
export function JobList({ jobs, empty, header, showCopy = false, select }: { jobs: Job[]; empty: ReactNode; header?: ReactNode; showCopy?: boolean; select?: (job: Job) => RowSelect }) {
  if (!jobs.length) return typeof empty === 'string' ? <Empty>{empty}</Empty> : <>{empty}</>
  return (
    <>
      {header}
      <div role="list" className="divide-y divide-line">
        {jobs.map((j) => (
          <div role="listitem" key={j.id}>
            <JobRow job={j} showCopy={showCopy} select={select?.(j)} />
          </div>
        ))}
      </div>
    </>
  )
}
