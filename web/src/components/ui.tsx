import { CircleAlert, CircleCheck, CircleDashed, CircleSlash, CircleX, Ellipsis, Info, LoaderCircle, TriangleAlert } from 'lucide-react'
import { useEffect, useId, useRef, useState, type AriaRole, type ButtonHTMLAttributes, type ComponentProps, type KeyboardEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router'
import { jobStatusText, statusLabel, type Job, type JobStatus } from '@danlu/core'

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
  'w-full min-w-0 rounded-md border border-line-strong bg-surface px-3 text-sm text-fg outline-none transition-[border-color,box-shadow] duration-150 hover:border-fg-4 focus:border-fg-3 focus:ring-3 focus:ring-run/15 focus-visible:outline-none disabled:opacity-50 aria-invalid:border-fail/60 aria-invalid:focus:ring-fail/15'

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

/** 标签包着控件的简单字段。传 error 时在控件下方显示错误，控件用 aria-describedby 引用 errorId */
export function Field({ label, hint, error, errorId, children, className }: { label: ReactNode; hint?: ReactNode; error?: ReactNode; errorId?: string; children: ReactNode; className?: string }) {
  return (
    <label className={cx('block space-y-1.5', className)}>
      <span className="block text-ui font-medium text-fg">{label}</span>
      {children}
      {hint && <span className="block text-xs leading-relaxed text-fg-3">{hint}</span>}
      {error && (
        <span id={errorId} className="flex items-start gap-1.5 text-xs leading-relaxed text-fail">
          <CircleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
          {error}
        </span>
      )}
    </label>
  )
}

/**
 * 桌面上标签在左、控件在右的表单行；手机上上下排列。
 * 单个控件传 htmlFor（控件的 id），说明文字的 id 是 `${htmlFor}-hint`，控件可以用 aria-describedby 引用；
 * 不传时控件区是一个以标签命名的 group，用于分段按钮加步进器这类组合控件；说明文字是这个 group 的描述，
 * 字段错误的 id 用 describedBy 传进来，也挂在 group 上。
 */
export function FormRow({ label, hint, htmlFor, describedBy, children }: { label: ReactNode; hint?: ReactNode; htmlFor?: string; describedBy?: string; children: ReactNode }) {
  const labelId = useId()
  const hintId = htmlFor ? `${htmlFor}-hint` : `${labelId}-hint`
  const cls = 'block text-ui font-medium text-fg'
  return (
    <div className="grid gap-2 px-4 py-4 sm:px-5 md:grid-cols-[200px_1fr] md:gap-6">
      <div>
        {htmlFor ? (
          <label htmlFor={htmlFor} className={cls}>
            {label}
          </label>
        ) : (
          <div id={labelId} className={cls}>
            {label}
          </div>
        )}
        {hint && (
          <div id={hintId} className="mt-1 text-xs leading-relaxed text-fg-3">
            {hint}
          </div>
        )}
      </div>
      <div
        className="min-w-0"
        {...(htmlFor ? {} : { role: 'group', 'aria-labelledby': labelId, 'aria-describedby': cx(hint ? hintId : undefined, describedBy) || undefined })}
      >
        {children}
      </div>
    </div>
  )
}

/** 字段下方的错误说明，配合控件的 aria-invalid 和 aria-describedby */
export function FieldError({ id, children }: { id: string; children: ReactNode }) {
  if (!children) return null
  return (
    <p id={id} className="mt-1.5 flex items-start gap-1.5 text-xs leading-relaxed text-fail">
      <CircleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
      {children}
    </p>
  )
}

// ---------- 状态 ----------

/**
 * 状态图标。默认带读屏能读出的状态名；旁边已经写着状态文字时传 decorative，避免读两遍。
 */
export function StatusIcon({ status, cancelling, decorative, className }: { status: JobStatus; cancelling?: boolean; decorative?: boolean; className?: string }) {
  const c = cx('size-4 shrink-0', className)
  // 调用方传了 text-* 颜色时用调用方的颜色，例如分组标题里的中性灰图标
  const tint = (color: string) => (/(^|\s)text-(fg|ok|fail|warn|run|inv)/.test(className ?? '') ? '' : color)
  const a11y = decorative
    ? { 'aria-hidden': true }
    : { role: 'img' as AriaRole, 'aria-hidden': false, 'aria-label': status === 'running' && cancelling ? '取消中' : statusLabel[status] }
  switch (status) {
    case 'queued':
      return <CircleDashed className={cx(c, tint('text-fg-3'))} {...a11y} />
    case 'running':
      return <LoaderCircle className={cx(c, 'animate-[spin_1.6s_linear_infinite]', tint(cancelling ? 'text-warn' : 'text-run'))} {...a11y} />
    case 'succeeded':
      return <CircleCheck className={cx(c, tint('text-ok'))} {...a11y} />
    case 'failed':
      return <CircleX className={cx(c, tint('text-fail'))} {...a11y} />
    case 'cancelled':
      return <CircleSlash className={cx(c, tint('text-fg-3'))} {...a11y} />
  }
}

export function StatusLabel({ job, className }: { job: Job; className?: string }) {
  return (
    <span className={cx('inline-flex items-center gap-1.5 text-ui font-medium text-fg', className)}>
      <StatusIcon status={job.status} cancelling={job.cancel_requested} decorative />
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

/**
 * 字号小的链接和按钮：点击区向四周撑出一圈（左右 8px、上下 10px，12px 的字撑到 36px 高），不改变排版。
 * 撑出去的范围里不能有别的可点元素。
 */
export const hitArea = 'relative before:absolute before:-inset-x-2 before:-inset-y-2.5'

/** commit 短哈希；有未提交改动时后面跟一个琥珀色的点。传 to 时是链接，通常指向任务的代码快照 */
export function Commit({ sha, dirty, to, className }: { sha: string; dirty?: boolean; to?: string; className?: string }) {
  if (!sha) return null
  const title = dirty ? `${sha}（有未提交的改动）` : sha
  const body = (
    <>
      {sha.slice(0, 7)}
      {dirty && (
        <>
          <span className="size-1.5 shrink-0 rounded-full bg-warn" aria-hidden />
          <span className="sr-only">，有未提交的改动</span>
        </>
      )}
    </>
  )
  const cls = cx('inline-flex items-center gap-1 font-mono text-xs text-fg-2', className)
  // 任务行里它浮在整行链接上面，只有字那么大的话手机上很容易点到整行
  return to ? (
    <Link to={to} className={cx(cls, hitArea, 'decoration-line-strong hover:text-fg hover:underline')} title={`${title}，查看这次运行的代码`}>
      {body}
    </Link>
  ) : (
    <span className={cls} title={title}>
      {body}
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

export function Alert({ tone = 'info', role, children, className }: { tone?: keyof typeof alertStyle; role?: AriaRole; children: ReactNode; className?: string }) {
  const s = alertStyle[tone]
  return (
    <div role={role} className={cx('flex gap-2 rounded-md border px-3 py-2 text-ui leading-relaxed', s.box, className)}>
      <s.icon className="mt-0.5 size-4 shrink-0" aria-hidden />
      <div className="min-w-0 flex-1 break-words">{children}</div>
    </div>
  )
}

/** 操作或加载出错时的提示；role=alert，出现时读屏会立刻读出来 */
export function ErrorBox({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null
  const msg = error instanceof Error ? error.message : String(error)
  return (
    <Alert tone="fail" role="alert" className={className}>
      {msg}
    </Alert>
  )
}

/**
 * 确认框：原生 <dialog>，Esc 和点遮罩都能关，焦点先落在第一个按钮上（放不会造成损失的那个）。
 * 手机上贴着底部，拇指够得到。挂载即打开，关闭时调用 onClose，由调用方卸载。
 * 挂到 body 下，免得悬停在框上时触发列表行的 hover 样式。
 */
export function Dialog({ title, children, footer, onClose, className, wide = false }: { title: ReactNode; children: ReactNode; footer: ReactNode; onClose: () => void; className?: string; wide?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    const d = ref.current
    if (d && !d.open) d.showModal()
  }, [])
  return createPortal(
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onClose={onClose}
      onClick={(e) => e.target === e.currentTarget && e.currentTarget.close()}
      className={cx('m-auto w-[calc(100%-2rem)] rounded-lg border border-line bg-surface p-0 text-fg shadow-[0_8px_24px_rgb(0_0_0/0.12)] backdrop:bg-black/40 max-sm:mb-[calc(1rem+env(safe-area-inset-bottom))]',
        wide ? 'max-w-xl' : 'max-w-[420px]', className)}
    >
      <div className={cx('px-4 pt-4 pb-4 sm:px-5', wide && 'min-h-0 flex-1 overflow-y-auto')}>
        <h2 id={titleId} className="text-sm font-semibold">
          {title}
        </h2>
        <div className="mt-1.5 space-y-2 text-ui leading-relaxed text-fg-2">{children}</div>
      </div>
      <div className="flex shrink-0 gap-2 rounded-b-lg border-t border-line bg-subtle px-4 py-3 sm:justify-end sm:px-5 [&>*]:flex-1 max-sm:[&>*]:h-11 sm:[&>*]:flex-none">{footer}</div>
    </dialog>,
    document.body,
  )
}

/**
 * 更多操作：一个按钮展开一列操作，放一行放不下、又不常用的那些。Enter、空格或 ↓ 打开并聚焦第一项，
 * ↑ ↓ Home End 在项之间移动，Esc 或点外面关闭（Esc 把焦点还给按钮），Tab 离开时关闭，点了某一项也关闭。
 * 子元素是 role="menuitem" 的链接或按钮，样式用 menuItemClass。
 */
export function Menu({ label, children, className, buttonClassName }: { label: string; children: ReactNode; className?: string; buttonClassName?: string }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const id = useId()
  const items = () => [...(root.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]
  useEffect(() => {
    if (!open) return
    items()[0]?.focus()
    const outside = (e: PointerEvent) => !root.current?.contains(e.target as Node) && setOpen(false)
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [open])
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!open) return
    const list = items()
    const i = list.indexOf(document.activeElement as HTMLElement)
    const to = ({ ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: list.length - 1 } as Record<string, number>)[e.key]
    if (to !== undefined) {
      e.preventDefault()
      list[(to + list.length) % list.length]?.focus()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      setOpen(false)
      button.current?.focus()
    } else if (e.key === 'Tab') setOpen(false)
  }
  return (
    <div ref={root} className={cx('relative', className)} onKeyDown={onKeyDown}>
      <button
        ref={button}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-label={label}
        title={label}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' && !open) {
            e.preventDefault()
            setOpen(true)
          }
        }}
        className={cx(buttonClass('secondary', 'icon'), open && 'bg-hover', buttonClassName)}
      >
        <Ellipsis aria-hidden />
      </button>
      {open && (
        <div
          id={id}
          role="menu"
          aria-label={label}
          onClick={() => setOpen(false)}
          className="absolute right-0 top-full z-20 mt-1 min-w-44 rounded-md border border-line bg-surface py-1 shadow-[0_8px_24px_rgb(0_0_0/0.12)]"
        >
          {children}
        </div>
      )}
    </div>
  )
}

export const menuItemClass =
  'flex h-11 w-full items-center gap-2.5 px-3 text-left text-sm text-fg-2 whitespace-nowrap hover:bg-hover hover:text-fg focus-visible:bg-hover focus-visible:text-fg focus-visible:-outline-offset-2 disabled:opacity-45 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-fg-3'

/**
 * 读屏播报。状态变化这类只靠颜色和整行闪一下表达的事，同时读一句话出来。整个应用只挂一个 <Announcer />，
 * 同一轮里的几条合成一句，几秒后清空，下次同样的话还能再读。
 */
let announceTo: ((text: string) => void) | null = null
export const announce = (text: string) => announceTo?.(text)

export function Announcer() {
  const [text, setText] = useState('')
  useEffect(() => {
    let timer = 0
    announceTo = (t) => {
      setText((prev) => (prev ? `${prev}；${t}` : t))
      window.clearTimeout(timer)
      timer = window.setTimeout(() => setText(''), 5000)
    }
    return () => {
      announceTo = null
      window.clearTimeout(timer)
    }
  }, [])
  return (
    <div role="status" aria-live="polite" className="sr-only">
      {text}
    </div>
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

/** 一组选项里只有选中的那个能 Tab 到，左右方向键和 Home / End 切换并选中（WAI-ARIA 的 radiogroup / tablist 约定） */
function arrowSelect<T>(e: KeyboardEvent<HTMLElement>, values: T[], value: T, onChange: (v: T) => void) {
  const i = values.indexOf(value)
  const to = ({ ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: values.length - 1 } as Record<string, number>)[e.key]
  if (to === undefined) return
  e.preventDefault()
  const n = (to + values.length) % values.length
  onChange(values[n])
  e.currentTarget.querySelectorAll<HTMLElement>('[data-opt]')[n]?.focus()
}

/** 分段按钮，用于筛选和小范围的选项切换；语义上是单选组 */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  label,
  className,
}: {
  options: { value: T; label: ReactNode }[]
  value: T
  onChange: (v: T) => void
  /** 给读屏的组名，例如"按状态筛选" */
  label: string
  className?: string
}) {
  return (
    <div
      role="radiogroup"
      aria-label={label}
      onKeyDown={(e) => arrowSelect(e, options.map((o) => o.value), value, onChange)}
      className={cx('inline-flex h-8 gap-0.5 rounded-md border border-line bg-subtle p-0.5', className)}
    >
      {options.map((o) => (
        <button
          type="button"
          role="radio"
          aria-checked={value === o.value}
          tabIndex={value === o.value ? 0 : -1}
          data-opt
          key={o.value}
          onClick={() => onChange(o.value)}
          className={cx(
            'flex flex-1 items-center justify-center gap-1.5 rounded-[4px] px-2.5 text-ui font-medium whitespace-nowrap transition-colors duration-150 [&_svg]:size-3.5',
            value === o.value ? 'bg-raised text-fg shadow-[0_1px_2px_rgb(0_0_0/0.08)] ring-1 ring-line' : 'text-fg-3 hover:text-fg',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

/**
 * 下划线标签页，用于页面内的主要分区。传 id 时，标签的 id 是 `${id}-tab-${value}`，
 * 内容区用 tabPanelProps(id, value) 拿到对应的 role、id 和 aria-labelledby。
 */
export function Tabs<T extends string>({
  tabs,
  value,
  onChange,
  id,
  className,
}: {
  tabs: { value: T; label: ReactNode }[]
  value: T
  onChange: (v: T) => void
  id?: string
  className?: string
}) {
  return (
    <div
      role="tablist"
      onKeyDown={(e) => arrowSelect(e, tabs.map((t) => t.value), value, onChange)}
      className={cx('flex gap-1 overflow-x-auto shadow-[inset_0_-1px_0_var(--line)]', className)}
    >
      {tabs.map((t) => (
        <button
          type="button"
          role="tab"
          id={id && `${id}-tab-${t.value}`}
          aria-controls={id && `${id}-panel`}
          aria-selected={value === t.value}
          tabIndex={value === t.value ? 0 : -1}
          data-opt
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

export const tabPanelProps = (id: string, value: string) => ({ role: 'tabpanel' as AriaRole, id: `${id}-panel`, 'aria-labelledby': `${id}-tab-${value}` })

/** 4px 细条。数值变化时用 transform 过渡，不触发重新布局（首页一次有十几根，每次轮询都会变） */
export function Meter({ value, className }: { value: number; className?: string }) {
  const v = Math.max(0, Math.min(100, value))
  return (
    <div className="h-1 w-full overflow-hidden rounded-full bg-hover">
      <div className={cx('h-full origin-left transition-transform duration-500 ease-out', className)} style={{ transform: `scaleX(${v / 100})` }} />
    </div>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="hidden h-5 min-w-5 items-center justify-center rounded border border-current/25 px-1 font-sans text-[11px] leading-none opacity-70 lg:inline-flex">{children}</kbd>
}
