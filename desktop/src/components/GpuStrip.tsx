import { mb, type GPU } from '@danlu/core'
import { t } from '../lib/i18n'
import type { ServerSnap } from '../lib/backend'
import { isOffline } from '../lib/fleet'
import { cx } from './ui'

const WINDOW_MS = 5 * 60_000

/** "5m 前"：离线服务器最后一次拿到数据的时间 */
export function shortAgo(ms: number, now = Date.now()) {
  const s = Math.max(0, Math.round((now - ms) / 1000))
  if (s < 60) return t('gpus.ago_s', { n: s })
  if (s < 3600) return t('gpus.ago_m', { n: Math.floor(s / 60) })
  if (s < 86400) return t('gpus.ago_h', { n: Math.floor(s / 3600) })
  return t('gpus.ago_d', { n: Math.floor(s / 86400) })
}

const unknownBusy = (g: GPU) => (g.unknown_mem_mb ?? 0) > 0
/** 卡上的外部进程或未知显存占用是需要注意的事；预约的卡上那是用户自己的任务，不算 */
export const externalBusy = (g: GPU) => !g.reservation && (g.external.length > 0 || unknownBusy(g))
export const freeCount = (gpus: GPU[]) => gpus.filter((g) => !g.job && !g.reservation && g.external.length === 0 && !unknownBusy(g)).length

/** 热力色：底色按利用率深浅着运行蓝；被外部进程占用的着琥珀色 */
export function heatStyle(g: GPU): React.CSSProperties {
  if (externalBusy(g)) return { background: 'color-mix(in srgb, var(--warn) 14%, var(--card))' }
  return { background: `color-mix(in srgb, var(--run) ${Math.round(4 + g.util * 0.3)}%, var(--card))` }
}

/** 空卡条：每台服务器一行，每张卡一个格子 */
export function GpuStrip({ servers, onServer }: { servers: ServerSnap[]; onServer?: (name: string) => void }) {
  return (
    <div className="space-y-1.5">
      {servers.map((s) => (
        <ServerRow key={s.name} snap={s} onServer={onServer} />
      ))}
    </div>
  )
}

function ServerRow({ snap: s, onServer }: { snap: ServerSnap; onServer?: (name: string) => void }) {
  const offline = isOffline(s)
  const gpus = s.gpus ?? []
  const sub = offline ? (s.updated_at ? shortAgo(s.updated_at) : t('gpus.offline')) : s.updated_at ? t('gpus.free_count', { n: freeCount(gpus) }) : t('gpus.connecting')
  return (
    // 连不上时保留最后一次拿到的状态，整排变淡
    <div className={cx('flex items-stretch gap-2 transition-opacity duration-300', offline && 'opacity-45')}>
      <button
        type="button"
        onClick={() => onServer?.(s.name)}
        className="w-[88px] shrink-0 rounded-[6px] px-1.5 py-1 text-left transition-colors duration-[120ms] hover:bg-hover"
        title={s.error ?? s.name}
      >
        <div className="truncate font-medium">{s.name}</div>
        <div className={cx('num text-[11px]', offline ? 'text-fail' : s.gpu_error ? 'text-warn' : 'text-fg-3')}>{s.gpu_error && !offline ? t('gpus.smi_error') : sub}</div>
      </button>
      {gpus.length === 0 ? (
        <div className="flex min-w-0 items-center truncate text-fg-3">{s.gpu_error ?? s.error ?? (s.updated_at ? t('gpus.no_gpu') : '')}</div>
      ) : (
        <div className="grid min-w-0 flex-1 grid-cols-[repeat(auto-fill,minmax(118px,1fr))] gap-1.5">
          {gpus.map((g) => (
            <GpuCell key={g.index} server={s.name} gpu={g} points={s.util.find((u) => u.index === g.index)?.points ?? []} />
          ))}
        </div>
      )}
    </div>
  )
}

