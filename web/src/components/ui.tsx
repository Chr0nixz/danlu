import { CircleAlert, CircleCheck, CircleDashed, CircleSlash, CircleX, Info, LoaderCircle, TriangleAlert } from 'lucide-react'
import type { ButtonHTMLAttributes, ComponentProps, ReactNode } from 'react'
import type { Job, JobStatus } from '../lib/api'
import { jobStatusText } from '../lib/format'

export function cx(...c: (string | false | null | undefined)[]) {
  return c.filter(Boolean).join(' ')
}

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'
type Size = 'sm' | 'md' | 'icon' | 'icon-sm'

const variants: Record<Variant, string> = {
  primary: 'bg-inv text-inv-fg hover:opacity-85 active:opacity-75',
  secondary: 'border border-line-strong bg-surface text-fg hover:bg-hover active:bg-line',
  ghost: 'text-fg-2 hover:bg-hover hover:text-fg active:bg-line',
  danger: 'border border-line-strong bg-surface text-fail hover:border-fail/40 hover:bg-fail/8 active:bg-fail/12',
}

const sizes: Record<Size, string> = {
  sm: 'h-8 gap-1.5 px-2.5 text-ui',
  md: 'h-9 gap-1.5 px-3.5 text-sm',
  icon: 'size-9',
  'icon-sm': 'size-8',
}

export const buttonClass = (variant: Variant = 'secondary', size: Size = 'md') =>
  cx(
    'inline-flex shrink-0 select-none items-center justify-center rounded-md font-medium whitespace-nowrap transition-[background-color,border-color,color,opacity] duration-150 disabled:pointer-events-none disabled:opacity-45 [&_svg]:size-4 [&_svg]:shrink-0',
    sizes[size],
    variants[variant],
  )

export function Button({
  variant = 'secondary',
  size = 'md',
  loading,
  className,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: Size; loading?: boolean }) {
  return (
    <button type="button" className={cx(buttonClass(variant, size), className)} disabled={loading || rest.disabled} {...rest}>
      {loading ? <LoaderCircle className="animate-spin" /> : null}
      {loading && size.startsWith('icon') ? null : children}
    </button>
  )
}

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cx('rounded-lg border border-line bg-surface', className)}>{children}</div>
}

const field =
  'w-full min-w-0 rounded-md border border-line-strong bg-surface px-3 text-sm text-fg outline-none transition-[border-color,box-shadow] duration-150 hover:border-fg-4 focus:border-fg-3 focus:ring-3 focus:ring-run/15 focus-visible:outline-none disabled:opacity-50'

export function Input({ className, ...rest }: ComponentProps<'input'>) {
  return <input className={cx(field, 'h-9', className)} {...rest} />
}

export function Textarea({ className, ...rest }: ComponentProps<'textarea'>) {
  return <textarea className={cx(field, 'py-2 leading-relaxed', className)} {...rest} />
}

export function Select({ className, children, ...rest }: ComponentProps<'select'>) {
  return (
    <select className={cx(field, 'h-9 pr-8', className)} {...rest}>
      {children}
    </select>
  )
}

export function Field({ label, hint, children, className }: { label: ReactNode; hint?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <label className={cx('block space-y-1.5', className)}>
      <span className="block text-ui font-medium text-fg">{label}</span>
      {children}
      {hint && <span className="block text-xs leading-relaxed text-fg-3">{hint}</span>}
    </label>
  )
}

/** 桌面上标签在左、控件在右的表单行；手机上上下排列 */
export function FormRow({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="grid gap-2 px-4 py-4 sm:px-5 md:grid-cols-[200px_1fr] md:gap-6">
      <div>
        <div className="text-ui font-medium text-fg">{label}</div>
        {hint && <div className="mt-1 text-xs leading-relaxed text-fg-3">{hint}</div>}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

// ---------- 状态 ----------

export function StatusIcon({ status, cancelling, className }: { status: JobStatus; cancelling?: boolean; className?: string }) {
  const c = cx('size-4 shrink-0', className)
  // 调用方传了 text-* 颜色时用调用方的颜色，例如分组标题里的中性灰图标
  const tint = (color: string) => (/(^|\s)text-(fg|ok|fail|warn|run|inv)/.test(className ?? '') ? '' : color)
  switch (status) {
    case 'queued':
      return <CircleDashed className={cx(c, tint('text-fg-3'))} aria-hidden />
    case 'running':
      return <LoaderCircle className={cx(c, 'animate-[spin_1.6s_linear_infinite]', tint(cancelling ? 'text-warn' : 'text-run'))} aria-hidden />
    case 'succeeded':
      return <CircleCheck className={cx(c, tint('text-ok'))} aria-hidden />
    case 'failed':
      return <CircleX className={cx(c, tint('text-fail'))} aria-hidden />
    case 'cancelled':
      return <CircleSlash className={cx(c, tint('text-fg-3'))} aria-hidden />
  }
}

export function StatusLabel({ job, className }: { job: Job; className?: string }) {
  return (
    <span className={cx('inline-flex items-center gap-1.5 text-ui font-medium text-fg', className)}>
      <StatusIcon status={job.status} cancelling={job.cancel_requested} />
      {jobStatusText(job)}
    </span>
  )
}

/** 卡号小方块，例如 [0][1] */
export function GpuChips({ ids, className }: { ids: number[]; className?: string }) {
  return (
    <span className={cx('inline-flex gap-0.5', className)}>
      {ids.map((i) => (
        <span key={i} className="grid h-[18px] min-w-[18px] place-items-center rounded-[4px] border border-line-strong px-1 font-mono text-[11px] leading-none text-fg-2">
          {i}
        </span>
      ))}
    </span>
  )
}

/** commit 短哈希；有未提交改动时后面跟一个琥珀色的点 */
export function Commit({ sha, dirty, className }: { sha: string; dirty?: boolean; className?: string }) {
  if (!sha) return null
  return (
    <span className={cx('inline-flex items-center gap-1 font-mono text-xs text-fg-2', className)} title={dirty ? `${sha}（有未提交的改动）` : sha}>
      {sha.slice(0, 7)}
      {dirty && <span className="size-1.5 rounded-full bg-warn" aria-label="有未提交的改动" />}
    </span>
  )
}

// ---------- 反馈 ----------

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle className={cx('size-4 animate-spin text-fg-3', className)} />
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cx('animate-pulse rounded bg-hover', className)} />
}

