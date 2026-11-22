import { useQuery } from '@tanstack/react-query'
import { Lock } from 'lucide-react'
import { Link } from 'react-router'
import { apiErrorText, getLocale, mb, type GPU, type GPUReservationRef, type Proc } from '@danlu/core'
import { t } from '../lib/i18n'
import { api } from '../lib/api'
import { Alert, Card, cx, ErrorBox, Meter, Skeleton, StatusIcon } from './ui'

export function useGpus() {
  return useQuery({ queryKey: ['gpus'], queryFn: api.gpus, refetchInterval: 3000 })
}

export const gpuBusy = (g: GPU) => !!g.job || !!g.reservation || g.external.length > 0 || (g.unknown_mem_mb ?? 0) > 0

/** "15:30"；不在今天时带上日期。按浏览器时区显示，服务端只给时间戳 */
export function clock(ts: number) {
  const d = new Date(ts * 1000)
  const tm = d.toLocaleTimeString(getLocale(), { hour: '2-digit', minute: '2-digit', hour12: false })
  return d.toDateString() === new Date().toDateString() ? tm : `${d.getMonth() + 1}/${d.getDate()} ${tm}`
}

/** GPU 卡片上预约那一行的短说明：收卡中 / 到 15:30 / 手动释放 */
export const reservationState = (r: GPUReservationRef) =>
  r.status === 'waiting' ? t('gpus.res_claiming') : r.ends_at ? t('gpus.res_until', { time: clock(r.ends_at) }) : t('gpus.res_manual')

/** 预约那一行：灰色加锁，不用状态色；用途放在最后，窄的时候先截掉它 */
export function ReservationLine({ r, className }: { r: GPUReservationRef; className?: string }) {
  return (
    <span className={cx('flex min-w-0 items-center gap-1.5 text-fg-2', className)}
      title={r.note ? t('gpus.res_line_title_note', { id: r.id, note: r.note }) : t('gpus.res_line_title', { id: r.id })}>
      <Lock className="size-3 shrink-0" aria-hidden />
      <span className="shrink-0">{t('gpus.reservation_label', { id: r.id })} · {reservationState(r)}</span>
      {r.note && <span className="truncate text-fg-3">· {r.note}</span>}
    </span>
  )
}

/** 占着别人卡的进程叫什么：别的任务跑过来的进程写任务号，其余是外部进程 */
export const strayLabel = (p: Proc) =>
  p.job_id ? t(p.graphics ? 'gpus.proc_job_graphics' : 'gpus.proc_job', { id: p.job_id }) : t(p.graphics ? 'gpus.proc_external_graphics' : 'gpus.proc_external')

type Stray = { key: string; job: number; user: string; procs: Proc[]; mem: number }

/** 按任务（外部进程按用户）合并：EGL 渲染一个任务在一张卡上就能有几十个进程。资源页的 GPU 行复用同一套叙事 */
export function groupStray(procs: Proc[]): Stray[] {
  const out: Stray[] = []
  for (const p of procs) {
    const key = p.job_id ? `job:${p.job_id}` : `user:${p.user}`
    let s = out.find((x) => x.key === key)
    if (!s) out.push((s = { key, job: p.job_id, user: p.user, procs: [], mem: 0 }))
    s.procs.push(p)
    s.mem += p.used_mem_mb
  }
  return out
}

/** tooltip 的前半段（pid 列表、用户、图形进程数）；预约卡版本复用它，不去截已本地化的整句 */
export function strayHead(s: Stray) {
  const pids = s.procs.map((p) => p.pid)
  const list = pids.slice(0, 12).join(', ') + (pids.length > 12 ? ' …' : '')
  const graphics = s.procs.filter((p) => p.graphics).length
  // 任务进程启动时的选卡变量：和分配的卡对不上时，一眼就知道是启动器或脚本改写了它
  const cvd = s.job ? s.procs.find((p) => !p.graphics && p.cuda_visible !== undefined)?.cuda_visible : undefined
  const env = cvd === undefined ? '' : cvd ? t('gpus.stray_cvd', { cvd }) : t('gpus.stray_cvd_unset')
  const kinds = (graphics ? t('gpus.stray_graphics', { n: graphics }) : '') + env
  return s.job
    ? t('gpus.stray_job_head', { job: s.job, pids: list, kinds })
    : t('gpus.stray_external_head', { pids: list, user: s.user ? t('gpus.stray_user', { user: s.user }) : '', kinds })
}

export function strayTitle(s: Stray) {
  const head = strayHead(s)
  return s.job ? t('gpus.stray_job_title', { head }) : t('gpus.stray_external_title', { head })
}

