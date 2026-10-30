import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { api, type GPU } from '../lib/api'
import { mb } from '../lib/format'
import { Alert, Card, cx, ErrorBox, Meter, Skeleton, StatusIcon } from './ui'

export function useGpus() {
  return useQuery({ queryKey: ['gpus'], queryFn: api.gpus, refetchInterval: 3000 })
}

export const gpuBusy = (g: GPU) => !!g.job || g.external.length > 0

/** "NVIDIA GeForce RTX 4090" → "RTX 4090" */
export const shortName = (name: string) => name.replace(/^NVIDIA\s+/, '').replace(/^GeForce\s+/, '')

const gb = (used: number, total: number) => `${(used / 1024).toFixed(1)} / ${Math.round(total / 1024)} GB`

type State = 'job' | 'external' | 'idle'
const stateOf = (g: GPU): State => (g.job ? 'job' : g.external.length ? 'external' : 'idle')
const barColor: Record<State, string> = { job: 'bg-run', external: 'bg-warn', idle: 'bg-fg-4' }

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
    <div className="space-y-2">
      {data?.error && <Alert tone="warn">GPU 信息读取失败，暂停分配新任务：{data.error}</Alert>}
      {gpus.length === 0 && !data?.error && <Alert>没有检测到 GPU。只能提交不用 GPU 的任务。</Alert>}

      {/* 手机：每张卡一行 */}
      <Card className="divide-y divide-line overflow-hidden sm:hidden">
        {gpus.map((g) => (
          <GpuRow key={g.index} g={g} showModel={mixed} />
        ))}
      </Card>

      {/* 电脑：每张卡一个面板 */}
      <div className="hidden gap-3 sm:grid sm:grid-cols-2 lg:grid-cols-4">
        {gpus.map((g) => (
          <GpuPanel key={g.index} g={g} showModel={mixed} />
        ))}
      </div>
    </div>
  )
}

function Occupant({ g }: { g: GPU }) {
  return (
    <div className="min-w-0 space-y-0.5 text-ui">
      {g.job && (
        <Link to={`/jobs/${g.job.id}`} className="flex min-w-0 items-center gap-1.5 text-fg hover:underline">
          <StatusIcon status="running" className="size-3.5" />
          <span className="shrink-0 text-fg-3">#{g.job.id}</span>
          <span className="truncate font-medium">{g.job.name}</span>
        </Link>
      )}
      {g.external.map((p) => (
        <div key={p.pid} className="flex min-w-0 items-center gap-1.5 text-warn" title="不是 danlu 启动的进程，这张卡不会被分配">
          <span className="size-1.5 shrink-0 rounded-full bg-warn" />
          <span className="truncate">
            外部进程 {p.pid}
            {p.user && ` · ${p.user}`} · {mb(p.used_mem_mb)}
          </span>
        </div>
      ))}
      {!g.job && !g.external.length && (
        <div className="flex items-center gap-1.5 text-ok">
          <span className="size-1.5 rounded-full bg-ok" />
          空闲
        </div>
      )}
    </div>
  )
}

function GpuPanel({ g, showModel }: { g: GPU; showModel: boolean }) {
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
      <div className="mt-2.5 grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between text-xs">
            <span className="text-fg-3">利用率</span>
            <span className="font-medium text-fg">{g.util}%</span>
          </div>
          <Meter value={g.util} className={barColor[s]} />
        </div>
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-1 text-xs">
            <span className="text-fg-3">显存</span>
            <span className="truncate font-medium text-fg">{gb(g.mem_used_mb, g.mem_total_mb)}</span>
          </div>
          <Meter value={memPct} className={barColor[s]} />
        </div>
      </div>
      <div className="mt-3 border-t border-line pt-2">
        <Occupant g={g} />
      </div>
    </Card>
  )
}

function GpuRow({ g, showModel }: { g: GPU; showModel: boolean }) {
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
        <Occupant g={g} />
        {showModel && <span className="ml-auto shrink-0 truncate text-xs text-fg-3">{shortName(g.name)}</span>}
      </div>
    </div>
  )
}