/** 列表加载中的占位行 */
export function SkeletonRows({ rows = 3 }: { rows?: number }) {
  return (
    <div className="divide-y divide-line">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-3 px-4 py-3.5">
          <Skeleton className="size-4 rounded-full" />
          <Skeleton className="h-3.5 w-40" />
          <Skeleton className="ml-auto h-3.5 w-16" />
        </div>
      ))}
    </div>
  )
}

const alertStyle = {
  fail: { box: 'border-fail/30 bg-fail/6 text-fail', icon: CircleAlert },
  warn: { box: 'border-warn/30 bg-warn/6 text-warn', icon: TriangleAlert },
  info: { box: 'border-line bg-subtle text-fg-2', icon: Info },
}

export function Alert({ tone = 'info', children, className }: { tone?: keyof typeof alertStyle; children: ReactNode; className?: string }) {
  const s = alertStyle[tone]
  return (
    <div className={cx('flex gap-2 rounded-md border px-3 py-2 text-ui leading-relaxed', s.box, className)}>
      <s.icon className="mt-0.5 size-4 shrink-0" />
      <div className="min-w-0 break-words">{children}</div>
    </div>
  )
}

export function ErrorBox({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null
  const msg = error instanceof Error ? error.message : String(error)
  return (
    <Alert tone="fail" className={className}>
      {msg}
    </Alert>
  )
}

export function Empty({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 px-4 py-8 text-center text-ui text-fg-3">
      <div>{children}</div>
      {action}
    </div>
  )
}

// ---------- 切换 ----------

/** 分段按钮，用于小范围的选项切换 */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  className,
}: {
  options: { value: T; label: ReactNode }[]
  value: T
  onChange: (v: T) => void
  className?: string
}) {
  return (
    <div role="tablist" className={cx('inline-flex h-8 gap-0.5 rounded-md border border-line bg-subtle p-0.5', className)}>
      {options.map((o) => (
        <button
          type="button"
          role="tab"
          aria-selected={value === o.value}
          key={o.value}
          onClick={() => onChange(o.value)}
          className={cx(
            'flex flex-1 items-center justify-center gap-1.5 rounded-[4px] px-2.5 text-ui font-medium whitespace-nowrap transition-colors duration-150 [&_svg]:size-3.5',
            value === o.value ? 'bg-raised text-fg shadow-[0_1px_2px_rgb(0_0_0/0.08)]' : 'text-fg-3 hover:text-fg',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

/** 下划线标签页，用于页面内的主要分区 */
export function Tabs<T extends string>({
  tabs,
  value,
  onChange,
  className,
}: {
  tabs: { value: T; label: ReactNode }[]
  value: T
  onChange: (v: T) => void
  className?: string
}) {
  return (
    <div role="tablist" className={cx('flex gap-1 overflow-x-auto shadow-[inset_0_-1px_0_var(--line)]', className)}>
      {tabs.map((t) => (
        <button
          type="button"
          role="tab"
          aria-selected={value === t.value}
          key={t.value}
          onClick={() => onChange(t.value)}
          className={cx(
            'flex h-10 shrink-0 items-center gap-1.5 border-b-2 px-2.5 text-sm whitespace-nowrap transition-colors duration-150 [&_svg]:size-4',
            value === t.value ? 'border-fg font-medium text-fg' : 'border-transparent text-fg-3 hover:text-fg',
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

export function Meter({ value, className }: { value: number; className?: string }) {
  const v = Math.max(0, Math.min(100, value))
  return (
    <div className="h-1 w-full overflow-hidden rounded-full bg-hover">
      <div className={cx('h-full rounded-full transition-[width] duration-500 ease-out', className)} style={{ width: `${v}%` }} />
    </div>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="hidden h-5 min-w-5 items-center justify-center rounded border border-current/25 px-1 font-sans text-[11px] leading-none opacity-70 lg:inline-flex">{children}</kbd>
}
