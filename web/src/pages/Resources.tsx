import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import {
  apiErrorText,
  bytes,
  duration,
  mb,
  now,
  type Disk,
  type GPU,
  type ResourceHistory,
  type ResourcePoint,
  type ResourceRange,
  type ResourceSnapshot,
  type SelfUsagePart,
} from '@danlu/core'
import { ResourceChart, clock, nearestAt, type ChartSeries } from '../components/ResourceChart'
import { gpuBusy, groupStray, ReservationLine, shortName, strayLabel, strayTitle, useGpus } from '../components/GpuGrid'
import { Alert, Button, Card, ErrorBox, Meter, Segmented, Skeleton, StatusIcon, buttonClass, cx } from '../components/ui'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { usePrefs } from '../lib/prefs'

const RANGES: ResourceRange[] = ['1m', '5m', '15m', '1h']
// 是函数不是常量表：档位文案随当前语言取值，切换语言后重渲染即生效
function rangeOptions(): { value: ResourceRange; label: string }[] {
  return [
    { value: '1m', label: t('resources.range_1m') },
    { value: '5m', label: t('resources.range_5m') },
    { value: '15m', label: t('resources.range_15m') },
    { value: '1h', label: t('resources.range_1h') },
  ]
}
const RANGE_SECONDS: Record<ResourceRange, number> = { '1m': 60, '5m': 300, '15m': 900, '1h': 3600 }
// 曲线刷新随档位走：固定 10 秒时，1 分钟档的曲线会比上面每 3 秒刷新的大数字落后六分之一屏
const HISTORY_REFETCH: Record<ResourceRange, number> = { '1m': 3000, '5m': 5000, '15m': 10_000, '1h': 20_000 }
const RANGE_KEY = 'danlu.resources.range'

// 告警阈值：颜色只说状态（DESIGN.md）。默认 CPU/内存 ≥90%、交换区 ≥50%、GPU ≥83°C 才上琥珀，load 超过核数也是；
// 前四个在「设置 → 面板偏好」里改，存在服务器上，手机和电脑共用
function useWarnThresholds() {
  const { prefs } = usePrefs()
  return { cpu: prefs.warn_cpu_percent, mem: prefs.warn_mem_percent, swap: prefs.warn_swap_percent, gpuTemp: prefs.warn_gpu_temp_c }
}

function readStoredRange(): ResourceRange | null {
  try {
    const v = localStorage.getItem(RANGE_KEY)
    return RANGES.some((r) => r === v) ? (v as ResourceRange) : null
  } catch {
    return null
  }
}

/** 小链接的点击区：桌面外扩左右 8px、上下 6px（进程行距 33px，撑到 36px 会和邻行重叠）；
 *  手机改用负外边距 + 内边距把链接本身撑到 44px，不用外扩（外扩会盖住邻行）。 */
const linkHit = 'relative before:absolute before:-inset-x-2 before:-inset-y-1.5 max-sm:before:hidden'
const linkHitSm = cx(linkHit, 'max-sm:-my-3 max-sm:py-3') // 20px 文字 → 44px
const linkHitXs = cx(linkHit, 'max-sm:-my-3.5 max-sm:py-3.5') // 16px 文字 → 44px

const fmtPercent = (v: number) => `${Math.round(v)}%`
const fmtRate = (v: number) => `${bytes(v)}/s`
const fmtWatts = (v: number) => `${Math.round(v)} W`
const fmtEnergy = (wh: number | null | undefined) =>
  wh == null ? '—' : wh >= 1000 ? `${(wh / 1000).toFixed(2)} kWh` : `${wh.toFixed(1)} Wh`
const fmtTemp = (c: number | null | undefined) => c == null ? '—' : `${Math.round(c)}°C`
const fmtFreq = (mhz: number | null | undefined) => mhz == null ? null : `${(mhz / 1000).toFixed(2)} GHz`
const fmtSpeed = (mbps: number | undefined) =>
  !mbps || mbps <= 0 ? null : mbps >= 1000 ? `${(mbps / 1000).toFixed(1)} Gb/s` : `${mbps} Mb/s`
const fmtUptime = (seconds: number) => seconds > 0 ? duration(seconds) : '—'
// 负载保留三位有效数字、斜杠两边用细空格：手机上两列指标卡只有 140px 宽，"26.40 / 24.90 / 22.10" 会折成两行
const fmtLoad = (cpu: ResourceSnapshot['cpu']) =>
  [cpu.load_1, cpu.load_5, cpu.load_15].map((v) => v.toFixed(v >= 10 ? 1 : 2)).join('\u2009/\u2009')
const ago = (at: number) => Math.max(0, Math.round(now() - at))
// 命令开头常是 /home/x/miniconda3/envs/y/bin/python 这种长路径，只留文件名，
// 把位置让给后面的脚本和参数；完整命令在悬停提示里
const shortCommand = (cmd: string) => cmd.replace(/^\/\S*\/(?=[^/\s])/, '')

/** "206.0 MB/s" → ["206.0", "MB/s"]，"37%" → ["37", "%"]：数字和单位分开排，单位用小一号的灰字 */
function splitUnit(text: string): [string, string] {
  const m = text.match(/^(-?[\d.,]+)\s*(.*)$/)
  return m ? [m[1], m[2]] : [text, '']
}

/** 两个速率的峰值：单位相同时只写一次（"峰值 读 460.0 · 写 120.0 MB/s"），1280px 下的窄卡也放得下一行 */
function peakPair(aLabel: string, a: number, bLabel: string, b: number) {
  const [an, au] = splitUnit(fmtRate(a))
  const [bn, bu] = splitUnit(fmtRate(b))
  return au === bu
    ? [t('resources.peak', { label: aLabel, value: an }), t('resources.pair_value', { label: bLabel, value: bn, unit: bu })]
    : [t('resources.peak', { label: aLabel, value: `${an} ${au}` }), t('resources.pair_value', { label: bLabel, value: bn, unit: bu })]
}

function average(points: ResourcePoint[], pick: (p: ResourcePoint) => number | null | undefined) {
  const values = points.map(pick).filter((v): v is number => v != null && Number.isFinite(v))
  return values.length ? values.reduce((a, b) => a + b, 0) / values.length : null
}

function pointsOf(points: ResourcePoint[], pick: (p: ResourcePoint) => number | null | undefined): { at: number; value: number | null }[] {
  return points.map((p) => ({ at: p.at, value: pick(p) ?? null }))
}

function Panel({ title, extra, children, className }: { title: string; extra?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <Card className={cx('overflow-hidden', className)}>
      <div className="flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 border-b border-line bg-subtle px-3 py-2 text-ui sm:px-4">
        <h2 className="font-medium">{title}</h2>
        {extra && <div className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-fg-3">{extra}</div>}
      </div>
      {children}
    </Card>
  )
}

/** 表格的列头：面板底色、小号灰字、一条底线。读屏读不到（每格自己带说明），第一列兼作小节标题 */
function ColHead({ className, style, children }: { className?: string; style?: CSSProperties; children: ReactNode }) {
  return (
    <div aria-hidden className={cx('h-8 items-center gap-3 border-b border-line px-3 text-xs text-fg-3 sm:px-4', className)} style={style}>
      {children}
    </div>
  )
}

/** 曲线图例的小线段：实线是第一条线，虚线是第二条 */
function SeriesMark({ dashed }: { dashed?: boolean }) {
  return <span aria-hidden className={cx('inline-block w-3 shrink-0 self-center', dashed ? 'border-t border-dashed border-fg-3' : 'h-[1.5px] bg-fg-2')} />
}

function Fact({ label, value, detail, warn, title }: { label: string; value: ReactNode; detail?: string; warn?: boolean; title?: string }) {
  return (
    <div className="min-w-0" title={title}>
      <dt className="text-xs text-fg-3">{label}</dt>
      <dd className={cx('text-sm font-medium tabular-nums', warn && 'text-warn')}>{value}</dd>
      {detail && <dd className="truncate text-[11px] text-fg-3" title={detail}>{detail}</dd>}
    </div>
  )
}

/**
 * 手机上的摘要行：手机首屏只放得下页头和 GPU，指标卡要往下翻，
 * 这一行把磁盘、网络、功耗这些数也放进首屏。电脑上指标卡本来就在第一屏，不显示这一行。
 */
function PhoneSummary({ data }: { data: ResourceSnapshot }) {
  const warn = useWarnThresholds()
  const lowSpace = [...data.disks, ...(data.root_disk ? [data.root_disk] : [])].some((d) => d.warn)
  const spaceText = lowSpace ? t('resources.space_low') : data.disks_error ? t('resources.read_failed') : null
  const ready = data.samples >= 2
  const items: { label: string; value: string; warn?: boolean }[] = [
    { label: 'CPU', value: data.cpu.percent == null ? t('resources.sampling') : `${data.cpu.percent}%`, warn: data.cpu.percent != null && data.cpu.percent >= warn.cpu },
    { label: t('resources.memory'), value: `${data.memory.percent}%`, warn: data.memory.percent >= warn.mem },
    { label: t('resources.load'), value: fmtLoad(data.cpu), warn: data.cpu.load_1 > data.host.cpu_cores },
    { label: t('resources.swap'), value: data.memory.swap_total_bytes === 0 ? t('resources.none') : `${data.memory.swap_percent}%`, warn: data.memory.swap_total_bytes > 0 && data.memory.swap_percent >= warn.swap },
    { label: data.cpu.power_w == null && data.gpu_power_w != null ? t('resources.gpu_power') : t('resources.power'), value: data.power_w == null ? '—' : fmtWatts(data.power_w) },
    ...(data.network.length ? [{ label: t('resources.network'), value: ready ? `↓ ${fmtRate(data.net_rx_bps)} ↑ ${fmtRate(data.net_tx_bps)}` : t('resources.sampling') }] : []),
    ...(data.disk_io.length ? [{ label: t('resources.disk'), value: ready ? t('resources.phone_disk_io', { read: fmtRate(data.disk_read_bps), write: fmtRate(data.disk_write_bps) }) : t('resources.sampling') }] : []),
    ...(spaceText ? [{ label: t('resources.disk_space'), value: spaceText, warn: true }] : []),
    ...(data.self ? [{ label: t('resources.brand'), value: bytes(data.self.total_bytes) }] : []),
  ]
  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs sm:hidden">
      {items.map((it) => (
        <span key={it.label} className="inline-flex items-baseline gap-1 whitespace-nowrap">
          <span className="text-fg-3">{it.label}</span>
          <b className={cx('font-medium tabular-nums', it.warn && 'text-warn')}>{it.value}</b>
        </span>
      ))}
    </div>
  )
}

