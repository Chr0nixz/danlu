import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Cpu, HardDrive, MemoryStick } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { apiErrorText, now, type HostMetricsResponse, type HostSample } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
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
      {/* 手机上标题栏 44px 高，这样右侧的「资源详情」整条都是可点区域；桌面仍是 36px */}
      <div className="flex h-9 items-center gap-2 border-b border-line bg-subtle px-3 text-ui max-sm:h-11 sm:px-4">
        <h2 className="font-medium">{t('resources.host_title')}</h2>
        <span className={cx('ml-auto min-w-0 truncate text-xs', stale ? 'text-warn' : 'text-fg-3')}>
          {stale ? t('resources.data_stale', { n: Math.round(age) }) : samples.length >= 31 ? t('resources.host_recent') : samples.length ? t('resources.host_sampling', { n: samples.length }) : t('resources.host_every_2s')}
        </span>
        <Link to="/resources" className="-mr-2 flex h-full min-w-9 shrink-0 items-center justify-center gap-1 px-2 text-xs text-fg-3 hover:text-fg max-sm:min-w-11" title={t('resources.host_details_title')}>
          <span className="max-sm:sr-only">{t('resources.host_details')}</span>
          <ArrowRight className="size-3.5" aria-hidden />
        </Link>
      </div>
      {error ? (
        <div role="alert" className="px-3 py-3 text-ui text-fail sm:px-4">{t('resources.host_query_failed', { error: apiErrorText(error) })}</div>
      ) : data?.error ? (
        <div role="alert" className="px-3 py-3 text-ui text-warn sm:px-4">{t('resources.host_unavailable', { error: data.error })}</div>
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
            emptyLabel={t('resources.wait_cpu')}
          />
          <Metric
            title={t('resources.memory')}
            icon={<MemoryStick className="size-4" aria-hidden />}
            value={sample ? `${sample.memory_percent}%` : '—'}
            detail={sample ? `${gib(sample.memory_used_bytes)} / ${gib(sample.memory_total_bytes)} GiB` : undefined}
            percent={sample?.memory_percent}
            samples={samples}
            getValue={(item) => item.memory_percent}
            emptyLabel={t('resources.wait_mem')}
          />
        </div>
      )}
      {data && (data.disks !== undefined || data.disks_error) && <DiskMetrics data={data} />}
    </Card>
  )
}

function DiskMetrics({ data }: { data: HostMetricsResponse }) {
  return (
    <section className="border-t border-line text-xs">
      <div className="flex h-8 items-center gap-1.5 bg-subtle px-3 sm:px-4">
        <HardDrive className="size-3.5 text-fg-3" aria-hidden />
        <h3 className="font-medium">{t('resources.disk_space_title')}</h3>
        <span className="ml-auto text-fg-3">{t('resources.disk_merge_note')}</span>
      </div>
      {data.disks_error && <p role="alert" className="px-3 py-2 text-warn sm:px-4">{t('resources.disk_unavailable', { error: data.disks_error })}</p>}
      <div className="divide-y divide-line">
        {data.disks?.map((disk, index) => (
          <div key={disk.id || `${disk.path}:${index}`} className="flex flex-wrap items-start gap-x-3 gap-y-1 px-3 py-2 sm:px-4">
            <div className="min-w-0 flex-1 space-y-0.5 max-sm:basis-full">
              {disk.paths.map((path) => <div key={path} className="font-mono [overflow-wrap:anywhere]">{path}</div>)}
              {disk.data && <span className="text-fg-3">{t('resources.data_dir_partition')}</span>}
            </div>
            <div className={cx('ml-auto shrink-0 text-right font-mono tabular-nums max-sm:flex max-sm:w-full max-sm:items-baseline max-sm:justify-between max-sm:gap-2 max-sm:text-left', disk.warn || disk.error ? 'text-warn' : 'text-fg-2')}>
              {disk.error ? <span className="font-sans">{t('resources.disk_scan_failed')}</span> : <span><span>{t('resources.disk_available', { value: gib(disk.available_bytes) })}</span><span className="ml-2 text-fg-3">/ {gib(disk.total_bytes)} GiB</span></span>}
              {disk.warn && !disk.error && <div className="font-sans">{t('resources.low_space')}</div>}
            </div>
            {disk.error && <p role="alert" className="w-full break-words text-warn">{disk.error}</p>}
          </div>
        ))}
      </div>
      {!data.disks?.length && !data.disks_error && <p className="px-3 py-2 text-fg-3 sm:px-4">{t('resources.disk_scanning')}</p>}
    </section>
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
      {/* 只有内存有这行说明；CPU 也留出同样的高度，两栏的细条和曲线才在同一条线上 */}
      <div className="mb-1 h-4 truncate text-right text-xs text-fg-3" title={detail}>{detail}</div>
      {percent == null ? <div className="mt-2 text-xs text-fg-3">{emptyLabel}</div> : <Meter value={percent} className="mt-2 bg-fg-3" />}
      <div className="mt-2 grid grid-cols-[2rem_minmax(0,1fr)] gap-1.5">
        <div className="flex h-[56px] flex-col justify-between text-[11px] leading-none text-fg-3" aria-hidden>
          <span>100%</span><span>50%</span><span>0</span>
        </div>
        {hasValues ? <HistoryChart values={values} label={t('resources.host_chart_aria', { name: title })} /> : <div className="flex h-[56px] items-center text-xs text-fg-3">{emptyLabel}</div>}
      </div>
      <div className="ml-[2.5rem] mt-1 flex justify-between text-[11px] leading-none text-fg-3">
        <span>{samples.length >= 31 ? t('resources.host_axis_60s') : t('resources.host_axis_start')}</span><span>{t('resources.now')}</span>
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

  // 曲线横向拉伸铺满宽度（preserveAspectRatio="none"），圆点放在 SVG 外面按百分比定位，不然会被拉成椭圆
  return (
    <div className="relative h-[56px]">
      <svg viewBox="0 0 320 56" preserveAspectRatio="none" role="img" aria-label={label} className="h-full w-full overflow-visible">
        {[top, top + height / 2, top + height].map((y) => (
          <line key={y} x1="0" x2={width} y1={y} y2={y} stroke="var(--line)" strokeWidth="1" />
        ))}
        {path && <path d={path} fill="none" stroke="var(--fg-2)" strokeWidth="2" vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />}
      </svg>
      {latest && (
        <span
          className="absolute size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-fg"
          style={{ left: `${(latestX / width) * 100}%`, top: `${(latestY / 56) * 100}%` }}
          aria-hidden
        />
      )}
    </div>
  )
}

function gib(bytes: number) {
  return (bytes / 1024 ** 3).toFixed(1)
}