function GpuCell({ server, gpu: g, points }: { server: string; gpu: GPU; points: [number, number][] }) {
  const reserved = !!g.reservation
  const external = g.external.length > 0 && !reserved
  const unknown = unknownBusy(g) && !reserved
  // 别的任务跑过来的进程按任务合并，外部进程按用户合并：EGL 渲染一个任务在一张卡上就能有几十个进程
  const strays = new Map<string, { n: number; pid: number; mem: number }>()
  for (const p of g.external) {
    const k = p.job_id ? t('gpus.proc_job', { key: `${server}-${p.job_id}` }) : t('gpus.proc_external', { user: p.user || '?' })
    const s = strays.get(k)
    strays.set(k, { n: (s?.n ?? 0) + 1, pid: s?.pid ?? p.pid, mem: (s?.mem ?? 0) + p.used_mem_mb })
  }
  const strayOwners = [...new Set(g.external.map((p) => (p.job_id ? `${server}-${p.job_id}` : p.user || `pid ${p.pid}`)))]
  const owner = g.job
    ? `${server}-${g.job.id}`
    : reserved ? t('gpus.reservation', { id: g.reservation!.id }) : external ? strayOwners.join(', ') : unknown ? t('gpus.unknown_mem') : t('gpus.idle')
  const title = [
    `GPU ${g.index} · ${g.name}`,
    t('gpus.card_title', { util: g.util, used: mb(g.mem_used_mb), total: mb(g.mem_total_mb), temp: g.temp_c, power: g.power_w }),
    g.job ? t('gpus.job_line', { key: `${server}-${g.job.id}`, name: g.job.name }) : '',
    reserved ? t('gpus.res_line', { id: g.reservation!.id, note: g.reservation!.note ? ` ${g.reservation!.note}` : '' }) : '',
    ...[...strays].map(([k, s]) => `${k} · ${s.n > 1 ? t('gpus.stray_count', { n: s.n }) : `pid ${s.pid}`} · ${mb(s.mem)}`),
    unknown ? t('gpus.unknown_line', { mem: mb(g.unknown_mem_mb ?? 0) }) : '',
  ]
    .filter(Boolean)
    .join('\n')
  return (
    <div
      title={title}
      style={heatStyle(g)}
      className={cx(
        'relative overflow-hidden rounded-[6px] px-2 pt-1.5 pb-1 transition-[background-color] duration-300',
        external || unknown ? 'shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--warn)_60%,transparent)]' : 'shadow-[inset_0_0_0_1px_var(--line)]',
      )}
    >
      <div className="flex items-baseline gap-1.5">
        <span className="num text-[11px] text-fg-3">{g.index}</span>
        <span className={cx('min-w-0 flex-1 truncate text-[11px]', g.job ? 'num text-fg' : external || unknown ? 'font-medium text-warn' : 'text-fg-3')}>{owner}</span>
        <span className="num text-[11.5px] font-medium">{g.util}%</span>
      </div>
      <Sparkline points={points} tone={external ? 'var(--warn)' : 'var(--run)'} />
      <div className="num text-[10.5px] leading-tight text-fg-3">
        {(g.mem_used_mb / 1024).toFixed(1)} / {Math.round(g.mem_total_mb / 1024)} GB
      </div>
    </div>
  )
}

/** 利用率小曲线：最近 5 分钟，数据由客户端轮询时积累，新数据从右边推入 */
function Sparkline({ points, tone }: { points: [number, number][]; tone: string }) {
  if (points.length < 2) return <div className="h-4" />
  const end = points[points.length - 1][0]
  const start = end - WINDOW_MS
  const x = (t: number) => ((t - start) / WINDOW_MS) * 100
  const y = (u: number) => 15 - (u / 100) * 13
  const d = points.map(([t, u], i) => `${i ? 'L' : 'M'}${x(t).toFixed(2)},${y(u).toFixed(2)}`).join('')
  const step = x(end) - x(points[points.length - 2][0])
  return (
    <div className="h-4 overflow-hidden">
      <svg
        key={end}
        viewBox="0 0 100 16"
        preserveAspectRatio="none"
        className="push block h-4 w-full"
        style={{ '--step': `${step}%` } as React.CSSProperties}
        aria-hidden
      >
        <path d={`${d}L${x(end).toFixed(2)},16L${x(points[0][0]).toFixed(2)},16Z`} fill={tone} opacity={0.12} />
        <path d={d} fill="none" stroke={tone} strokeWidth={1.25} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      </svg>
    </div>
  )
}

/** 托盘浮窗里的迷你热力色块 */
export function MiniCells({ gpus }: { gpus: GPU[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {gpus.map((g) => {
        const unknown = unknownBusy(g)
        const owner = g.job
          ? t('gpus.owner_job', { id: g.job.id })
          : g.reservation ? t('gpus.owner_res', { id: g.reservation.id }) : g.external.length ? t('gpus.owner_external') : !unknown ? t('gpus.owner_idle') : ''
        return (
          <div
            key={g.index}
            title={`GPU ${g.index} · ${g.util}%${owner}${unknown ? t('gpus.unknown_suffix', { mem: mb(g.unknown_mem_mb ?? 0) }) : ''}`}
            style={heatStyle(g)}
            className={cx(
              'num grid size-[22px] place-items-center rounded-[5px] text-[10px]',
              externalBusy(g)
                ? 'text-warn shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--warn)_60%,transparent)]'
                : g.job || g.reservation ? 'text-fg' : 'text-fg-3 shadow-[inset_0_0_0_1px_var(--line)]',
            )}
          >
            {g.index}
          </div>
        )
      })}
    </div>
  )
}
