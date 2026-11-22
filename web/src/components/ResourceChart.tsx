import { useRef } from 'react'
import { t } from '../lib/i18n'
import { cx } from './ui'

export type ChartPoint = { at: number; value: number | null }
export type ChartSeries = {
  label: string
  points: ChartPoint[]
  dashed?: boolean
}

const W = 320
const H = 100

function niceMax(value: number, binary: boolean) {
  if (!Number.isFinite(value) || value <= 0) return 1
  if (binary) {
    // 字节按 1024 进位显示，刻度也按 1024 取整：写成 512.0 MB/s、1.0 GB/s，而不是 476.8 MB/s
    const unit = 1024 ** Math.max(0, Math.floor(Math.log(value) / Math.log(1024)))
    const n = value / unit
    return ([1, 2, 5, 10, 20, 50, 100, 200, 500].find((s) => n <= s) ?? 1024) * unit
  }
  const power = 10 ** Math.floor(Math.log10(value))
  const n = value / power
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10
  return step * power
}

function medianGap(times: number[]) {
  const sorted = [...new Set(times)].sort((a, b) => a - b)
  if (sorted.length < 2) return 0
  const gaps = sorted.slice(1).map((t, i) => t - sorted[i]).sort((a, b) => a - b)
  return gaps[Math.floor(gaps.length / 2)]
}

/** 采样空档按中位间隔的 3 倍断开：卡住的时段不能画成一条直线，悬停时也不能吸到很远的点上 */
export function breakGapOf(times: number[]) {
  return Math.max(medianGap(times) * 3, 6)
}

/** 离 at 最近、且不在采样空档里的点；没有就是 null */
export function nearestAt<T extends { at: number }>(points: T[], at: number | null): T | null {
  if (at == null || points.length === 0) return null
  let best = points[0]
  for (const p of points) if (Math.abs(p.at - at) < Math.abs(best.at - at)) best = p
  return Math.abs(best.at - at) <= breakGapOf(points.map((p) => p.at)) / 2 ? best : null
}

