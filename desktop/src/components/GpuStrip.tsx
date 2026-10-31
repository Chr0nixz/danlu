import { mb, type GPU } from '@danlu/core'
import type { ServerSnap } from '../lib/backend'
import { isOffline } from '../lib/fleet'
import { cx } from './ui'

const WINDOW_MS = 5 * 60_000

/** "5m 前"：离线服务器最后一次拿到数据的时间 */
export function shortAgo(ms: number, now = Date.now()) {
  const s = Math.max(0, Math.round((now - ms) / 1000))
  if (s < 60) return `${s}s 前`
  if (s < 3600) return `${Math.floor(s / 60)}m 前`
  if (s < 86400) return `${Math.floor(s / 3600)}h 前`
  return `${Math.floor(s / 86400)}d 前`
}

export const freeCount = (gpus: GPU[]) => gpus.filter((g) => !g.job && g.external.length === 0).length

/** 热力色：底色按利用率深浅着运行蓝；被外部进程占用的着琥珀色 */
export function heatStyle(g: GPU): React.CSSProperties {
  if (g.external.length > 0) return { background: 'color-mix(in srgb, var(--warn) 14%, var(--card))' }
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
  const sub = offline ? (s.updated_at ? shortAgo(s.updated_at) : '连不上') : s.updated_at ? `空 ${freeCount(gpus)}` : '连接中…'
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
        <div className={cx('num text-[11px]', offline ? 'text-fail' : s.gpu_error ? 'text-warn' : 'text-fg-3')}>{s.gpu_error && !offline ? 'nvidia-smi 报错' : sub}</div>
      </button>
      {gpus.length === 0 ? (
        <div className="flex min-w-0 items-center truncate text-fg-3">{s.gpu_error ?? s.error ?? (s.updated_at ? '没有 GPU' : '')}</div>
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
  const external = g.external.length > 0
  const owner = g.job ? `${server}-${g.job.id}` : external ? g.external.map((p) => p.user || `pid ${p.pid}`).join(', ') : '空闲'
  const title = [
    `GPU ${g.index} · ${g.name}`,
    `利用率 ${g.util}% · 显存 ${mb(g.mem_used_mb)} / ${mb(g.mem_total_mb)} · ${g.temp_c}°C · ${g.power_w}W`,
    g.job ? `任务 ${server}-${g.job.id} ${g.job.name}` : '',
    ...g.external.map((p) => `外部进程 ${p.user || '?'} · pid ${p.pid} · ${mb(p.used_mem_mb)}`),
  ]
    .filter(Boolean)
    .join('\n')
  return (
    <div
      title={title}
      style={heatStyle(g)}
      className={cx(
        'relative overflow-hidden rounded-[6px] px-2 pt-1.5 pb-1 transition-[background-color] duration-300',
        external ? 'shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--warn)_60%,transparent)]' : 'shadow-[inset_0_0_0_1px_var(--line)]',
      )}
    >
      <div className="flex items-baseline gap-1.5">
        <span className="num text-[11px] text-fg-3">{g.index}</span>
        <span className={cx('min-w-0 flex-1 truncate text-[11px]', g.job ? 'num text-fg' : external ? 'font-medium text-warn' : 'text-fg-3')}>{owner}</span>
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
      {gpus.map((g) => (
        <div
          key={g.index}
          title={`GPU ${g.index} · ${g.util}%${g.job ? ` · 任务 ${g.job.id}` : g.external.length ? ' · 外部进程' : ' · 空闲'}`}
          style={heatStyle(g)}
          className={cx(
            'num grid size-[22px] place-items-center rounded-[5px] text-[10px]',
            g.external.length ? 'text-warn shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--warn)_60%,transparent)]' : g.job ? 'text-fg' : 'text-fg-3 shadow-[inset_0_0_0_1px_var(--line)]',
          )}
        >
          {g.index}
        </div>
      ))}
    </div>
  )
}