// ───────────────────────── 指标墙 ─────────────────────────

type TileValue = { label?: string; text: string; warn?: boolean; title?: string; mark?: boolean }

/**
 * 指标卡：标题和容量 → 当前值 → 第二个值 → 区间统计 → 面积图。
 * 同一排的卡等高，图都压在卡底，几张图的时间轴上下对齐，悬停时一起画竖线。
 */
function MetricTile({ title, context, hoverAt, primary, secondary, stats, chart, from, to, className }: {
  title: string
  context?: ReactNode
  /** 悬停的时刻：右上角换成这个时间，值也换成那一刻的 */
  hoverAt: number | null
  primary: TileValue
  secondary?: TileValue
  /** 区间统计，每项不拆行：卡窄时整项折到下一行，不会把"MB/s"单独甩到行首 */
  stats?: string[]
  chart: ReactNode
  from: number
  to: number
  className?: string
}) {
  const [num, unit] = splitUnit(primary.text)
  const withSeconds = to - from <= 300
  return (
    <section className={cx('flex min-w-0 flex-col rounded-lg border border-line bg-surface p-2.5 sm:p-3', className)}>
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-ui font-medium">{title}</h2>
        {hoverAt != null
          ? <span className="shrink-0 text-xs font-medium tabular-nums">{clock(hoverAt, true)}</span>
          : context && <span className="truncate text-xs text-fg-3 tabular-nums">{context}</span>}
      </div>
      <div className={cx('mt-1 flex min-w-0 flex-wrap items-baseline gap-x-1.5', primary.warn && 'text-warn')} title={primary.title}>
        {primary.mark && <SeriesMark />}
        {primary.label && <span className="shrink-0 text-xs text-fg-3">{primary.label}</span>}
        <span className="text-xl font-semibold leading-7 tracking-tight tabular-nums">{num}</span>
        {unit && <span className={cx('text-ui', !primary.warn && 'text-fg-3')}>{unit}</span>}
      </div>
      {secondary && (
        <div className={cx('flex min-w-0 items-baseline gap-x-1.5 text-xs', secondary.warn ? 'text-warn' : 'text-fg-2')} title={secondary.title}>
          {secondary.mark && <SeriesMark dashed />}
          {secondary.label && <span className={cx('shrink-0', !secondary.warn && 'text-fg-3')}>{secondary.label}</span>}
          <span className="min-w-0 tabular-nums">{secondary.text}</span>
        </div>
      )}
      {stats && stats.length > 0 && (
        // 项之间只留空隙不加圆点：卡窄折行时，圆点会挂在第二行行首
        <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-fg-3 tabular-nums">
          {stats.map((s, i) => <span key={i} className="whitespace-nowrap">{s}</span>)}
        </div>
      )}
      <div className="mt-auto pt-2.5">
        {chart}
        <div className="mt-1 flex justify-between text-[11px] leading-none text-fg-3 tabular-nums" aria-hidden>
          <span>{clock(from, withSeconds)}</span>
          <span>{clock(to, withSeconds)}</span>
        </div>
      </div>
    </section>
  )
}