export function clock(at: number, withSeconds: boolean) {
  const d = new Date(at * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return withSeconds ? `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}` : `${p(d.getHours())}:${p(d.getMinutes())}`
}

/**
 * 资源页指标卡里的小面积图。不引图表库：x 轴按时间，y 轴固定百分比或自动取整，
 * 刻度上限写在左上角；第一条线实线带浅色填充，其余虚线。
 * cursor 是整排指标卡共用的悬停时刻：在任何一张图上移动，所有图在同一时刻画竖线和圆点。
 */
export function ResourceChart({
  series,
  from,
  to,
  max,
  format,
  label,
  empty = t('resources.chart_empty'),
  binary = false,
  cursor = null,
  onCursor,
  className,
}: {
  series: ChartSeries[]
  from: number
  to: number
  max?: number
  format: (value: number) => string
  label: string
  empty?: string
  /** 数值是字节：纵轴上限按 1024 取整 */
  binary?: boolean
  /** 悬停的时刻（已经吸到最近的采样点）；null 表示没有悬停 */
  cursor?: number | null
  onCursor?: (at: number | null) => void
  /** 高度由调用方给（h-14 之类） */
  className?: string
}) {
  const plot = useRef<HTMLDivElement>(null)
  const allPoints = series.flatMap((s) => s.points)
  const values = allPoints.map((p) => p.value).filter((v): v is number => v != null && Number.isFinite(v))
  const hasData = values.length >= 2
  const top = max ?? niceMax(Math.max(0, ...values), binary)
  const breakGap = breakGapOf(allPoints.map((p) => p.at))
  const span = Math.max(1, to - from)

  const xOf = (at: number) => ((at - from) / span) * W
  const yOf = (value: number) => (1 - Math.max(0, Math.min(1, value / top))) * H

  // 按空档切成若干段：线逐段画，填充逐段闭合到基线
  const segmentsOf = (points: ChartPoint[]) => {
    const segments: [number, number][][] = []
    let current: [number, number][] = []
    let last: number | null = null
    for (const p of points) {
      if (p.value == null || !Number.isFinite(p.value)) {
        if (current.length) segments.push(current)
        current = []
        last = null
        continue
      }
      if (last != null && p.at - last > breakGap && current.length) {
        segments.push(current)
        current = []
      }
      current.push([xOf(p.at), yOf(p.value)])
      last = p.at
    }
    if (current.length) segments.push(current)
    return segments
  }
  const linePath = (segments: [number, number][][]) =>
    segments.map((seg) => seg.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`).join(' ')).join(' ')
  const areaPath = (segments: [number, number][][]) =>
    segments
      .filter((seg) => seg.length > 1)
      .map((seg) => `M${seg[0][0].toFixed(1)},${H} ${seg.map(([x, y]) => `L${x.toFixed(1)},${y.toFixed(1)}`).join(' ')} L${seg[seg.length - 1][0].toFixed(1)},${H} Z`)
      .join(' ')

  // 圆点：悬停时画在悬停时刻上，否则画在每条线最新的点上；带序号，某条线暂时没数据时颜色仍对得上
  const dots = series
    .map((s, index) => {
      const point = cursor != null ? s.points.find((p) => p.at === cursor) : [...s.points].reverse().find((p) => p.value != null)
      return point && point.value != null && Number.isFinite(point.value) ? { key: s.label, point, index } : null
    })
    .filter((d): d is { key: string; point: ChartPoint; index: number } => d != null)

  if (!hasData) {
    return <div className={cx('flex items-center justify-center rounded-md bg-subtle px-2 text-center text-xs text-fg-3', className)}>{empty}</div>
  }

  const latest = dots.find((d) => d.index === 0 && cursor == null)
  const ariaLabel = latest ? t('resources.chart_aria_latest', { label, value: format(latest.point.value as number) }) : label
  const move = (clientX: number) => {
    const rect = plot.current?.getBoundingClientRect()
    if (!rect || rect.width <= 0 || !onCursor) return
    onCursor(from + Math.max(0, Math.min(1, (clientX - rect.left) / rect.width)) * span)
  }

  return (
    <div
      ref={plot}
      role="img"
      aria-label={ariaLabel}
      className={cx('relative select-none', className)}
      // 横向拖动用来看数值，竖向仍然滚动页面
      style={{ touchAction: 'pan-y' }}
      onPointerMove={(e) => move(e.clientX)}
      onPointerDown={(e) => move(e.clientX)}
      onPointerLeave={(e) => { if (e.pointerType === 'mouse') onCursor?.(null) }}
      onPointerUp={(e) => { if (e.pointerType !== 'mouse') onCursor?.(null) }}
      onPointerCancel={() => onCursor?.(null)}
    >
      {/* 刻度：顶线是上限（虚线），底线是 0；上限的数值压在顶线左端 */}
      <div className="absolute inset-x-0 top-1.5 border-t border-dashed border-line" aria-hidden />
      <div className="absolute inset-x-0 bottom-0 border-t border-line" aria-hidden />
      <span className="absolute left-0 top-0 bg-surface pr-1.5 text-[11px] leading-3 text-fg-3 tabular-nums" aria-hidden>{format(top)}</span>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="absolute inset-x-0 bottom-0 top-1.5 h-[calc(100%-0.375rem)] w-full overflow-visible" aria-hidden>
        {series.map((s, i) => {
          const segments = segmentsOf(s.points)
          if (!segments.length) return null
          return (
            <g key={s.label}>
              {i === 0 && <path d={areaPath(segments)} fill="color-mix(in oklab, var(--fg) 7%, transparent)" />}
              <path
                d={linePath(segments)}
                fill="none"
                stroke={i === 0 ? 'var(--fg-2)' : 'var(--fg-3)'}
                strokeWidth={i === 0 ? 1.5 : 1.25}
                strokeDasharray={s.dashed ? '3 3' : undefined}
                strokeLinejoin="round"
                strokeLinecap="round"
                vectorEffect="non-scaling-stroke"
              />
            </g>
          )
        })}
        {cursor != null && cursor >= from && cursor <= to && (
          <line x1={xOf(cursor)} x2={xOf(cursor)} y1={0} y2={H} stroke="var(--fg-3)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
        )}
      </svg>
      {/* 圆点画在 SVG 外：SVG 是 preserveAspectRatio="none" 拉伸的，里面的圆会被拉成椭圆 */}
      <div className="pointer-events-none absolute inset-x-0 bottom-0 top-1.5" aria-hidden>
        {dots.map(({ key, point, index }) => (
          <span
            key={key}
            className={cx('absolute size-[5px] -translate-x-1/2 -translate-y-1/2 rounded-full', cursor != null && 'ring-2 ring-surface')}
            style={{
              left: `${(xOf(point.at) / W) * 100}%`,
              top: `${(yOf(point.value as number) / H) * 100}%`,
              backgroundColor: index === 0 ? 'var(--fg)' : 'var(--fg-3)',
            }}
          />
        ))}
      </div>
    </div>
  )
}
