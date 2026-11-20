import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import {
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
} from '@danlu/core'
import { ResourceChart, clock, nearestAt, type ChartSeries } from '../components/ResourceChart'
import { gpuBusy, groupStray, shortName, strayLabel, strayTitle, useGpus } from '../components/GpuGrid'
import { Alert, Button, Card, ErrorBox, Meter, Segmented, Skeleton, StatusIcon, buttonClass, cx } from '../components/ui'
import { api } from '../lib/api'

const RANGES: { value: ResourceRange; label: string }[] = [
  { value: '1m', label: '1 分钟' },
  { value: '5m', label: '5 分钟' },
  { value: '15m', label: '15 分钟' },
  { value: '1h', label: '1 小时' },
]
const RANGE_SECONDS: Record<ResourceRange, number> = { '1m': 60, '5m': 300, '15m': 900, '1h': 3600 }
// 曲线刷新随档位走：固定 10 秒时，1 分钟档的曲线会比上面每 3 秒刷新的大数字落后六分之一屏
const HISTORY_REFETCH: Record<ResourceRange, number> = { '1m': 3000, '5m': 5000, '15m': 10_000, '1h': 20_000 }
const RANGE_KEY = 'danlu.resources.range'

// 告警阈值：颜色只说状态（DESIGN.md）。CPU/内存 ≥90%、load 超过核数、GPU ≥83°C 才上琥珀。
const CPU_WARN = 90
const MEM_WARN = 90
const SWAP_WARN = 50
const GPU_TEMP_WARN = 83

