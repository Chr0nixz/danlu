import { useQuery } from '@tanstack/react-query'
import { Cpu, MemoryStick } from 'lucide-react'
import type { ReactNode } from 'react'
import { now, type HostSample } from '@danlu/core'
import { api } from '../lib/api'
import { Card, cx, Meter, Skeleton } from './ui'

export function useHostMetrics() {
  return useQuery({ queryKey: ['host-metrics'], queryFn: api.hostMetrics, refetchInterval: 3000 })
}

export function HostMetrics() {
  const { data, isLoading, error } = useHostMetrics()
  const samples = data?.samples ?? []
  const sample = samples.at(-1)
  const age = data ? Math.max(0, now() - data.updated_at) : 0
  const stale = data && age > 15

  return (
    <Card className="overflow-hidden">
      <div className="flex h-9 items-center gap-2 border-b border-line bg-subtle px-3 text-ui sm:px-4">
        <h2 className="font-medium">主机资源</h2>
        <span className={cx('ml-auto text-xs', stale ? 'text-warn' : 'text-fg-3')}>
          {stale ? `数据 ${Math.round(age)} 秒没有更新` : samples.length >= 31 ? '近 1 分钟 · 每 2 秒采样' : samples.length ? `采样中 ${samples.length}/31` : '每 2 秒采样'}
        </span>
      </div>
      {error ? (
        <div role="alert" className="px-3 py-3 text-ui text-fail sm:px-4">读取 CPU 和内存失败：{(error as Error).message}</div>
      ) : data?.error ? (
        <div role="alert" className="px-3 py-3 text-ui text-warn sm:px-4">CPU 和内存数据暂不可用：{data.error}</div>
      ) : isLoading ? (
        <div className="grid grid-cols-2 divide-x divide-line p-3 sm:p-4">
          <Skeleton className="mr-3 h-[112px] sm:mr-4" />
          <Skeleton className="ml-3 h-[112px] sm:ml-4" />
        </div>
      ) : (
        <div className="grid grid-cols-2 divide-x divide-line">
          <Metric
            title="CPU"
            icon={<Cpu className="size-4" aria-hidden />}
            value={sample?.cpu_percent == null ? '—' : `${sample.cpu_percent}%`}
            percent={sample?.cpu_percent}
            samples={samples}
            getValue={(item) => item.cpu_percent}
            emptyLabel="等待 CPU 数据"
          />
          <Metric
            title="内存"
            icon={<MemoryStick className="size-4" aria-hidden />}
            value={sample ? `${sample.memory_percent}%` : '—'}
            detail={sample ? `${gib(sample.memory_used_bytes)} / ${gib(sample.memory_total_bytes)} GiB` : undefined}
            percent={sample?.memory_percent}
            samples={samples}
            getValue={(item) => item.memory_percent}
            emptyLabel="等待内存数据"
          />
        </div>
      )}
    </Card>
  )
}

function Metric({ title, icon, value, percent, detail, samples, getValue, emptyLabel }: {
  title: string
  icon: ReactNode
  value: string
  percent?: number | null
  detail?: string
  samples: HostSample[]
  getValue: (sample: HostSample) => number | null
  emptyLabel: string
}) {
  const values = samples.map(getValue)
  const hasValues = values.some((v) => v != null)
  return (
    <section className="min-w-0 px-2.5 py-2.5 sm:px-4">
      <div className="flex min-h-7 items-center gap-1.5 sm:gap-2">
        <span className="text-fg-3">{icon}</span>
        <h3 className="text-ui font-medium">{title}</h3>
        <span className="ml-auto font-mono text-base font-semibold tabular-nums sm:text-lg">{value}</span>
      </div>
      {detail && <div className="mb-1 truncate text-right text-xs text-fg-3" title={detail}>{detail}</div>}
      {percent == null ? <div className="mt-2 text-xs text-fg-3">{emptyLabel}</div> : <Meter value={percent} className="mt-2 bg-fg-3" />}
      <div className="mt-2 grid grid-cols-[2rem_minmax(0,1fr)] gap-1.5">
        <div className="flex h-[56px] flex-col justify-between text-[11px] leading-none text-fg-3" aria-hidden>
          <span>100%</span><span>50%</span><span>0</span>
        </div>
        {hasValues ? <HistoryChart values={values} label={`${title} 最近 1 分钟使用率`} /> : <div className="flex h-[56px] items-center text-xs text-fg-3">{emptyLabel}</div>}
      </div>
      <div className="ml-[2.5rem] mt-1 flex justify-between text-[11px] leading-none text-fg-3">
        <span>{samples.length >= 31 ? '60 秒前' : '开始采样'}</span><span>现在</span>
      </div>
    </section>
  )
}

function HistoryChart({ values, label }: { values: (number | null)[]; label: string }) {
  const width = 320
  const top = 1
  const height = 52
  const usable = values
    .map((value, index) => ({ value, index }))
    .filter((point): point is { value: number; index: number } => point.value != null)
  const path = usable
    .map(({ value, index }, n) => {
      const x = (index / Math.max(1, values.length - 1)) * width
      const y = top + (1 - Math.max(0, Math.min(100, value)) / 100) * height
      return `${n ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
  const latest = usable.at(-1)
  const latestX = latest ? (latest.index / Math.max(1, values.length - 1)) * width : 0
  const latestY = latest ? top + (1 - Math.max(0, Math.min(100, latest.value)) / 100) * height : 0

  return (
    <svg viewBox="0 0 320 56" preserveAspectRatio="none" role="img" aria-label={label} className="h-[56px] w-full overflow-visible">
      {[top, top + height / 2, top + height].map((y) => (
        <line key={y} x1="0" x2={width} y1={y} y2={y} stroke="var(--line)" strokeWidth="1" />
      ))}
      {path && <path d={path} fill="none" stroke="var(--fg-2)" strokeWidth="2" vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />}
      {latest && <circle cx={latestX} cy={latestY} r="3" fill="var(--fg)" />}
    </svg>
  )
}

function gib(bytes: number) {
  return (bytes / 1024 ** 3).toFixed(1)
}