function MetricWall({ data, history, gpus, from, to }: { data: ResourceSnapshot; history?: ResourceHistory; gpus: GPU[]; from: number; to: number }) {
  const warn = useWarnThresholds()
  const [hover, setHover] = useState<number | null>(null)
  const points = history?.points ?? []
  // 悬停吸到最近的采样点；落在采样空档里就当没有悬停
  const hp = useMemo(() => nearestAt(points, hover), [points, hover])
  const at = hp?.at ?? null
  const peaks = points.length ? history?.peaks : undefined
  const chartProps = { from, to, cursor: at, onCursor: setHover, className: 'h-10 sm:h-14' }
  const rateReady = data.samples >= 2

  // CPU
  const cpuNow = hp ? hp.cpu_percent : data.cpu.percent
  const cpuAvg = average(points, (p) => p.cpu_percent)
  const loadWarn = data.cpu.load_1 > data.host.cpu_cores

  // 内存
  const memNow = hp ? hp.memory_percent : data.memory.percent
  const swapNow = hp ? hp.swap_percent : data.memory.swap_percent
  const memAvg = average(points, (p) => p.memory_percent)

  // 磁盘
  const lowDisk = [...data.disks, ...(data.root_disk ? [data.root_disk] : [])].find((d) => d.warn)
  const diskAlert = lowDisk ? t('resources.disk_low', { path: lowDisk.path }) : data.disks_error ? t('resources.disks_read_failed') : null
  const readNow = hp ? hp.disk_read_bps : data.disk_read_bps
  const writeNow = hp ? hp.disk_write_bps : data.disk_write_bps

  // 网络
  const rxNow = hp ? hp.net_rx_bps : data.net_rx_bps
  const txNow = hp ? hp.net_tx_bps : data.net_tx_bps

  // 功耗：CPU 功耗常年读不到（普通用户读不了能量计数器）时不画它，图例里不挂一条不存在的线
  const cpuPowerAvg = average(points, (p) => p.cpu_power_w)
  const gpuPowerAvg = average(points, (p) => p.gpu_power_w)
  const hasCpuPower = cpuPowerAvg != null || data.cpu.power_w != null
  const gpuPowerNow = hp ? hp.gpu_power_w : data.gpu_power_w
  const cpuPowerNow = hp ? hp.cpu_power_w : data.cpu.power_w
  const powerLimit = gpus.reduce((sum, g) => sum + (g.power_limit_w ?? 0), 0)
  const powerSeries: ChartSeries[] = [
    { label: 'GPU', points: pointsOf(points, (p) => p.gpu_power_w) },
    ...(hasCpuPower ? [{ label: t('resources.cpu_package'), points: pointsOf(points, (p) => p.cpu_power_w), dashed: true }] : []),
  ]
  const noPowerSource = data.gpu_power_w == null && !hasCpuPower

  return (
    // 手机两列，功耗占满一行；平板和小桌面 3+2；1280px 以上一排 5 张
    <div className="grid grid-cols-2 gap-2 sm:gap-3 md:grid-cols-6 xl:grid-cols-5">
      <MetricTile
        title="CPU"
        context={t('resources.cores', { n: data.host.cpu_cores })}
        hoverAt={at}
        className="md:col-span-2 xl:col-span-1"
        primary={{ text: cpuNow == null ? '—' : `${cpuNow}%`, warn: cpuNow != null && cpuNow >= warn.cpu }}
        secondary={{ label: t('resources.load'), text: fmtLoad(data.cpu), warn: loadWarn, title: t('resources.load_title') }}
        stats={[t('resources.avg', { value: cpuAvg == null ? '—' : fmtPercent(cpuAvg) }), t('resources.peak_value', { value: peaks?.cpu_percent == null ? '—' : `${peaks.cpu_percent}%` })]}
        from={from}
        to={to}
        chart={<ResourceChart {...chartProps} series={[{ label: 'CPU', points: pointsOf(points, (p) => p.cpu_percent) }]} max={100} format={fmtPercent} label={t('resources.chart_cpu')} />}
      />
      <MetricTile
        title={t('resources.memory')}
        context={bytes(data.memory.total_bytes)}
        hoverAt={at}
        className="md:col-span-2 xl:col-span-1"
        primary={{ label: t('resources.used'), mark: true, text: `${memNow}%`, warn: memNow >= warn.mem, title: `${bytes(data.memory.used_bytes)} / ${bytes(data.memory.total_bytes)}` }}
        secondary={{
          label: t('resources.swap'),
          mark: data.memory.swap_total_bytes > 0,
          text: data.memory.swap_total_bytes === 0 ? t('resources.swap_off') : `${swapNow}%${hp ? '' : ` · ${bytes(data.memory.swap_used_bytes)}`}`,
          warn: data.memory.swap_total_bytes > 0 && swapNow >= warn.swap,
        }}
        stats={[t('resources.avg', { value: memAvg == null ? '—' : fmtPercent(memAvg) }), t('resources.peak_value', { value: peaks ? `${peaks.memory_percent}%` : '—' })]}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={[
              { label: t('resources.memory'), points: pointsOf(points, (p) => p.memory_percent) },
              ...(data.memory.swap_total_bytes > 0 ? [{ label: t('resources.swap'), points: pointsOf(points, (p) => p.swap_percent), dashed: true }] : []),
            ]}
            max={100}
            format={fmtPercent}
            label={t('resources.chart_mem')}
          />
        }
      />
      <MetricTile
        title={t('resources.disk')}
        // 容量告警占右上角：写在读写速率旁会被读成"I/O 出了问题"，单独占一行又把这张卡撑得比邻居高
        context={diskAlert ? <span className="text-warn">{diskAlert}</span> : data.disk_io.length ? t('resources.physical_disks', { n: data.disk_io.length }) : undefined}
        hoverAt={at}
        className="md:col-span-2 xl:col-span-1"
        primary={{ label: t('resources.read'), mark: true, text: rateReady || hp ? fmtRate(readNow) : '—' }}
        secondary={{ label: t('resources.write'), mark: true, text: rateReady || hp ? fmtRate(writeNow) : t('resources.wait_second_sample') }}
        // 长档位的曲线是桶均值，几秒的突发会被摊平；峰值按原始采样点算，单独写出来
        stats={peaks ? peakPair(t('resources.read'), peaks.disk_read_bps, t('resources.write'), peaks.disk_write_bps) : undefined}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={[
              { label: t('resources.read'), points: pointsOf(points, (p) => p.disk_read_bps) },
              { label: t('resources.write'), points: pointsOf(points, (p) => p.disk_write_bps), dashed: true },
            ]}
            format={fmtRate}
            binary
            label={t('resources.chart_disk')}
          />
        }
      />
      <MetricTile
        title={t('resources.network')}
        context={data.network.length ? t('resources.nic_count', { n: data.network.length }) : undefined}
        hoverAt={at}
        className="md:col-span-3 xl:col-span-1"
        primary={{ label: t('resources.rx'), mark: true, text: rateReady || hp ? fmtRate(rxNow) : '—' }}
        secondary={{ label: t('resources.tx'), mark: true, text: rateReady || hp ? fmtRate(txNow) : t('resources.wait_second_sample') }}
        stats={peaks ? peakPair(t('resources.rx_short'), peaks.net_rx_bps, t('resources.tx_short'), peaks.net_tx_bps) : undefined}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={[
              { label: t('resources.rx'), points: pointsOf(points, (p) => p.net_rx_bps) },
              { label: t('resources.tx'), points: pointsOf(points, (p) => p.net_tx_bps), dashed: true },
            ]}
            format={fmtRate}
            binary
            label={t('resources.chart_net')}
          />
        }
      />
      <MetricTile
        title={t('resources.power')}
        context={powerLimit > 0 ? t('resources.gpu_limit', { value: fmtWatts(powerLimit) }) : undefined}
        hoverAt={at}
        className="col-span-2 md:col-span-3 xl:col-span-1"
        primary={{ label: 'GPU', mark: hasCpuPower, text: gpuPowerNow == null ? '—' : fmtWatts(gpuPowerNow) }}
        secondary={
          hasCpuPower
            ? { label: t('resources.cpu_package'), mark: true, text: cpuPowerNow == null ? '—' : fmtWatts(cpuPowerNow) }
            : { label: t('resources.cpu_package'), text: data.cpu.power_error ? t('resources.power_unreadable') : '—', title: data.cpu.power_error }
        }
        stats={gpuPowerAvg == null ? undefined : [t('resources.avg', { value: fmtWatts(gpuPowerAvg) }), t('resources.peak_value', { value: peaks?.gpu_power_w == null ? '—' : fmtWatts(peaks.gpu_power_w) })]}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={powerSeries}
            format={fmtWatts}
            label={hasCpuPower ? t('resources.chart_power_both') : t('resources.chart_power_gpu')}
            // 首采样时实时功耗可能有值而历史还没积累：空态别说成"没有功耗来源"
            empty={noPowerSource && data.samples >= 2 ? t('resources.no_power_source') : t('resources.chart_empty')}
          />
        }
      />
    </div>
  )
}

// ───────────────────────── GPU ─────────────────────────

function GpuPanel({ gpus }: { gpus: ReturnType<typeof useGpus> }) {
  if (gpus.isLoading) {
    return <Card><Skeleton className="m-3 h-28 sm:m-4" /></Card>
  }
  const cards = gpus.data?.gpus ?? []
  // 查询失败（网络）和服务端报错（驱动挂了）都保留面板外框；有旧卡列表时继续显示，不没收整块
  const err = gpus.error
    ? t('resources.gpu_query_failed', { error: apiErrorText(gpus.error) })
    : gpus.data?.error
      ? t('resources.gpu_info_failed', { error: gpus.data.error })
      : null
  const procsKnown = !gpus.error && !gpus.data?.error
  const free = cards.filter((g) => !gpuBusy(g)).length
  const models = [...new Set(cards.map((g) => g.name))]
  const mixed = models.length > 1
  // 型号一样时写在标题栏一次；混装时写在每张卡的卡号下面
  const columns = `${mixed ? '7.5rem' : '3.5rem'} minmax(6rem,1fr) minmax(9rem,1.3fr) 3rem 6rem 3rem 6.5rem 3.5rem minmax(9rem,2fr)`
  return (
    <Panel
      title="GPU"
      extra={
        <>
          {err && <span className="text-warn">{t('resources.read_failed')}</span>}
          {cards.length > 0 && (
            <span className="inline-flex items-center gap-1.5 text-fg">
              <span className={cx('size-1.5 rounded-full', free ? 'bg-ok' : 'bg-fg-4')} aria-hidden />
              <b className="font-semibold tabular-nums">{free}<span className="font-normal text-fg-3"> / {cards.length}</span></b>
              <span className="text-fg-2">{t('resources.cards_free_suffix')}</span>
            </span>
          )}
          {!mixed && models[0] && <span className="max-sm:hidden">{shortName(models[0])}</span>}
        </>
      }
    >
      {err && <Alert tone="warn" role="alert" className="m-3 sm:m-4">{err}</Alert>}
      {cards.length === 0 ? (
        <p className="px-3 py-4 text-ui text-fg-3 sm:px-4">{err ? t('resources.gpu_unavailable') : t('resources.no_gpu')}</p>
      ) : (
        <>
          <ColHead className="hidden lg:grid" style={{ gridTemplateColumns: columns }}>
            <span>{t('resources.gpu_col_card')}</span>
            <span>{t('resources.utilization')}</span>
            <span>{t('resources.vram')}</span>
            <span className="text-right">{t('resources.temp')}</span>
            <span className="text-right">{t('resources.gpu_col_power')}</span>
            <span className="text-right">{t('resources.fan')}</span>
            <span className="text-right">{t('resources.gpu_col_clocks')}</span>
            <span>P-State</span>
            <span>{t('resources.gpu_col_occupant')}</span>
          </ColHead>
          <div className="divide-y divide-line">
            {cards.map((g) => (
              <div key={g.index}>
                <GpuLine g={g} mixed={mixed} procsKnown={procsKnown} columns={columns} />
                <GpuStack g={g} mixed={mixed} procsKnown={procsKnown} />
              </div>
            ))}
          </div>
        </>
      )}
    </Panel>
  )
}