/** "NVIDIA GeForce RTX 4090" → "RTX 4090" */
export const shortName = (name: string) => name.replace(/^NVIDIA\s+/, '').replace(/^GeForce\s+/, '')

const gb = (used: number, total: number) => `${(used / 1024).toFixed(1)} / ${Math.round(total / 1024)} GB`

function errorDuration(since: number) {
  const seconds = Math.max(0, Math.floor(Date.now() / 1000 - since))
  if (seconds < 60) return t('gpus.error_duration_s', { n: seconds })
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return t('gpus.error_duration_m', { n: minutes })
  const hours = Math.floor(minutes / 60)
  return t('gpus.error_duration_hm', { h: hours, m: minutes % 60 })
}

type State = 'job' | 'reserved' | 'external' | 'unknown' | 'idle'
const stateOf = (g: GPU): State =>
  g.job ? 'job' : g.reservation ? 'reserved' : g.external.length ? 'external' : (g.unknown_mem_mb ?? 0) > 0 ? 'unknown' : 'idle'
// 预约的卡不是任务状态，用比空闲深一级的灰
const barColor: Record<State, string> = { job: 'bg-run', reserved: 'bg-fg-3', external: 'bg-warn', unknown: 'bg-warn', idle: 'bg-fg-4' }

export function GpuGrid() {
  const { data, isLoading, error } = useGpus()
  if (isLoading) {
    return (
      <div className="grid gap-2 sm:grid-cols-2 sm:gap-3 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-14 rounded-lg sm:h-[124px]" />
        ))}
      </div>
    )
  }
  if (error) return <ErrorBox error={t('gpus.load_failed', { message: apiErrorText(error) })} />
  const gpus = data?.gpus ?? []
  const models = new Set(gpus.map((g) => g.name))
  const mixed = models.size > 1

  return (
    <section aria-labelledby="gpu-heading" className="space-y-2">
      <h2 id="gpu-heading" className="sr-only">GPU</h2>
      {data?.error && (
        <Alert tone="warn">
          {t('gpus.sample_failed', { error: data.error })}
          {data.error_since && <span className="ml-1">{t('gpus.error_since', { duration: errorDuration(data.error_since) })}</span>}
        </Alert>
      )}
      {gpus.length === 0 && !data?.error && <Alert>{t('gpus.none_detected')}</Alert>}

      {/* 手机：每张卡一行 */}
      <Card className="divide-y divide-line overflow-hidden sm:hidden">
        {gpus.map((g) => (
          <GpuRow key={g.index} g={g} showModel={mixed} procsKnown={!data?.error} />
        ))}
      </Card>

      {/* 电脑：每张卡一个面板 */}
      <div className="hidden gap-3 sm:grid sm:grid-cols-2 lg:grid-cols-4">
        {gpus.map((g) => (
          <GpuPanel key={g.index} g={g} showModel={mixed} procsKnown={!data?.error} />
        ))}
      </div>
    </section>
  )
}

