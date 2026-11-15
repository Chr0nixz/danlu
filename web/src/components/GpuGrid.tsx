import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { mb, type GPU, type Proc } from '@danlu/core'
import { api } from '../lib/api'
import { Alert, Card, cx, ErrorBox, Meter, Skeleton, StatusIcon } from './ui'

export function useGpus() {
  return useQuery({ queryKey: ['gpus'], queryFn: api.gpus, refetchInterval: 3000 })
}

export const gpuBusy = (g: GPU) => !!g.job || g.external.length > 0 || (g.unknown_mem_mb ?? 0) > 0

/** 占着别人卡的进程叫什么：别的任务跑过来的进程写任务号，其余是外部进程 */
export const strayLabel = (p: Proc) => `${p.job_id ? `#${p.job_id} 的` : '外部'}${p.graphics ? '图形' : ''}进程`

type Stray = { key: string; job: number; user: string; procs: Proc[]; mem: number }

/** 按任务（外部进程按用户）合并：EGL 渲染一个任务在一张卡上就能有几十个进程 */
function groupStray(procs: Proc[]): Stray[] {
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

function strayTitle(s: Stray) {
  const pids = s.procs.map((p) => p.pid)
  const list = pids.slice(0, 12).join(', ') + (pids.length > 12 ? ' …' : '')
  const graphics = s.procs.filter((p) => p.graphics).length
  const kinds = graphics
    ? `，其中 ${graphics} 个是图形进程（EGL/OpenGL 渲染，按自己的设备编号选卡，不受 CUDA_VISIBLE_DEVICES 限制）`
    : ''
  return s.job
    ? `任务 #${s.job} 的进程（pid ${list}${kinds}），但这张卡没有分给它：程序自己选了卡，或是已结束任务的残留。这张卡不会被分配`
    : `外部进程（pid ${list}${s.user ? `，用户 ${s.user}` : ''}${kinds}）：不是 danlu 启动的，这张卡不会被分配`
}

/** "NVIDIA GeForce RTX 4090" → "RTX 4090" */
export const shortName = (name: string) => name.replace(/^NVIDIA\s+/, '').replace(/^GeForce\s+/, '')

const gb = (used: number, total: number) => `${(used / 1024).toFixed(1)} / ${Math.round(total / 1024)} GB`

function errorDuration(since: number) {
  const seconds = Math.max(0, Math.floor(Date.now() / 1000 - since))
  if (seconds < 60) return `${seconds} 秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  return `${hours} 小时 ${minutes % 60} 分钟`
}

type State = 'job' | 'external' | 'unknown' | 'idle'
const stateOf = (g: GPU): State => (g.job ? 'job' : g.external.length ? 'external' : (g.unknown_mem_mb ?? 0) > 0 ? 'unknown' : 'idle')
const barColor: Record<State, string> = { job: 'bg-run', external: 'bg-warn', unknown: 'bg-warn', idle: 'bg-fg-4' }

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
  if (error) return <ErrorBox error={`读取 GPU 失败：${(error as Error).message}`} />
  const gpus = data?.gpus ?? []
  const models = new Set(gpus.map((g) => g.name))
  const mixed = models.size > 1

  return (
    <section aria-labelledby="gpu-heading" className="space-y-2">
      <h2 id="gpu-heading" className="sr-only">GPU</h2>
      {data?.error && (
        <Alert tone="warn">
          GPU 信息读取失败，暂停分配新任务：{data.error}
          {data.error_since && <span className="ml-1">（已持续 {errorDuration(data.error_since)}）</span>}
        </Alert>
      )}
      {gpus.length === 0 && !data?.error && <Alert>没有检测到 GPU。只能提交不用 GPU 的任务。</Alert>}

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
        <div className="text-xs text-fg-3" title="这张卡分给了这个任务，但卡上还没有它的进程：可能还在启动，也可能程序没有用这张卡">
          卡上暂无该任务的进程
        </div>
      )}
      {groupStray(g.external).map((s) => {
        const one = s.procs.length === 1 ? s.procs[0] : null
        const text = (
          <>
            <span className="size-1.5 shrink-0 rounded-full bg-warn" aria-hidden />
            <span className="truncate">
              {one ? `${strayLabel(one)} ${one.pid}` : s.job ? `#${s.job} 的 ${s.procs.length} 个进程` : `${s.procs.length} 个外部进程`}
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
          <div key={s.key} className="flex min-w-0 items-center gap-1.5 text-warn" title={strayTitle(s)}>
            {text}
          </div>
        )
      })}
      {(g.unknown_mem_mb ?? 0) > 0 && (
        <div
          className="flex items-center gap-1.5 text-warn"
          title="nvidia-smi 列出的进程解释不了这部分显存（比如别的容器里的进程），这张卡不会被分配"
        >
          <span className="size-1.5 shrink-0 rounded-full bg-warn" />
          未知显存占用 · {mb(g.unknown_mem_mb ?? 0)}
        </div>
      )}
      {!g.job && !g.external.length && !(g.unknown_mem_mb ?? 0) && (
        <div className="flex items-center gap-1.5 text-ok">
          <span className="size-1.5 rounded-full bg-ok" />
          空闲
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
            <span className="text-fg-3">利用率</span>
            <span className="font-medium text-fg">{g.util}%</span>
          </div>
          <Meter value={g.util} className={barColor[s]} />
        </div>
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-1 text-xs">
            <span className="shrink-0 text-fg-3">显存</span>
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