const gpuBar = (g: GPU) => g.job ? 'bg-run' : g.reservation ? 'bg-fg-3' : g.external.length || (g.unknown_mem_mb ?? 0) > 0 ? 'bg-warn' : 'bg-fg-4'
const gpuMem = (g: GPU) => `${(g.mem_used_mb / 1024).toFixed(1)} / ${Math.round(g.mem_total_mb / 1024)} GB`
const gpuMemPct = (g: GPU) => g.mem_total_mb ? (g.mem_used_mb / g.mem_total_mb) * 100 : 0
const gpuPower = (g: GPU) => g.power_w <= 0 ? '—' : g.power_limit_w && g.power_limit_w > 0 ? `${Math.round(g.power_w)} / ${Math.round(g.power_limit_w)} W` : `${Math.round(g.power_w)} W`
const gpuClocks = (g: GPU) =>
  g.clock_graphics_mhz || g.clock_memory_mhz
    ? `${g.clock_graphics_mhz ? (g.clock_graphics_mhz / 1000).toFixed(2) : '—'} / ${g.clock_memory_mhz ? (g.clock_memory_mhz / 1000).toFixed(1) : '—'} GHz`
    : '—'

/** 占卡者：和首页同一套叙事——按任务/用户合并、显存可见、单个进程写出 pid、别的任务的进程链接过去 */
function Occupant({ g, procsKnown }: { g: GPU; procsKnown: boolean }) {
  if (g.job) {
    return (
      <Link to={`/jobs/${g.job.id}`} className={cx('flex min-w-0 items-center gap-1.5 text-fg hover:underline', linkHitSm)} title={g.job.name}>
        <StatusIcon status="running" className="size-3.5 shrink-0" />
        <span className="shrink-0 text-fg-3">#{g.job.id}</span>
        <span className="truncate font-medium">{g.job.name}</span>
      </Link>
    )
  }
  if (g.reservation) {
    // 预约的卡上的外部进程多半是用户自己的任务，只写个数，不标成需要注意
    const n = g.external.length
    return (
      <span className="flex min-w-0 items-center gap-1.5">
        <ReservationLine r={g.reservation} />
        {n > 0 && <span className="shrink-0 text-fg-3" title={groupStray(g.external).map(strayTitle).join('\n')}>· {t('resources.process_count', { n })}</span>}
      </span>
    )
  }
  if (g.external.length > 0) {
    return (
      <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-warn">
        {groupStray(g.external).map((s) => {
          const one = s.procs.length === 1 ? s.procs[0] : null
          const label = one ? `${strayLabel(one)} ${one.pid}` : s.job ? t('resources.job_processes', { id: s.job, n: s.procs.length }) : t('resources.external_processes', { n: s.procs.length })
          const inner = (
            <>
              <span className="size-1.5 shrink-0 rounded-full bg-warn" aria-hidden />
              <span className="truncate">{label}{!s.job && s.user ? ` · ${s.user}` : ''}</span>
              <span className="shrink-0">· {mb(s.mem)}</span>
            </>
          )
          return s.job ? (
            <Link key={s.key} to={`/jobs/${s.job}`} className={cx('flex min-w-0 items-center gap-1.5 hover:underline', linkHitXs)} title={strayTitle(s)}>{inner}</Link>
          ) : (
            <span key={s.key} className="flex min-w-0 items-center gap-1.5" title={strayTitle(s)}>{inner}</span>
          )
        })}
      </span>
    )
  }
  if ((g.unknown_mem_mb ?? 0) > 0) {
    return <span className="inline-flex items-center gap-1.5 text-warn"><span className="size-1.5 rounded-full bg-warn" aria-hidden />{t('resources.unknown_mem', { value: bytes((g.unknown_mem_mb ?? 0) * 2 ** 20) })}</span>
  }
  return procsKnown
    ? <span className="inline-flex items-center gap-1.5 text-ok"><span className="size-1.5 rounded-full bg-ok" aria-hidden />{t('resources.gpu_idle')}</span>
    : <span className="text-fg-3">{t('resources.occupancy_unknown')}</span>
}

/** ≥1024px：每张卡一行，列和列头对齐 */
function GpuLine({ g, mixed, procsKnown, columns }: { g: GPU; mixed: boolean; procsKnown: boolean; columns: string }) {
  const warn = useWarnThresholds()
  const tempWarn = g.temp_c >= warn.gpuTemp
  return (
    <div className="hidden min-h-9 items-center gap-3 px-3 py-1.5 text-ui sm:px-4 lg:grid" style={{ gridTemplateColumns: columns }}>
      <div className="min-w-0">
        <div className="font-semibold">GPU {g.index}</div>
        {mixed && <div className="truncate text-[11px] text-fg-3" title={g.name}>{shortName(g.name)}</div>}
      </div>
      <div className="flex items-center gap-2">
        <div className="flex-1"><Meter value={g.util} className={gpuBar(g)} /></div>
        <span className="w-9 text-right tabular-nums"><span className="sr-only">{t('resources.utilization')} </span>{g.util}%</span>
      </div>
      <div className="flex min-w-0 items-center gap-2">
        <div className="min-w-6 flex-1"><Meter value={gpuMemPct(g)} className={gpuBar(g)} /></div>
        <span className="shrink-0 text-xs text-fg-2 tabular-nums"><span className="sr-only">{t('resources.vram')} </span>{gpuMem(g)}</span>
      </div>
      <span className={cx('text-right text-xs tabular-nums', tempWarn ? 'text-warn' : 'text-fg-2')}><span className="sr-only">{t('resources.temp')} </span>{g.temp_c > 0 ? `${g.temp_c}°C` : '—'}</span>
      <span className="text-right text-xs text-fg-2 tabular-nums"><span className="sr-only">{t('resources.power')} </span>{gpuPower(g)}</span>
      <span className="text-right text-xs text-fg-2 tabular-nums"><span className="sr-only">{t('resources.fan')} </span>{g.fan_percent && g.fan_percent > 0 ? `${g.fan_percent}%` : '—'}</span>
      <span className="text-right text-xs text-fg-2 tabular-nums"><span className="sr-only">{t('resources.gpu_clocks_sr')} </span>{gpuClocks(g)}</span>
      <span className="text-xs text-fg-3">{g.pstate || '—'}</span>
      <div className="min-w-0"><Occupant g={g} procsKnown={procsKnown} /></div>
    </div>
  )
}

/** <1024px：每张卡两行，第一行卡号和占卡者，第二行利用率细条和指标 */
function GpuStack({ g, mixed, procsKnown }: { g: GPU; mixed: boolean; procsKnown: boolean }) {
  const warn = useWarnThresholds()
  const metrics: { text: string; warn?: boolean }[] = []
  if (g.temp_c > 0) metrics.push({ text: `${g.temp_c}°C`, warn: g.temp_c >= warn.gpuTemp })
  if (g.power_w > 0) metrics.push({ text: gpuPower(g) })
  if (g.fan_percent && g.fan_percent > 0) metrics.push({ text: t('resources.fan_value', { n: g.fan_percent }) })
  if (g.clock_graphics_mhz) metrics.push({ text: `${(g.clock_graphics_mhz / 1000).toFixed(2)} GHz` })
  if (g.clock_memory_mhz) metrics.push({ text: t('resources.vram_clock', { value: (g.clock_memory_mhz / 1000).toFixed(1) }) })
  if (g.pstate) metrics.push({ text: g.pstate })
  return (
    <div className="space-y-1.5 px-3 py-2.5 sm:px-4 lg:hidden">
      <div className="flex min-w-0 items-center gap-x-3 text-ui">
        <span className="shrink-0 font-semibold">GPU {g.index}</span>
        {mixed && <span className="min-w-0 truncate text-xs text-fg-3" title={g.name}>{shortName(g.name)}</span>}
        <div className="ml-auto flex min-w-0 justify-end"><Occupant g={g} procsKnown={procsKnown} /></div>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-fg-3">
        <span className="flex w-28 shrink-0 items-center gap-2"><Meter value={g.util} className={gpuBar(g)} /><span className="w-8 shrink-0 text-right text-fg tabular-nums">{g.util}%</span></span>
        <span className="text-fg-2 tabular-nums">{gpuMem(g)}</span>
        {metrics.map((m, i) => <span key={`${i}-${m.text}`} className={cx('tabular-nums', m.warn && 'text-warn')}>{m.text}</span>)}
      </div>
    </div>
  )
}

// ───────────────────────── 明细 ─────────────────────────