// procsKnown：这一轮查到了卡上的进程。查不到时不能说任务没在用这张卡
function Occupant({ g, procsKnown }: { g: GPU; procsKnown: boolean }) {
  return (
    <div className="min-w-0 space-y-0.5 text-ui">
      {g.job && (
        // 手机上把点击区撑高到 44px，不改变排版
        <Link to={`/jobs/${g.job.id}`} title={g.job.name} className="flex min-w-0 items-center gap-1.5 text-fg hover:underline max-sm:-my-3 max-sm:py-3">
          <StatusIcon status="running" className="size-3.5" />
          <span className="shrink-0 text-fg-3">#{g.job.id}</span>
          <span className="truncate font-medium">{g.job.name}</span>
        </Link>
      )}
      {g.job && procsKnown && !g.procs.some((p) => p.job_id === g.job!.id) && (
        <div className="text-xs text-fg-3" title={t('gpus.no_procs_title')}>
          {t('gpus.no_procs')}
        </div>
      )}
      {g.reservation && <ReservationLine r={g.reservation} />}
      {groupStray(g.external).map((s) => {
        const one = s.procs.length === 1 ? s.procs[0] : null
        // 预约的卡上的外部进程多半是用户自己的任务，不算需要注意的事
        const expected = !!g.reservation && !s.job
        const text = (
          <>
            <span className={cx('size-1.5 shrink-0 rounded-full', expected ? 'bg-fg-3' : 'bg-warn')} aria-hidden />
            <span className="truncate">
              {one ? `${strayLabel(one)} ${one.pid}` : s.job ? t('gpus.job_proc_count', { job: s.job, n: s.procs.length }) : t('gpus.external_proc_count', { n: s.procs.length })}
              {!s.job && s.user && ` · ${s.user}`}
            </span>
            {/* 显存放在截断范围外，窄的时候宁可截掉用户名 */}
            <span className="shrink-0">· {mb(s.mem)}</span>
          </>
        )
        // 本实例任务的进程跑到了没分给它的卡上：程序自己选了卡、渲染落到了别的卡，或者是已结束任务的残留
        return s.job ? (
          <Link key={s.key} to={`/jobs/${s.job}`} className="flex min-w-0 items-center gap-1.5 text-warn hover:underline" title={strayTitle(s)}>
            {text}
          </Link>
        ) : (
          <div key={s.key} className={cx('flex min-w-0 items-center gap-1.5', expected ? 'text-fg-2' : 'text-warn')}
            title={expected ? t('gpus.stray_reserved_title', { head: strayHead(s) }) : strayTitle(s)}>
            {text}
          </div>
        )
      })}
      {(g.unknown_mem_mb ?? 0) > 0 && (
        <div
          className="flex items-center gap-1.5 text-warn"
          title={t('gpus.unknown_mem_title')}
        >
          <span className="size-1.5 shrink-0 rounded-full bg-warn" />
          {t('gpus.unknown_mem')} · {mb(g.unknown_mem_mb ?? 0)}
        </div>
      )}
      {!g.job && !g.reservation && !g.external.length && !(g.unknown_mem_mb ?? 0) && (
        <div className="flex items-center gap-1.5 text-ok">
          <span className="size-1.5 rounded-full bg-ok" />
          {t('gpus.idle')}
        </div>
      )}
    </div>
  )
}

function GpuPanel({ g, showModel, procsKnown }: { g: GPU; showModel: boolean; procsKnown: boolean }) {
  const s = stateOf(g)
  const memPct = g.mem_total_mb ? (g.mem_used_mb / g.mem_total_mb) * 100 : 0
  return (
    <Card className="flex flex-col p-3">
      <div className="flex items-baseline gap-2">
        <span className="text-ui font-semibold">GPU {g.index}</span>
        <span className="min-w-0 flex-1 truncate text-xs text-fg-3" title={g.name}>
          {showModel || !g.name ? shortName(g.name) : ''}
        </span>
        <span className="shrink-0 text-xs text-fg-3">
          {g.temp_c}°C · {Math.round(g.power_w)}W
        </span>
      </div>
      {/* 显存的数值（20.8 / 24 GB）比利用率长，按 2:3 分宽度：1024px 下每块面板只有 220px 上下，对半分会把"显存"折成两行 */}
      <div className="mt-2.5 grid grid-cols-[2fr_3fr] gap-3">
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between text-xs">
            <span className="text-fg-3">{t('gpus.util')}</span>
            <span className="font-medium text-fg">{g.util}%</span>
          </div>
          <Meter value={g.util} className={barColor[s]} />
        </div>
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-1 text-xs">
            <span className="shrink-0 text-fg-3">{t('gpus.mem')}</span>
            <span className="truncate font-medium text-fg">{gb(g.mem_used_mb, g.mem_total_mb)}</span>
          </div>
          <Meter value={memPct} className={barColor[s]} />
        </div>
      </div>
      <div className="mt-3 border-t border-line pt-2">
        <Occupant g={g} procsKnown={procsKnown} />
      </div>
    </Card>
  )
}

function GpuRow({ g, showModel, procsKnown }: { g: GPU; showModel: boolean; procsKnown: boolean }) {
  const s = stateOf(g)
  return (
    <div className="px-3 py-2.5">
      <div className="flex items-center gap-3 text-ui">
        <span className="w-12 shrink-0 font-semibold">GPU {g.index}</span>
        <div className="flex w-[4.5rem] shrink-0 items-center gap-1.5">
          <div className="flex-1">
            <Meter value={g.util} className={barColor[s]} />
          </div>
        </div>
        <span className="w-9 shrink-0 text-right font-medium">{g.util}%</span>
        <span className="min-w-0 flex-1 truncate text-right text-xs text-fg-2">{gb(g.mem_used_mb, g.mem_total_mb)}</span>
        <span className="w-9 shrink-0 text-right text-xs text-fg-3">{g.temp_c}°</span>
      </div>
      <div className={cx('mt-1 flex items-center gap-2 pl-[3.75rem]')}>
        <Occupant g={g} procsKnown={procsKnown} />
        {showModel && <span className="ml-auto shrink-0 truncate text-xs text-fg-3">{shortName(g.name)}</span>}
      </div>
    </div>
  )
}