function readStoredRange(): ResourceRange | null {
  try {
    const v = localStorage.getItem(RANGE_KEY)
    return RANGES.some((r) => r.value === v) ? (v as ResourceRange) : null
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
  return au === bu ? [`峰值 ${aLabel} ${an}`, `${bLabel} ${bn} ${bu}`] : [`峰值 ${aLabel} ${an} ${au}`, `${bLabel} ${bn} ${bu}`]
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
  const lowSpace = [...data.disks, ...(data.root_disk ? [data.root_disk] : [])].some((d) => d.warn)
  const spaceText = lowSpace ? '偏低' : data.disks_error ? '读取失败' : null
  const ready = data.samples >= 2
  const items: { label: string; value: string; warn?: boolean }[] = [
    { label: 'CPU', value: data.cpu.percent == null ? '采样中' : `${data.cpu.percent}%`, warn: data.cpu.percent != null && data.cpu.percent >= CPU_WARN },
    { label: '内存', value: `${data.memory.percent}%`, warn: data.memory.percent >= MEM_WARN },
    { label: '负载', value: fmtLoad(data.cpu), warn: data.cpu.load_1 > data.host.cpu_cores },
    { label: '交换', value: data.memory.swap_total_bytes === 0 ? '无' : `${data.memory.swap_percent}%`, warn: data.memory.swap_total_bytes > 0 && data.memory.swap_percent >= SWAP_WARN },
    { label: data.cpu.power_w == null && data.gpu_power_w != null ? 'GPU 功耗' : '功耗', value: data.power_w == null ? '—' : fmtWatts(data.power_w) },
    ...(data.network.length ? [{ label: '网络', value: ready ? `↓ ${fmtRate(data.net_rx_bps)} ↑ ${fmtRate(data.net_tx_bps)}` : '采样中' }] : []),
    ...(data.disk_io.length ? [{ label: '磁盘', value: ready ? `读 ${fmtRate(data.disk_read_bps)} 写 ${fmtRate(data.disk_write_bps)}` : '采样中' }] : []),
    ...(spaceText ? [{ label: '磁盘空间', value: spaceText, warn: true }] : []),
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
  const diskAlert = lowDisk ? `${lowDisk.path} 余量偏低` : data.disks_error ? '分区容量读取失败' : null
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
    ...(hasCpuPower ? [{ label: 'CPU 封装', points: pointsOf(points, (p) => p.cpu_power_w), dashed: true }] : []),
  ]
  const noPowerSource = data.gpu_power_w == null && !hasCpuPower

  return (
    // 手机两列，功耗占满一行；平板和小桌面 3+2；1280px 以上一排 5 张
    <div className="grid grid-cols-2 gap-2 sm:gap-3 md:grid-cols-6 xl:grid-cols-5">
      <MetricTile
        title="CPU"
        context={`${data.host.cpu_cores} 核`}
        hoverAt={at}
        className="md:col-span-2 xl:col-span-1"
        primary={{ text: cpuNow == null ? '—' : `${cpuNow}%`, warn: cpuNow != null && cpuNow >= CPU_WARN }}
        secondary={{ label: '负载', text: fmtLoad(data.cpu), warn: loadWarn, title: '1 / 5 / 15 分钟 load average；超过核数说明有进程在排队等 CPU' }}
        stats={[`平均 ${cpuAvg == null ? '—' : fmtPercent(cpuAvg)}`, `峰值 ${peaks?.cpu_percent == null ? '—' : `${peaks.cpu_percent}%`}`]}
        from={from}
        to={to}
        chart={<ResourceChart {...chartProps} series={[{ label: 'CPU', points: pointsOf(points, (p) => p.cpu_percent) }]} max={100} format={fmtPercent} label="CPU 使用率历史曲线" />}
      />
      <MetricTile
        title="内存"
        context={bytes(data.memory.total_bytes)}
        hoverAt={at}
        className="md:col-span-2 xl:col-span-1"
        primary={{ label: '已用', mark: true, text: `${memNow}%`, warn: memNow >= MEM_WARN, title: `${bytes(data.memory.used_bytes)} / ${bytes(data.memory.total_bytes)}` }}
        secondary={{
          label: '交换',
          mark: data.memory.swap_total_bytes > 0,
          text: data.memory.swap_total_bytes === 0 ? '未启用' : `${swapNow}%${hp ? '' : ` · ${bytes(data.memory.swap_used_bytes)}`}`,
          warn: data.memory.swap_total_bytes > 0 && swapNow >= SWAP_WARN,
        }}
        stats={[`平均 ${memAvg == null ? '—' : fmtPercent(memAvg)}`, `峰值 ${peaks ? `${peaks.memory_percent}%` : '—'}`]}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={[
              { label: '内存', points: pointsOf(points, (p) => p.memory_percent) },
              ...(data.memory.swap_total_bytes > 0 ? [{ label: '交换', points: pointsOf(points, (p) => p.swap_percent), dashed: true }] : []),
            ]}
            max={100}
            format={fmtPercent}
            label="内存和交换使用率历史曲线"
          />
        }
      />
      <MetricTile
        title="磁盘"
        // 容量告警占右上角：写在读写速率旁会被读成"I/O 出了问题"，单独占一行又把这张卡撑得比邻居高
        context={diskAlert ? <span className="text-warn">{diskAlert}</span> : data.disk_io.length ? `${data.disk_io.length} 块物理盘` : undefined}
        hoverAt={at}
        className="md:col-span-2 xl:col-span-1"
        primary={{ label: '读', mark: true, text: rateReady || hp ? fmtRate(readNow) : '—' }}
        secondary={{ label: '写', mark: true, text: rateReady || hp ? fmtRate(writeNow) : '等待第二次采样' }}
        // 长档位的曲线是桶均值，几秒的突发会被摊平；峰值按原始采样点算，单独写出来
        stats={peaks ? peakPair('读', peaks.disk_read_bps, '写', peaks.disk_write_bps) : undefined}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={[
              { label: '读', points: pointsOf(points, (p) => p.disk_read_bps) },
              { label: '写', points: pointsOf(points, (p) => p.disk_write_bps), dashed: true },
            ]}
            format={fmtRate}
            binary
            label="物理盘读写速率历史曲线"
          />
        }
      />
      <MetricTile
        title="网络"
        context={data.network.length ? `${data.network.length} 个网卡` : undefined}
        hoverAt={at}
        className="md:col-span-3 xl:col-span-1"
        primary={{ label: '接收', mark: true, text: rateReady || hp ? fmtRate(rxNow) : '—' }}
        secondary={{ label: '发送', mark: true, text: rateReady || hp ? fmtRate(txNow) : '等待第二次采样' }}
        stats={peaks ? peakPair('收', peaks.net_rx_bps, '发', peaks.net_tx_bps) : undefined}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={[
              { label: '接收', points: pointsOf(points, (p) => p.net_rx_bps) },
              { label: '发送', points: pointsOf(points, (p) => p.net_tx_bps), dashed: true },
            ]}
            format={fmtRate}
            binary
            label="网络收发速率历史曲线"
          />
        }
      />
      <MetricTile
        title="功耗"
        context={powerLimit > 0 ? `GPU 上限 ${fmtWatts(powerLimit)}` : undefined}
        hoverAt={at}
        className="col-span-2 md:col-span-3 xl:col-span-1"
        primary={{ label: 'GPU', mark: hasCpuPower, text: gpuPowerNow == null ? '—' : fmtWatts(gpuPowerNow) }}
        secondary={
          hasCpuPower
            ? { label: 'CPU 封装', mark: true, text: cpuPowerNow == null ? '—' : fmtWatts(cpuPowerNow) }
            : { label: 'CPU 封装', text: data.cpu.power_error ? '读不到' : '—', title: data.cpu.power_error }
        }
        stats={gpuPowerAvg == null ? undefined : [`平均 ${fmtWatts(gpuPowerAvg)}`, `峰值 ${peaks?.gpu_power_w == null ? '—' : fmtWatts(peaks.gpu_power_w)}`]}
        from={from}
        to={to}
        chart={
          <ResourceChart
            {...chartProps}
            series={powerSeries}
            format={fmtWatts}
            label={hasCpuPower ? 'GPU 和 CPU 功耗历史曲线' : 'GPU 功耗历史曲线'}
            // 首采样时实时功耗可能有值而历史还没积累：空态别说成"没有功耗来源"
            empty={noPowerSource && data.samples >= 2 ? '没有功耗来源' : '正在积累历史数据…'}
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
    ? `读取 GPU 失败：${(gpus.error as Error).message}`
    : gpus.data?.error
      ? `GPU 信息读取失败：${gpus.data.error}`
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
          {err && <span className="text-warn">读取失败</span>}
          {cards.length > 0 && (
            <span className="inline-flex items-center gap-1.5 text-fg">
              <span className={cx('size-1.5 rounded-full', free ? 'bg-ok' : 'bg-fg-4')} aria-hidden />
              <b className="font-semibold tabular-nums">{free}<span className="font-normal text-fg-3"> / {cards.length}</span></b>
              <span className="text-fg-2">张卡空闲</span>
            </span>
          )}
          {!mixed && models[0] && <span className="max-sm:hidden">{shortName(models[0])}</span>}
        </>
      }
    >
      {err && <Alert tone="warn" role="alert" className="m-3 sm:m-4">{err}</Alert>}
      {cards.length === 0 ? (
        <p className="px-3 py-4 text-ui text-fg-3 sm:px-4">{err ? 'GPU 数据暂不可用，无法判断占用' : '没有检测到 GPU，只能提交不用 GPU 的任务'}</p>
      ) : (
        <>
          <ColHead className="hidden lg:grid" style={{ gridTemplateColumns: columns }}>
            <span>卡</span>
            <span>利用率</span>
            <span>显存</span>
            <span className="text-right">温度</span>
            <span className="text-right">功耗 / 上限</span>
            <span className="text-right">风扇</span>
            <span className="text-right">核心 / 显存</span>
            <span>P-State</span>
            <span>占用</span>
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

const gpuBar = (g: GPU) => g.job ? 'bg-run' : g.external.length || (g.unknown_mem_mb ?? 0) > 0 ? 'bg-warn' : 'bg-fg-4'
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
  if (g.external.length > 0) {
    return (
      <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-warn">
        {groupStray(g.external).map((s) => {
          const one = s.procs.length === 1 ? s.procs[0] : null
          const label = one ? `${strayLabel(one)} ${one.pid}` : s.job ? `#${s.job} 的 ${s.procs.length} 个进程` : `${s.procs.length} 个外部进程`
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
    return <span className="inline-flex items-center gap-1.5 text-warn"><span className="size-1.5 rounded-full bg-warn" aria-hidden />未知显存 {bytes((g.unknown_mem_mb ?? 0) * 2 ** 20)}</span>
  }
  return procsKnown
    ? <span className="inline-flex items-center gap-1.5 text-ok"><span className="size-1.5 rounded-full bg-ok" aria-hidden />空闲</span>
    : <span className="text-fg-3">占用未知</span>
}

/** ≥1024px：每张卡一行，列和列头对齐 */
function GpuLine({ g, mixed, procsKnown, columns }: { g: GPU; mixed: boolean; procsKnown: boolean; columns: string }) {
  const tempWarn = g.temp_c >= GPU_TEMP_WARN
  return (
    <div className="hidden min-h-9 items-center gap-3 px-3 py-1.5 text-ui sm:px-4 lg:grid" style={{ gridTemplateColumns: columns }}>
      <div className="min-w-0">
        <div className="font-semibold">GPU {g.index}</div>
        {mixed && <div className="truncate text-[11px] text-fg-3" title={g.name}>{shortName(g.name)}</div>}
      </div>
      <div className="flex items-center gap-2">
        <div className="flex-1"><Meter value={g.util} className={gpuBar(g)} /></div>
        <span className="w-9 text-right tabular-nums"><span className="sr-only">利用率 </span>{g.util}%</span>
      </div>
      <div className="flex min-w-0 items-center gap-2">
        <div className="min-w-6 flex-1"><Meter value={gpuMemPct(g)} className={gpuBar(g)} /></div>
        <span className="shrink-0 text-xs text-fg-2 tabular-nums"><span className="sr-only">显存 </span>{gpuMem(g)}</span>
      </div>
      <span className={cx('text-right text-xs tabular-nums', tempWarn ? 'text-warn' : 'text-fg-2')}><span className="sr-only">温度 </span>{g.temp_c > 0 ? `${g.temp_c}°C` : '—'}</span>
      <span className="text-right text-xs text-fg-2 tabular-nums"><span className="sr-only">功耗 </span>{gpuPower(g)}</span>
      <span className="text-right text-xs text-fg-2 tabular-nums"><span className="sr-only">风扇 </span>{g.fan_percent && g.fan_percent > 0 ? `${g.fan_percent}%` : '—'}</span>
      <span className="text-right text-xs text-fg-2 tabular-nums"><span className="sr-only">核心和显存频率 </span>{gpuClocks(g)}</span>
      <span className="text-xs text-fg-3">{g.pstate || '—'}</span>
      <div className="min-w-0"><Occupant g={g} procsKnown={procsKnown} /></div>
    </div>
  )
}

/** <1024px：每张卡两行，第一行卡号和占卡者，第二行利用率细条和指标 */
function GpuStack({ g, mixed, procsKnown }: { g: GPU; mixed: boolean; procsKnown: boolean }) {
  const metrics: { text: string; warn?: boolean }[] = []
  if (g.temp_c > 0) metrics.push({ text: `${g.temp_c}°C`, warn: g.temp_c >= GPU_TEMP_WARN })
  if (g.power_w > 0) metrics.push({ text: gpuPower(g) })
  if (g.fan_percent && g.fan_percent > 0) metrics.push({ text: `风扇 ${g.fan_percent}%` })
  if (g.clock_graphics_mhz) metrics.push({ text: `${(g.clock_graphics_mhz / 1000).toFixed(2)} GHz` })
  if (g.clock_memory_mhz) metrics.push({ text: `显存 ${(g.clock_memory_mhz / 1000).toFixed(1)} GHz` })
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
  if (!cores.length) return <p className="text-xs text-fg-3">没有读到每核使用率</p>
  // 一行 16 格，核号好数：第 3 行第 5 格就是核 37；核太多时一行 32 格
  const cols = cores.length <= 16 ? cores.length : cores.length <= 64 ? 16 : 32
  return (
    // role="img" 会把子格呈献性吞掉，所以读屏靠 summary（最高/平均/哪几核）；格子本身 aria-hidden，鼠标悬停仍有每核数值
    <div role="img" aria-label={summary ?? `${cores.length} 个核心的当前使用率`} className="grid gap-[3px]" style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))` }}>
      {cores.map((value, index) => {
        const known = value >= 0
        return (
          <span
            key={index}
            title={known ? `CPU ${index}: ${value}%` : `CPU ${index}: 采样中`}
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
  const topText = topCores.length ? `（核 ${topCores.slice(0, 3).join('、')}${topCores.length > 3 ? ' 等' : ''}）` : ''
  const coreSummary = maxCore == null ? undefined : `${data.cpu.cores.length} 个核心：最高 ${maxCore}%${topText}，平均 ${avgCore}%`
  return (
    <Panel title="CPU 核心" className="h-full md:col-span-2 lg:col-span-1" extra={data.host.cpu_model && <span className="truncate" title={data.host.cpu_model}>{data.host.cpu_model}</span>}>
      <div className="space-y-3 p-3 sm:p-4">
        <dl className="grid grid-cols-3 gap-3">
          <Fact label="平均频率" value={freq ?? '—'} />
          {/* 此刻最快那个核的频率，不是睿频上限，写成"最快核"免得误读 */}
          <Fact label="最快核" value={maxFreq ?? '—'} title="此刻最快那个核的频率，不是睿频上限" />
          <Fact label="温度" value={fmtTemp(data.cpu.temp_c)} title="多路服务器取各颗 CPU 里最高的温度" />
        </dl>
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-2 text-xs text-fg-3">
            <span className="shrink-0">{data.cpu.cores.length} 个核心</span>
            <span className="truncate text-right">{maxCore == null ? '等待第二次采样' : `最高 ${maxCore}%${topText} · 平均 ${avgCore}%`}</span>
          </div>
          <CoreGrid cores={data.cpu.cores} summary={coreSummary} />
          <p className="text-[11px] text-fg-3">每格一个核心，越深越忙</p>
        </div>
      </div>
    </Panel>
  )
}

function MemoryDetail({ data }: { data: ResourceSnapshot }) {
  const m = data.memory
  // 已用 = 总量 − 可用；可回收 = 可用 − 空闲（页缓存等内核随时能收回的部分）；三段正好加满总量
  const reclaim = Math.max(0, m.available_bytes - m.free_bytes)
  const total = m.total_bytes || m.used_bytes + reclaim + m.free_bytes || 1
  const parts = [
    { label: '已用', value: m.used_bytes, swatch: 'bg-fg-2' },
    { label: '可回收', value: reclaim, swatch: 'bg-fg-4', title: '页缓存等内核随时能收回的内存（可用 − 空闲）' },
    { label: '空闲', value: m.free_bytes, swatch: 'bg-hover ring-1 ring-inset ring-line-strong' },
  ]
  const swapWarn = m.swap_total_bytes > 0 && m.swap_percent >= SWAP_WARN
  return (
    <Panel title="内存构成" className="h-full" extra={<span>共 {bytes(m.total_bytes)}</span>}>
      <div className="space-y-3 p-3 sm:p-4">
        <div
          role="img"
          aria-label={`已用 ${bytes(m.used_bytes)}，可回收 ${bytes(reclaim)}，空闲 ${bytes(m.free_bytes)}`}
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
          <Fact label="可用" value={bytes(m.available_bytes)} />
          <Fact label="缓存" value={bytes(m.cached_bytes)} />
          <Fact label="缓冲" value={bytes(m.buffers_bytes)} />
        </dl>
        <div className="flex items-center gap-3 text-xs">
          <span className="shrink-0 text-fg-3">交换</span>
          {m.swap_total_bytes ? (
            <>
              <div className="flex-1"><Meter value={m.swap_percent} className={swapWarn ? 'bg-warn' : 'bg-fg-3'} /></div>
              <span className={cx('shrink-0 tabular-nums', swapWarn && 'text-warn')}>{bytes(m.swap_used_bytes)} / {bytes(m.swap_total_bytes)}</span>
            </>
          ) : <span className="text-fg-2">未启用</span>}
        </div>
      </div>
    </Panel>
  )
}

function PowerDetail({ data, history, gpus }: { data: ResourceSnapshot; history?: ResourceHistory; gpus: GPU[] }) {
  const points = history?.points ?? []
  const avgCPU = average(points, (p) => p.cpu_power_w)
  const avgGPU = average(points, (p) => p.gpu_power_w)
  const limit = gpus.reduce((sum, g) => sum + (g.power_limit_w ?? 0), 0)
  const tempWarn = data.gpu_temp_max_c != null && data.gpu_temp_max_c >= GPU_TEMP_WARN
  return (
    <Panel title="功耗与温度" className="h-full">
      <div className="space-y-3 p-3 sm:p-4">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3">
          <Fact label="CPU 封装" title="来自 Intel RAPL 或 amd_energy" value={data.cpu.power_w == null ? '—' : fmtWatts(data.cpu.power_w)} detail={data.cpu.power_w == null ? (data.cpu.power_error ? '读不到' : undefined) : avgCPU == null ? undefined : `平均 ${fmtWatts(avgCPU)}`} />
          <Fact label="GPU" value={data.gpu_power_w == null ? '—' : fmtWatts(data.gpu_power_w)} detail={avgGPU == null ? undefined : `平均 ${fmtWatts(avgGPU)}`} />
          <Fact label="已知合计" value={data.power_w == null ? '—' : fmtWatts(data.power_w)} detail="CPU + GPU" />
          <Fact label="服务启动以来" value={fmtEnergy(data.energy_wh)} detail="已知功耗积分" />
          <Fact label="GPU 最高温度" value={fmtTemp(data.gpu_temp_max_c)} warn={tempWarn} detail={tempWarn ? '偏高' : data.gpu_power_w == null ? '功耗不可用' : '来自 nvidia-smi'} />
          <Fact label="GPU 功耗上限" value={limit > 0 ? fmtWatts(limit) : '—'} detail={limit > 0 ? `${gpus.length} 张卡之和` : undefined} />
        </dl>
        <p className="border-t border-line pt-3 text-[11px] leading-relaxed text-fg-3">
          {data.cpu.power_w == null && data.cpu.power_error && <span className="text-fg-2">CPU 封装读不到：{data.cpu.power_error}。</span>}
          合计只有 CPU 和 GPU，不含内存、硬盘、风扇和电源损耗。
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
      title="存储"
      className="h-full"
      extra={data.disks_updated_at ? <span>分区数据 {ago(data.disks_updated_at)} 秒前</span> : undefined}
    >
      {/* 行内已经写出采集失败的分区时，不再在顶部重复同一句错误 */}
      {data.disks_error && !rows.some((d) => d.error) && <Alert tone="warn" role="alert" className="m-3 sm:m-4">磁盘容量读取失败：{data.disks_error}</Alert>}
      <ColHead className="flex">
        <span className="font-medium text-fg-2">分区</span>
        <span className="ml-auto">可用 / 总量</span>
      </ColHead>
      <div className="divide-y divide-line">
        {rows.length === 0 ? <p className="px-3 py-2 text-ui text-fg-3 sm:px-4">没有读到分区数据</p> : rows.map((disk, i) => <DiskRow key={disk.id || `${disk.path}:${i}`} disk={disk} />)}
      </div>
      <ColHead className={cx('grid border-t', ioCols)}>
        <span className="font-medium text-fg-2">物理盘 I/O</span>
        <span className="text-right">读</span>
        <span className="text-right">写</span>
        <span className="text-right">繁忙</span>
      </ColHead>
      {data.disk_io_error ? <Alert tone="warn" role="alert" className="m-3 sm:m-4">磁盘 I/O 读取失败：{data.disk_io_error}</Alert> : io.length === 0 ? <p className="px-3 py-2 text-xs text-fg-3 sm:px-4">没有读到设备 I/O</p> : (
        <div className="divide-y divide-line text-xs">
          {io.map((d) => (
            <div key={d.name} className={cx('grid items-center gap-3 px-3 py-1.5 sm:px-4', ioCols)}>
              <span className="truncate font-mono" title={d.name}>{d.name}</span>
              <span className="text-right tabular-nums"><span className="sr-only">读 </span>{fmtRate(d.read_bytes_per_sec)}</span>
              <span className="text-right tabular-nums"><span className="sr-only">写 </span>{fmtRate(d.write_bytes_per_sec)}</span>
              <span className="flex items-center justify-end gap-2 tabular-nums">
                <span className="w-10 max-sm:hidden"><Meter value={d.busy_percent} /></span>
                <span className="w-8 text-right"><span className="sr-only">繁忙 </span>{d.busy_percent}%</span>
              </span>
            </div>
          ))}
        </div>
      )}
      {data.disk_io.length > io.length && <p className="border-t border-line px-3 py-1.5 text-[11px] text-fg-3 sm:px-4">还有 {data.disk_io.length - io.length} 个设备</p>}
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
        {disk.data && <span className="shrink-0 whitespace-nowrap text-[11px] text-fg-3">数据目录</span>}
        {disk.warn && !disk.error && <span className="shrink-0 text-xs text-warn">余量偏低</span>}
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

function NetworkPanel({ data }: { data: ResourceSnapshot }) {
  const ifaces = data.network.slice(0, 8)
  // 手机上网卡名独占一行（挤在速率中间会被截成 "e…"，等于没显示），下面一行四个数
  // 1024–1279px 两栏并排时面板只有 480px 上下，数值列收窄、利用率只写数字，把宽度让给网卡名
  const cols = 'grid-cols-[4.5rem_minmax(0,1fr)_minmax(0,1fr)_2.5rem] sm:grid-cols-[minmax(0,1fr)_4.5rem_5.5rem_5.5rem_5.5rem] lg:grid-cols-[minmax(0,1fr)_4rem_5rem_5rem_2.5rem] xl:grid-cols-[minmax(0,1fr)_4.5rem_5.5rem_5.5rem_5.5rem]'
  return (
    <Panel title="网卡" className="h-full" extra={data.network.length > 0 ? <span>{data.network.length} 个</span> : undefined}>
      {data.network_error && <Alert tone="warn" role="alert" className="m-3 sm:m-4">网络数据读取失败：{data.network_error}</Alert>}
      <ColHead className={cx('hidden sm:grid', cols)}>
        <span className="font-medium text-fg-2">接口</span>
        <span className="text-right">链路</span>
        <span className="text-right">接收</span>
        <span className="text-right">发送</span>
        <span className="text-right">利用率</span>
      </ColHead>
      {data.samples < 2 && <p className="px-3 pt-2 text-xs text-fg-3 sm:px-4">等待第二次采样后才有速率</p>}
      {ifaces.length === 0 ? <p className="px-3 py-2 text-xs text-fg-3 sm:px-4">没有读到网卡数据</p> : (
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
                    {n.virtual && <span className="shrink-0 text-[11px] text-fg-3">虚拟</span>}
                    {faults > 0 && <span className="min-w-0 truncate text-warn" title={`错误 ${n.rx_errors + n.tx_errors}，丢弃 ${n.rx_drops + n.tx_drops}`}>错误/丢弃 {faults}</span>}
                  </span>
                  <span className="block truncate text-[11px] text-fg-3 tabular-nums max-sm:ml-auto" title="开机以来收发的总量">累计 ↓ {bytes(n.rx_total_bytes)} · ↑ {bytes(n.tx_total_bytes)}</span>
                </span>
                <span className="text-fg-2 tabular-nums sm:text-right"><span className="sr-only">链路 </span>{speed ?? '—'}</span>
                <span className="text-right tabular-nums"><span className="sm:hidden">↓ </span><span className="sr-only">接收 </span>{fmtRate(n.rx_bytes_per_sec)}</span>
                <span className="text-right tabular-nums"><span className="sm:hidden">↑ </span><span className="sr-only">发送 </span>{fmtRate(n.tx_bytes_per_sec)}</span>
                <span className="flex items-center justify-end gap-2 tabular-nums">
                  {util != null && <span className="w-10 max-sm:hidden lg:max-xl:hidden"><Meter value={util} /></span>}
                  <span className="w-8 text-right"><span className="sr-only">利用率 </span>{util == null ? '—' : `${util}%`}</span>
                </span>
              </div>
            )
          })}
        </div>
      )}
      {data.network.length > ifaces.length && <p className="border-t border-line px-3 py-1.5 text-[11px] text-fg-3 sm:px-4">还有 {data.network.length - ifaces.length} 个网卡</p>}
    </Panel>
  )
}

function ProcessPanel({ data }: { data: ResourceSnapshot }) {
  const byCpu = useMemo(() => [...data.processes].sort((a, b) => b.cpu_percent - a.cpu_percent).slice(0, 8), [data.processes])
  const byMem = useMemo(() => [...data.processes].sort((a, b) => b.rss_bytes - a.rss_bytes).slice(0, 8), [data.processes])
  const processAge = data.processes_at ? ago(data.processes_at) : null
  return (
    <Panel
      title="进程"
      extra={
        <>
          <span>CPU 为最近 10 秒</span>
          {data.process_samples < 2 && <span className="text-warn">CPU 等待第二次采样</span>}
          {processAge != null && <span>进程数据 {processAge} 秒前</span>}
        </>
      }
    >
      <div className="grid gap-0 divide-y divide-line lg:grid-cols-2 lg:divide-x lg:divide-y-0">
        <ProcessList title="按 CPU 占用" sort="cpu" processes={byCpu} cpuKnown={data.process_samples >= 2} />
        <ProcessList title="按内存占用" sort="mem" processes={byMem} cpuKnown={data.process_samples >= 2} />
      </div>
      {data.processes_error && <p role="alert" className="border-t border-line px-3 py-2 text-xs text-warn sm:px-4">进程数据读取失败：{data.processes_error}</p>}
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
        <span className="max-sm:hidden">用户</span>
        <span className={cx('text-right max-sm:hidden', sort === 'cpu' && 'text-fg-2')}>CPU{sort === 'cpu' && ' ↓'}</span>
        <span className={cx('text-right max-sm:hidden', sort === 'mem' && 'text-fg-2')}>内存{sort === 'mem' && ' ↓'}</span>
      </ColHead>
      {processes.length === 0 ? <p className="px-3 py-3 text-ui text-fg-3 sm:px-4">没有读到进程信息</p> : (
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
              <span className={cx('text-right tabular-nums', sort === 'mem' && 'font-medium')}><span className="sr-only">内存 </span>{bytes(p.rss_bytes)}</span>
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
      {age == null ? '等待第一次采样' : stale ? (error ? `数据 ${age} 秒前` : `数据 ${age} 秒没有更新`) : `${age} 秒前 · 每 2 秒采样`}
    </span>
  )
}

export function Resources() {
  const [params, setParams] = useSearchParams()
  const raw = params.get('range') as ResourceRange | null
  const rawValid = RANGES.some((r) => r.value === raw)
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
        <h1 className="text-lg font-semibold">服务器资源</h1>
        <ErrorBox error={current.error ?? '没有读到资源数据'} />
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => current.refetch()}>重试</Button>
          <Link to="/" className={buttonClass('ghost')}>回到总览</Link>
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
  const hostLine = [host.hostname, host.os, host.kernel && `内核 ${host.kernel}`, host.uptime_seconds > 0 && `已运行 ${fmtUptime(host.uptime_seconds)}`].filter(Boolean).join(' · ')
  const gpuList = gpus.data?.gpus ?? []

  return (
    <div className="space-y-4 sm:space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1 space-y-0.5">
          <div className="flex items-center gap-3">
            <h1 className="text-lg font-semibold">服务器资源</h1>
            {/* 手机上新鲜度和标题同一行，省出一行给下面的面板 */}
            <Freshness age={age} stale={stale} error={!!data.error} className="ml-auto sm:hidden" />
          </div>
          {hostLine && <p className="truncate text-ui text-fg-3" title={hostLine}>{hostLine}</p>}
        </div>
        <div className="flex items-center gap-3 max-sm:w-full">
          <Freshness age={age} stale={stale} error={!!data.error} className="max-sm:hidden" />
          <Segmented options={RANGES} value={range} onChange={setRange} label="历史范围" className="max-sm:w-full" />
        </div>
      </div>

      {current.error && (
        <Alert tone="warn" role="alert">
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span>数据更新失败：{(current.error as Error).message}。显示的是 {age == null ? '最近一次' : `${age} 秒前`}的快照。</span>
            <Button size="sm" className="-my-2" onClick={() => current.refetch()}>重试</Button>
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
              {data.error ? '还没有采到 CPU、内存等主机数据，原因见上方。' : '正在进行第一次采样…'}
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
          </div>
          <ProcessPanel data={data} />
          <p className="text-[11px] text-fg-3">历史每 1 小时滚动一次，服务重启后清零；CPU/GPU 功耗为可用来源的估算值（CPU 来自 Intel RAPL 或 amd_energy），电量按本次服务启动以来的采样积分。曲线是区间桶均值，"平均"按曲线算，"峰值"按原始采样点算；在任一张曲线上悬停或横向拖动，所有指标卡一起显示那一刻的数值。</p>
        </>
      )}
    </div>
  )
}