function CoreGrid({ cores, summary }: { cores: number[]; summary?: string }) {
  if (!cores.length) return <p className="text-xs text-fg-3">{t('resources.no_per_core')}</p>
  // 一行 16 格，核号好数：第 3 行第 5 格就是核 37；核太多时一行 32 格
  const cols = cores.length <= 16 ? cores.length : cores.length <= 64 ? 16 : 32
  return (
    // role="img" 会把子格呈献性吞掉，所以读屏靠 summary（最高/平均/哪几核）；格子本身 aria-hidden，鼠标悬停仍有每核数值
    <div role="img" aria-label={summary ?? t('resources.core_grid_aria', { n: cores.length })} className="grid gap-[3px]" style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))` }}>
      {cores.map((value, index) => {
        const known = value >= 0
        return (
          <span
            key={index}
            title={known ? `CPU ${index}: ${value}%` : t('resources.core_cell_sampling', { index })}
            aria-hidden
            className="h-3.5 rounded-[2px]"
            style={{ backgroundColor: known ? `color-mix(in oklab, var(--fg) ${Math.max(7, value)}%, var(--subtle))` : 'var(--subtle)' }}
          />
        )
      })}
    </div>
  )
}

function CpuDetail({ data }: { data: ResourceSnapshot }) {
  const freq = fmtFreq(data.cpu.freq_mhz)
  const maxFreq = fmtFreq(data.cpu.freq_max_mhz)
  const knownCores = data.cpu.cores.map((v, i) => ({ v, i })).filter((c) => c.v >= 0)
  const maxCore = knownCores.length ? Math.max(...knownCores.map((c) => c.v)) : null
  const avgCore = knownCores.length ? Math.round(knownCores.reduce((a, c) => a + c.v, 0) / knownCores.length) : null
  // 最忙的是哪几核：写成可见文字，触屏和读屏不用靠悬停
  const topCores = maxCore == null ? [] : knownCores.filter((c) => c.v === maxCore).map((c) => c.i)
  const topText = topCores.length
    ? t(topCores.length > 3 ? 'resources.top_cores_more' : 'resources.top_cores', { list: topCores.slice(0, 3).join(t('resources.core_list_sep')) })
    : ''
  const coreSummary = maxCore == null ? undefined : t('resources.core_summary', { n: data.cpu.cores.length, max: maxCore, top: topText, avg: avgCore ?? 0 })
  return (
    <Panel title={t('resources.cpu_cores_title')} className="h-full md:col-span-2 lg:col-span-1" extra={data.host.cpu_model && <span className="truncate" title={data.host.cpu_model}>{data.host.cpu_model}</span>}>
      <div className="space-y-3 p-3 sm:p-4">
        <dl className="grid grid-cols-3 gap-3">
          <Fact label={t('resources.avg_freq')} value={freq ?? '—'} />
          {/* 此刻最快那个核的频率，不是睿频上限，写成"最快核"免得误读 */}
          <Fact label={t('resources.fastest_core')} value={maxFreq ?? '—'} title={t('resources.fastest_core_title')} />
          <Fact label={t('resources.temp')} value={fmtTemp(data.cpu.temp_c)} title={t('resources.cpu_temp_title')} />
        </dl>
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-2 text-xs text-fg-3">
            <span className="shrink-0">{t('resources.core_count', { n: data.cpu.cores.length })}</span>
            <span className="truncate text-right">{maxCore == null ? t('resources.wait_second_sample') : t('resources.core_max_avg', { max: maxCore, top: topText, avg: avgCore ?? 0 })}</span>
          </div>
          <CoreGrid cores={data.cpu.cores} summary={coreSummary} />
          <p className="text-[11px] text-fg-3">{t('resources.core_grid_hint')}</p>
        </div>
      </div>
    </Panel>
  )
}

function MemoryDetail({ data }: { data: ResourceSnapshot }) {
  const warn = useWarnThresholds()
  const m = data.memory
  // 已用 = 总量 − 可用；可回收 = 可用 − 空闲（页缓存等内核随时能收回的部分）；三段正好加满总量
  const reclaim = Math.max(0, m.available_bytes - m.free_bytes)
  const total = m.total_bytes || m.used_bytes + reclaim + m.free_bytes || 1
  const parts = [
    { label: t('resources.used'), value: m.used_bytes, swatch: 'bg-fg-2' },
    { label: t('resources.reclaimable'), value: reclaim, swatch: 'bg-fg-4', title: t('resources.reclaimable_title') },
    { label: t('resources.mem_free'), value: m.free_bytes, swatch: 'bg-hover ring-1 ring-inset ring-line-strong' },
  ]
  const swapWarn = m.swap_total_bytes > 0 && m.swap_percent >= warn.swap
  return (
    <Panel title={t('resources.mem_breakdown')} className="h-full" extra={<span>{t('resources.total_label')} {bytes(m.total_bytes)}</span>}>
      <div className="space-y-3 p-3 sm:p-4">
        <div
          role="img"
          aria-label={t('resources.mem_bar_aria', { used: bytes(m.used_bytes), reclaim: bytes(reclaim), free: bytes(m.free_bytes) })}
          className="flex h-2.5 gap-0.5 overflow-hidden rounded-full"
        >
          <span className="bg-fg-2" style={{ width: `${(m.used_bytes / total) * 100}%` }} />
          <span className="bg-fg-4" style={{ width: `${(reclaim / total) * 100}%` }} />
          <span className="flex-1 bg-hover" />
        </div>
        <dl className="grid grid-cols-3 gap-3">
          {parts.map((p) => (
            <div key={p.label} className="min-w-0" title={p.title}>
              <dt className="flex items-center gap-1.5 text-xs text-fg-3"><span className={cx('size-2 shrink-0 rounded-[2px]', p.swatch)} aria-hidden />{p.label}</dt>
              <dd className="text-sm font-medium tabular-nums">{bytes(p.value)}</dd>
            </div>
          ))}
        </dl>
        <dl className="grid grid-cols-3 gap-3 border-t border-line pt-3">
          <Fact label={t('resources.available')} value={bytes(m.available_bytes)} />
          <Fact label={t('resources.cached')} value={bytes(m.cached_bytes)} />
          <Fact label={t('resources.buffers')} value={bytes(m.buffers_bytes)} />
        </dl>
        <div className="flex items-center gap-3 text-xs">
          <span className="shrink-0 text-fg-3">{t('resources.swap')}</span>
          {m.swap_total_bytes ? (
            <>
              <div className="flex-1"><Meter value={m.swap_percent} className={swapWarn ? 'bg-warn' : 'bg-fg-3'} /></div>
              <span className={cx('shrink-0 tabular-nums', swapWarn && 'text-warn')}>{bytes(m.swap_used_bytes)} / {bytes(m.swap_total_bytes)}</span>
            </>
          ) : <span className="text-fg-2">{t('resources.swap_off')}</span>}
        </div>
      </div>
    </Panel>
  )
}

function PowerDetail({ data, history, gpus }: { data: ResourceSnapshot; history?: ResourceHistory; gpus: GPU[] }) {
  const warn = useWarnThresholds()
  const points = history?.points ?? []
  const avgCPU = average(points, (p) => p.cpu_power_w)
  const avgGPU = average(points, (p) => p.gpu_power_w)
  const limit = gpus.reduce((sum, g) => sum + (g.power_limit_w ?? 0), 0)
  const tempWarn = data.gpu_temp_max_c != null && data.gpu_temp_max_c >= warn.gpuTemp
  return (
    <Panel title={t('resources.power_temp')} className="h-full">
      <div className="space-y-3 p-3 sm:p-4">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3">
          <Fact label={t('resources.cpu_package')} title={t('resources.rapl_title')} value={data.cpu.power_w == null ? '—' : fmtWatts(data.cpu.power_w)} detail={data.cpu.power_w == null ? (data.cpu.power_error ? t('resources.power_unreadable') : undefined) : avgCPU == null ? undefined : t('resources.avg', { value: fmtWatts(avgCPU) })} />
          <Fact label="GPU" value={data.gpu_power_w == null ? '—' : fmtWatts(data.gpu_power_w)} detail={avgGPU == null ? undefined : t('resources.avg', { value: fmtWatts(avgGPU) })} />
          <Fact label={t('resources.known_total')} value={data.power_w == null ? '—' : fmtWatts(data.power_w)} detail="CPU + GPU" />
          <Fact label={t('resources.since_start')} value={fmtEnergy(data.energy_wh)} detail={t('resources.energy_integral')} />
          <Fact label={t('resources.gpu_max_temp')} value={fmtTemp(data.gpu_temp_max_c)} warn={tempWarn} detail={tempWarn ? t('resources.temp_high') : data.gpu_power_w == null ? t('resources.power_unavailable') : t('resources.from_nvidia')} />
          <Fact label={t('resources.gpu_power_limit')} value={limit > 0 ? fmtWatts(limit) : '—'} detail={limit > 0 ? t('resources.gpu_limit_sum', { n: gpus.length }) : undefined} />
        </dl>
        <p className="border-t border-line pt-3 text-[11px] leading-relaxed text-fg-3">
          {data.cpu.power_w == null && data.cpu.power_error && <span className="text-fg-2">{t('resources.power_cpu_error', { error: data.cpu.power_error })}</span>}
          {t('resources.power_note')}
        </p>
      </div>
    </Panel>
  )
}

function StoragePanel({ data }: { data: ResourceSnapshot }) {
  const rows: Disk[] = [...data.disks]
  const root = data.root_disk
  // 按 id 或路径去重：root statfs 失败时 id 为空，只按 id 去重会把 "/" 重复成两行
  if (root && !data.disks.some((d) => (root.id && d.id === root.id) || d.path === root.path || d.paths.includes(root.path))) rows.unshift(root)
  const io = data.disk_io.slice(0, 8)
  const ioCols = 'grid-cols-[minmax(0,1fr)_5rem_5rem_2.5rem] sm:grid-cols-[minmax(0,1fr)_6rem_6rem_6rem]'
  return (
    <Panel
      title={t('resources.storage')}
      className="h-full"
      extra={data.disks_updated_at ? <span>{t('resources.disks_age', { n: ago(data.disks_updated_at) })}</span> : undefined}
    >
      {/* 行内已经写出采集失败的分区时，不再在顶部重复同一句错误 */}
      {data.disks_error && !rows.some((d) => d.error) && <Alert tone="warn" role="alert" className="m-3 sm:m-4">{t('resources.disk_capacity_failed', { error: data.disks_error })}</Alert>}
      <ColHead className="flex">
        <span className="font-medium text-fg-2">{t('resources.partition')}</span>
        <span className="ml-auto">{t('resources.free_total')}</span>
      </ColHead>
      <div className="divide-y divide-line">
        {rows.length === 0 ? <p className="px-3 py-2 text-ui text-fg-3 sm:px-4">{t('resources.no_partitions')}</p> : rows.map((disk, i) => <DiskRow key={disk.id || `${disk.path}:${i}`} disk={disk} />)}
      </div>
      <ColHead className={cx('grid border-t', ioCols)}>
        <span className="font-medium text-fg-2">{t('resources.physical_io')}</span>
        <span className="text-right">{t('resources.read')}</span>
        <span className="text-right">{t('resources.write')}</span>
        <span className="text-right">{t('resources.busy')}</span>
      </ColHead>
      {data.disk_io_error ? <Alert tone="warn" role="alert" className="m-3 sm:m-4">{t('resources.disk_io_failed', { error: data.disk_io_error })}</Alert> : io.length === 0 ? <p className="px-3 py-2 text-xs text-fg-3 sm:px-4">{t('resources.no_device_io')}</p> : (
        <div className="divide-y divide-line text-xs">
          {io.map((d) => (
            <div key={d.name} className={cx('grid items-center gap-3 px-3 py-1.5 sm:px-4', ioCols)}>
              <span className="truncate font-mono" title={d.name}>{d.name}</span>
              <span className="text-right tabular-nums"><span className="sr-only">{t('resources.read')} </span>{fmtRate(d.read_bytes_per_sec)}</span>
              <span className="text-right tabular-nums"><span className="sr-only">{t('resources.write')} </span>{fmtRate(d.write_bytes_per_sec)}</span>
              <span className="flex items-center justify-end gap-2 tabular-nums">
                <span className="w-10 max-sm:hidden"><Meter value={d.busy_percent} /></span>
                <span className="w-8 text-right"><span className="sr-only">{t('resources.busy')} </span>{d.busy_percent}%</span>
              </span>
            </div>
          ))}
        </div>
      )}
      {data.disk_io.length > io.length && <p className="border-t border-line px-3 py-1.5 text-[11px] text-fg-3 sm:px-4">{t('resources.more_devices', { n: data.disk_io.length - io.length })}</p>}
    </Panel>
  )
}

function DiskRow({ disk }: { disk: Disk }) {
  const used = disk.total_bytes > disk.available_bytes ? disk.total_bytes - disk.available_bytes : 0
  const percent = disk.total_bytes ? Math.round((used / disk.total_bytes) * 100) : 0
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 sm:px-4">
      {/* 手机上路径独占一行：和容量挤在一起时，长路径会被从中间断开 */}
      <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 max-sm:basis-full">
        <span className="min-w-0 font-mono text-xs [overflow-wrap:anywhere]">{disk.paths.join(' · ') || disk.path}</span>
        {disk.data && <span className="shrink-0 whitespace-nowrap text-[11px] text-fg-3">{t('resources.data_dir')}</span>}
        {disk.warn && !disk.error && <span className="shrink-0 text-xs text-warn">{t('resources.low_space')}</span>}
      </div>
      {disk.error ? <span className="text-xs text-warn">{disk.error}</span> : (
        <div className="flex items-center gap-2 max-sm:w-full sm:w-60">
          <div className="flex-1"><Meter value={percent} className={disk.warn ? 'bg-warn' : 'bg-fg-3'} /></div>
          <span className="shrink-0 text-xs tabular-nums">{bytes(disk.available_bytes)} / {bytes(disk.total_bytes)}</span>
        </div>
      )}
    </div>
  )
}

// ───────────────────────── 丹炉自身占用 ─────────────────────────

// 静态键表 + 函数取值：文案随当前语言，切换语言后重渲染即生效；未知分类原样显示服务器给的名称
const selfLabelKeys = {
  db: 'resources.self_db', logs: 'resources.self_logs', snapshots: 'resources.self_snapshots',
  run: 'resources.self_run', envs: 'resources.self_envs', backup: 'resources.self_backup', other: 'resources.self_other',
} as const

function selfLabel(name: string): string {
  const key = selfLabelKeys[name as keyof typeof selfLabelKeys]
  return key ? t(key) : name
}

function SelfPartRow({ part, total }: { part: SelfUsagePart; total: number }) {
  const percent = total ? Math.round((part.bytes / total) * 100) : 0
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 sm:px-4">
      {/* 手机上名称独占一行，和 DiskRow 同一套排法 */}
      <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 max-sm:basis-full">
        <span className="shrink-0 text-xs">{selfLabel(part.name)}</span>
        <span className="shrink-0 text-[11px] text-fg-3 tabular-nums">{t('resources.file_count', { n: part.files })}</span>
        {part.error && <span className="min-w-0 truncate text-xs text-warn" title={part.error}>{part.error}</span>}
      </div>
      <div className="flex items-center gap-2 max-sm:w-full sm:w-60" title={t('resources.self_share', { percent })}>
        <div className="flex-1"><Meter value={percent} className="bg-fg-3" /></div>
        <span className="shrink-0 text-xs tabular-nums">{bytes(part.bytes)}</span>
      </div>
    </div>
  )
}

/** 丹炉自己吃多少磁盘：数据目录分类占用 + 数据库大小 + 最近 1 小时趋势。每 30 秒统计一次。 */
function SelfUsagePanel({ data, history, from, to }: { data: ResourceSnapshot; history?: ResourceHistory; from: number; to: number }) {
  const self = data.self
  const points = history?.points ?? []
  const selfAge = data.self_updated_at ? ago(data.self_updated_at) : null
  const withSeconds = to - from <= 300
  const series: ChartSeries[] = [
    { label: t('resources.data_dir'), points: pointsOf(points, (p) => p.data_bytes) },
    { label: t('resources.self_db'), points: pointsOf(points, (p) => p.db_bytes), dashed: true },
  ]
  return (
    <Panel
      title={t('resources.self_title')}
      extra={
        <>
          {self && <span className="text-fg">{t('resources.total_label')} <b className="font-semibold tabular-nums">{bytes(self.total_bytes)}</b></span>}
          {data.self_error
            ? <span className="text-warn">{t('resources.self_scan_failed')}</span>
            : selfAge != null && <span>{t('resources.self_age', { n: selfAge })}</span>}
        </>
      }
    >
      {data.self_error && <Alert tone="warn" role="alert" className="m-3 sm:m-4">{t('resources.self_error', { error: data.self_error })}</Alert>}
      {!self ? (
        <p className="px-3 py-4 text-ui text-fg-3 sm:px-4">{data.self_error ? t('resources.self_no_success') : t('resources.self_scanning')}</p>
      ) : (
        <>
          <div className="grid gap-0 divide-y divide-line lg:grid-cols-2 lg:divide-x lg:divide-y-0">
            <div className="min-w-0">
              <ColHead className="flex">
                <span className="font-medium text-fg-2">{t('resources.self_breakdown')}</span>
                <span className="ml-auto tabular-nums">{t('resources.file_count', { n: self.files })}</span>
              </ColHead>
              <div className="divide-y divide-line">
                {self.parts.map((p) => <SelfPartRow key={p.name} part={p} total={self.total_bytes} />)}
              </div>
            </div>
            <div className="min-w-0">
              <ColHead className="flex">
                <span className="font-medium text-fg-2">{t('resources.self_db')}</span>
                {self.db && <span className="ml-auto tabular-nums">{t('resources.db_pages', { n: self.db.page_count, size: bytes(self.db.page_size) })}</span>}
              </ColHead>
              <div className="space-y-3 p-3 sm:p-4">
                {self.db_error && <p role="alert" className="text-xs text-warn">{t('resources.db_size_failed', { error: self.db_error })}</p>}
                {self.db ? (
                  <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
                    <Fact label={t('resources.db_logical')} value={bytes(self.db.logical_bytes)} title={t('resources.db_logical_title')} />
                    <Fact label={t('resources.db_free_pages')} value={bytes(self.db.free_bytes)} title={t('resources.db_free_title')} />
                    <Fact label="WAL" value={bytes(self.db.wal_bytes)} title={t('resources.wal_title')} />
                    <Fact label={t('resources.db_main_file')} value={bytes(self.db.file_bytes)} />
                  </dl>
                ) : !self.db_error && <p className="text-xs text-fg-3">{t('resources.no_db_stats')}</p>}
                <div>
                  <ResourceChart series={series} from={from} to={to} format={bytes} binary className="h-16" label={t('resources.chart_self')} />
                  <div className="mt-1 flex justify-between text-[11px] leading-none text-fg-3 tabular-nums" aria-hidden>
                    <span>{clock(from, withSeconds)}</span>
                    <span>{clock(to, withSeconds)}</span>
                  </div>
                  <p className="mt-1.5 flex flex-wrap items-center gap-x-3 text-[11px] text-fg-3">
                    <span className="inline-flex items-center gap-1"><SeriesMark />{t('resources.data_dir')}</span>
                    <span className="inline-flex items-center gap-1"><SeriesMark dashed />{t('resources.self_db')}</span>
                  </p>
                </div>
              </div>
            </div>
          </div>
          <p className="border-t border-line px-3 py-2 text-[11px] leading-relaxed text-fg-3 sm:px-4">
            <span className="font-mono [overflow-wrap:anywhere]" title={self.path}>{self.path}</span>
            {t('resources.self_note')}
          </p>
        </>
      )}
    </Panel>
  )
}

function NetworkPanel({ data }: { data: ResourceSnapshot }) {
  const ifaces = data.network.slice(0, 8)
  // 手机上网卡名独占一行（挤在速率中间会被截成 "e…"，等于没显示），下面一行四个数
  // 1024–1279px 两栏并排时面板只有 480px 上下，数值列收窄、利用率只写数字，把宽度让给网卡名
  const cols = 'grid-cols-[4.5rem_minmax(0,1fr)_minmax(0,1fr)_2.5rem] sm:grid-cols-[minmax(0,1fr)_4.5rem_5.5rem_5.5rem_5.5rem] lg:grid-cols-[minmax(0,1fr)_4rem_5rem_5rem_2.5rem] xl:grid-cols-[minmax(0,1fr)_4.5rem_5.5rem_5.5rem_5.5rem]'
  return (
    <Panel title={t('resources.nics')} className="h-full" extra={data.network.length > 0 ? <span>{t('resources.iface_count', { n: data.network.length })}</span> : undefined}>
      {data.network_error && <Alert tone="warn" role="alert" className="m-3 sm:m-4">{t('resources.net_read_failed', { error: data.network_error })}</Alert>}
      <ColHead className={cx('hidden sm:grid', cols)}>
        <span className="font-medium text-fg-2">{t('resources.iface')}</span>
        <span className="text-right">{t('resources.link')}</span>
        <span className="text-right">{t('resources.rx')}</span>
        <span className="text-right">{t('resources.tx')}</span>
        <span className="text-right">{t('resources.utilization')}</span>
      </ColHead>
      {data.samples < 2 && <p className="px-3 pt-2 text-xs text-fg-3 sm:px-4">{t('resources.wait_rate')}</p>}
      {ifaces.length === 0 ? <p className="px-3 py-2 text-xs text-fg-3 sm:px-4">{t('resources.no_nic_data')}</p> : (
        <div className="divide-y divide-line text-xs">
          {ifaces.map((n) => {
            const speed = fmtSpeed(n.speed_mbps)
            // 链路是全双工，收和发各有一份带宽：利用率看较忙的那个方向，不是两者相加
            const util = speed && n.speed_mbps ? Math.min(100, Math.round((Math.max(n.rx_bytes_per_sec, n.tx_bytes_per_sec) * 8) / (n.speed_mbps * 1e6) * 100)) : null
            const faults = n.rx_errors + n.tx_errors + n.rx_drops + n.tx_drops
            return (
              <div key={n.name} className={cx('grid items-center gap-x-3 gap-y-0.5 px-3 py-1.5 sm:px-4', cols)}>
                <span className="col-span-full flex min-w-0 flex-wrap items-baseline gap-x-2 sm:col-span-1 sm:block">
                  <span className="flex min-w-0 items-baseline gap-2">
                    {/* 网卡名优先完整显示，挤不下时先截掉后面的标记（标记有悬停提示） */}
                    <span className="max-w-full shrink-0 truncate font-mono" title={n.name}>{n.name}</span>
                    {n.virtual && <span className="shrink-0 text-[11px] text-fg-3">{t('resources.virtual')}</span>}
                    {faults > 0 && <span className="min-w-0 truncate text-warn" title={t('resources.iface_faults_title', { errors: n.rx_errors + n.tx_errors, drops: n.rx_drops + n.tx_drops })}>{t('resources.iface_faults', { n: faults })}</span>}
                  </span>
                  <span className="block truncate text-[11px] text-fg-3 tabular-nums max-sm:ml-auto" title={t('resources.iface_total_title')}>{t('resources.iface_total', { rx: bytes(n.rx_total_bytes), tx: bytes(n.tx_total_bytes) })}</span>
                </span>
                <span className="text-fg-2 tabular-nums sm:text-right"><span className="sr-only">{t('resources.link')} </span>{speed ?? '—'}</span>
                <span className="text-right tabular-nums"><span className="sm:hidden">↓ </span><span className="sr-only">{t('resources.rx')} </span>{fmtRate(n.rx_bytes_per_sec)}</span>
                <span className="text-right tabular-nums"><span className="sm:hidden">↑ </span><span className="sr-only">{t('resources.tx')} </span>{fmtRate(n.tx_bytes_per_sec)}</span>
                <span className="flex items-center justify-end gap-2 tabular-nums">
                  {util != null && <span className="w-10 max-sm:hidden lg:max-xl:hidden"><Meter value={util} /></span>}
                  <span className="w-8 text-right"><span className="sr-only">{t('resources.utilization')} </span>{util == null ? '—' : `${util}%`}</span>
                </span>
              </div>
            )
          })}
        </div>
      )}
      {data.network.length > ifaces.length && <p className="border-t border-line px-3 py-1.5 text-[11px] text-fg-3 sm:px-4">{t('resources.more_nics', { n: data.network.length - ifaces.length })}</p>}
    </Panel>
  )
}

function ProcessPanel({ data }: { data: ResourceSnapshot }) {
  const byCpu = useMemo(() => [...data.processes].sort((a, b) => b.cpu_percent - a.cpu_percent).slice(0, 8), [data.processes])
  const byMem = useMemo(() => [...data.processes].sort((a, b) => b.rss_bytes - a.rss_bytes).slice(0, 8), [data.processes])
  const processAge = data.processes_at ? ago(data.processes_at) : null
  return (
    <Panel
      title={t('resources.processes')}
      extra={
        <>
          <span>{t('resources.proc_cpu_window')}</span>
          {data.process_samples < 2 && <span className="text-warn">{t('resources.proc_cpu_wait')}</span>}
          {processAge != null && <span>{t('resources.processes_age', { n: processAge })}</span>}
        </>
      }
    >
      <div className="grid gap-0 divide-y divide-line lg:grid-cols-2 lg:divide-x lg:divide-y-0">
        <ProcessList title={t('resources.by_cpu')} sort="cpu" processes={byCpu} cpuKnown={data.process_samples >= 2} />
        <ProcessList title={t('resources.by_mem')} sort="mem" processes={byMem} cpuKnown={data.process_samples >= 2} />
      </div>
      {data.processes_error && <p role="alert" className="border-t border-line px-3 py-2 text-xs text-warn sm:px-4">{t('resources.proc_read_failed', { error: data.processes_error })}</p>}
    </Panel>
  )
}

const procCols = 'sm:grid sm:grid-cols-[minmax(0,1fr)_4rem_4rem_4rem_4.25rem]'

function ProcessList({ title, sort, processes, cpuKnown }: { title: string; sort: 'cpu' | 'mem'; processes: ResourceSnapshot['processes']; cpuKnown: boolean }) {
  return (
    <div className="min-w-0">
      <ColHead className={cx('flex', procCols)}>
        <span className="font-medium text-fg-2">{title}</span>
        <span className="text-right max-sm:hidden">PID</span>
        <span className="max-sm:hidden">{t('resources.user')}</span>
        <span className={cx('text-right max-sm:hidden', sort === 'cpu' && 'text-fg-2')}>CPU{sort === 'cpu' && ' ↓'}</span>
        <span className={cx('text-right max-sm:hidden', sort === 'mem' && 'text-fg-2')}>{t('resources.memory')}{sort === 'mem' && ' ↓'}</span>
      </ColHead>
      {processes.length === 0 ? <p className="px-3 py-3 text-ui text-fg-3 sm:px-4">{t('resources.no_processes')}</p> : (
        <ul className="divide-y divide-line">
          {processes.map((p) => (
            <li key={p.pid} className={cx('flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-1.5 text-xs sm:items-center sm:gap-y-0 sm:px-4', procCols)}>
              {/* 手机允许两行（截断等于没写）；桌面单行截断，完整命令在悬停提示里 */}
              <span className="min-w-0 max-sm:basis-full">
                <span className="block font-mono [overflow-wrap:anywhere] max-sm:line-clamp-2 sm:truncate" title={p.command}>{shortCommand(p.command) || `pid ${p.pid}`}</span>
                {p.job_id > 0 && (
                  <Link to={`/jobs/${p.job_id}`} className={cx('block w-fit max-w-full truncate text-[11px] text-fg-3 hover:text-fg hover:underline', linkHitXs)} title={p.job_name}>
                    #{p.job_id}{p.job_name ? ` ${p.job_name}` : ''}
                  </Link>
                )}
              </span>
              <span className="text-fg-3 tabular-nums sm:text-right"><span className="sm:hidden">PID </span>{p.pid}</span>
              <span className="truncate text-fg-3" title={p.user}>{p.user || '—'}</span>
              <span className={cx('text-right tabular-nums max-sm:ml-auto', sort === 'cpu' && 'font-medium')}><span className="sr-only">CPU </span>{cpuKnown ? `${p.cpu_percent.toFixed(1)}%` : '—'}</span>
              <span className={cx('text-right tabular-nums', sort === 'mem' && 'font-medium')}><span className="sr-only">{t('resources.memory')} </span>{bytes(p.rss_bytes)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ───────────────────────── 页面 ─────────────────────────

function Freshness({ age, stale, error, className }: { age: number | null; stale: boolean; error: boolean; className?: string }) {
  return (
    <span className={cx('inline-flex items-center gap-1.5 whitespace-nowrap text-xs', stale ? 'text-warn' : 'text-fg-3', className)}>
      <span className={cx('size-1.5 rounded-full', age == null ? 'bg-fg-4' : stale ? 'bg-warn' : 'bg-ok')} aria-hidden />
      {/* data.error 的 Alert 会解释原因，这里只报年龄，同一件事不说两遍 */}
      {age == null ? t('resources.wait_first_sample') : stale ? (error ? t('resources.data_age', { n: age }) : t('resources.data_stale', { n: age })) : t('resources.fresh', { n: age })}
    </span>
  )
}

export function Resources() {
  const [params, setParams] = useSearchParams()
  const raw = params.get('range') as ResourceRange | null
  const rawValid = RANGES.some((r) => r === raw)
  // URL 优先（可分享），其次记住上次选的档位，最后默认 15m
  const range = rawValid ? raw! : (readStoredRange() ?? '15m')
  const setRange = (value: ResourceRange) => {
    try { localStorage.setItem(RANGE_KEY, value) } catch { /* 隐私模式等写不进就算了 */ }
    const next = new URLSearchParams(params)
    next.set('range', value)
    setParams(next, { replace: true })
  }
  // URL 里带了非法 range：静默落到有效档位，并把地址栏修正，别留一个后端会 400 的值
  useEffect(() => {
    if (raw && !rawValid) {
      const next = new URLSearchParams(params)
      next.set('range', range)
      setParams(next, { replace: true })
    }
  }, [raw, rawValid])
  const current = useQuery({ queryKey: ['resources'], queryFn: api.resources, refetchInterval: 3000 })
  const history = useQuery({
    queryKey: ['resources', 'history', range],
    queryFn: () => api.resourceHistory(range),
    refetchInterval: HISTORY_REFETCH[range],
    // 切档时保留上一档曲线直到新数据到达：不然每次切换都闪一次"正在积累历史数据…"，慢网上放大到整个 RTT
    placeholderData: keepPreviousData,
  })
  const gpus = useGpus()

  if (current.isLoading) {
    return (
      <div className="space-y-4 sm:space-y-5">
        <Skeleton className="h-12 w-64" />
        <Skeleton className="h-40" />
        <div className="grid grid-cols-2 gap-2 sm:gap-3 md:grid-cols-6 xl:grid-cols-5">
          {Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className={cx('h-44', i < 3 ? 'md:col-span-2 xl:col-span-1' : 'md:col-span-3 xl:col-span-1', i === 4 && 'col-span-2')} />)}
        </div>
        <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3"><Skeleton className="h-48 md:col-span-2 lg:col-span-1" /><Skeleton className="h-48" /><Skeleton className="h-48" /></div>
      </div>
    )
  }
  // 只有首次加载失败才整页报错；轮询失败时手里还有上一份好数据，保留面板降级显示
  if (!current.data) {
    return (
      <div className="space-y-4">
        <h1 className="text-lg font-semibold">{t('resources.title')}</h1>
        <ErrorBox error={current.error ?? t('resources.no_data')} />
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => current.refetch()}>{t('common.retry')}</Button>
          <Link to="/" className={buttonClass('ghost')}>{t('resources.back_home')}</Link>
        </div>
      </div>
    )
  }

  const data = current.data
  const from = history.data?.from ?? now() - RANGE_SECONDS[range]
  const to = history.data?.to ?? now()
  // updated_at 是最近一次成功采样的时间；0 说明一次都没成功，其它字段全是零值，不能画成"内存 0%"
  const noSample = !data.updated_at
  const age = noSample ? null : ago(data.updated_at)
  const stale = age != null && age > 10
  const host = data.host
  const hostLine = [host.hostname, host.os, host.kernel && t('resources.host_kernel', { kernel: host.kernel }), host.uptime_seconds > 0 && t('resources.host_uptime', { uptime: fmtUptime(host.uptime_seconds) })].filter(Boolean).join(' · ')
  const gpuList = gpus.data?.gpus ?? []

  return (
    <div className="space-y-4 sm:space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1 space-y-0.5">
          <div className="flex items-center gap-3">
            <h1 className="text-lg font-semibold">{t('resources.title')}</h1>
            {/* 手机上新鲜度和标题同一行，省出一行给下面的面板 */}
            <Freshness age={age} stale={stale} error={!!data.error} className="ml-auto sm:hidden" />
          </div>
          {hostLine && <p className="truncate text-ui text-fg-3" title={hostLine}>{hostLine}</p>}
        </div>
        <div className="flex items-center gap-3 max-sm:w-full">
          <Freshness age={age} stale={stale} error={!!data.error} className="max-sm:hidden" />
          <Segmented options={rangeOptions()} value={range} onChange={setRange} label={t('resources.history_range')} className="max-sm:w-full" />
        </div>
      </div>

      {current.error && (
        <Alert tone="warn" role="alert">
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span>{age == null ? t('resources.refresh_failed_latest', { error: apiErrorText(current.error) }) : t('resources.refresh_failed_age', { error: apiErrorText(current.error), age })}</span>
            <Button size="sm" className="-my-2" onClick={() => current.refetch()}>{t('common.retry')}</Button>
          </span>
        </Alert>
      )}
      {data.error && <Alert tone="warn" role="alert">{data.error}</Alert>}
      {history.error && !history.data && <ErrorBox error={history.error} />}
      {noSample ? (
        <>
          {/* GPU 数据走自己的接口，主机采样失败时照样能回答"卡空不空" */}
          <GpuPanel gpus={gpus} />
          <Card>
            <p className="px-3 py-8 text-center text-ui text-fg-3 sm:px-4">
              {data.error ? t('resources.no_host_data') : t('resources.first_sampling')}
            </p>
          </Card>
        </>
      ) : (
        <>
          {/* 顺序按用户的三个问题排：先答"卡空不空"（GPU），再是整排指标卡（第一屏看完所有曲线），最后是明细 */}
          <PhoneSummary data={data} />
          <GpuPanel gpus={gpus} />
          <MetricWall data={data} history={history.data} gpus={gpuList} from={from} to={to} />
          <div className="space-y-3">
            <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
              <CpuDetail data={data} />
              <MemoryDetail data={data} />
              <PowerDetail data={data} history={history.data} gpus={gpuList} />
            </div>
            <div className="grid gap-3 lg:grid-cols-2">
              <StoragePanel data={data} />
              <NetworkPanel data={data} />
            </div>
            <SelfUsagePanel data={data} history={history.data} from={from} to={to} />
          </div>
          <ProcessPanel data={data} />
          <p className="text-[11px] text-fg-3">{t('resources.footnote')}</p>
        </>
      )}
    </div>
  )
}
