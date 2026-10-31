import { jobStatusText, type Job, type JobStatus } from '@danlu/core'
import { Ban, Check, CircleDashed, LoaderCircle, X } from 'lucide-react'
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ComponentProps, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

export function cx(...c: (string | false | null | undefined)[]) {
  return c.filter(Boolean).join(' ')
}

// ---------- 按钮 ----------

// 主按钮是反色的墨（深色模式白底黑字），不用彩色；危险操作只把文字染成失败红
const buttonStyle = {
  primary: 'bg-primary text-on-primary hover:opacity-90',
  secondary: 'border border-line-strong bg-panel text-fg hover:bg-hover',
  ghost: 'text-fg-2 hover:bg-hover hover:text-fg',
  danger: 'border border-line-strong bg-panel text-fail hover:bg-hover',
}

export function Button({
  variant = 'secondary',
  loading,
  className,
  children,
  disabled,
  ...rest
}: ComponentProps<'button'> & { variant?: keyof typeof buttonStyle; loading?: boolean }) {
  return (
    <button
      type="button"
      disabled={disabled || loading}
      className={cx(
        'inline-flex h-7 shrink-0 items-center justify-center gap-1.5 rounded-[6px] px-2.5 font-medium whitespace-nowrap transition-colors duration-[120ms] disabled:pointer-events-none disabled:opacity-45 [&_svg]:size-3.5 [&_svg]:shrink-0',
        buttonStyle[variant],
        className,
      )}
      {...rest}
    >
      {loading && <LoaderCircle className="animate-spin" />}
      {children}
    </button>
  )
}

export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd className={cx('num inline-flex h-[18px] items-center rounded-[4px] border border-line-strong px-1 text-[10.5px] leading-none text-fg-3', className)}>
      {children}
    </kbd>
  )
}

// ---------- 表单 ----------

export const inputClass =
  'h-7 w-full rounded-[6px] border border-line-strong bg-panel px-2 text-fg outline-none transition-colors duration-[120ms] placeholder:text-fg-3 focus:border-fg-3 disabled:opacity-50'

export function Field({ label, hint, children, className }: { label: ReactNode; hint?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <label className={cx('block space-y-1', className)}>
      <span className="flex items-baseline justify-between gap-2 text-fg-2">
        <span className="font-medium">{label}</span>
        {hint && <span className="truncate text-[11.5px] text-fg-3">{hint}</span>}
      </span>
      {children}
    </label>
  )
}

/** 一组互斥选项，选中项用底色和细边区分 */
export function Segmented<T extends string>({
  value,
  onChange,
  options,
  className,
}: {
  value: T
  onChange: (v: T) => void
  options: { value: T; label: ReactNode; disabled?: boolean }[]
  className?: string
}) {
  return (
    <div role="radiogroup" className={cx('inline-flex h-7 items-center gap-0.5 rounded-[6px] bg-hover p-0.5', className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          disabled={o.disabled}
          onClick={() => onChange(o.value)}
          className={cx(
            'h-6 rounded-[5px] px-2 whitespace-nowrap transition-colors duration-[120ms] disabled:opacity-40',
            value === o.value ? 'bg-panel font-medium text-fg shadow-[0_0_0_1px_var(--line-strong)]' : 'text-fg-3 hover:text-fg',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

// ---------- 状态 ----------

export const statusColor: Record<JobStatus, string> = {
  running: 'text-run',
  succeeded: 'text-ok',
  failed: 'text-fail',
  queued: 'text-fg-3',
  cancelled: 'text-fg-3',
}

/** 状态的颜色值，给闪烁动画用 */
export const statusVar: Record<JobStatus, string> = {
  running: 'var(--run)',
  succeeded: 'var(--ok)',
  failed: 'var(--fail)',
  queued: 'var(--fg-3)',
  cancelled: 'var(--fg-3)',
}

/** 状态图标：不读文字也能分清。运行中匀速转圈 */
export function StatusIcon({ status, className }: { status: JobStatus; className?: string }) {
  const c = cx('size-3.5 shrink-0', statusColor[status], className)
  switch (status) {
    case 'running':
      return <LoaderCircle className={cx(c, 'animate-spin [animation-duration:1.6s]')} aria-hidden />
    case 'queued':
      return <CircleDashed className={c} aria-hidden />
    case 'succeeded':
      return <Check className={c} strokeWidth={2.5} aria-hidden />
    case 'failed':
      return <X className={c} strokeWidth={2.5} aria-hidden />
    default:
      return <Ban className={c} aria-hidden />
  }
}

export function StatusLabel({ job }: { job: Job }) {
  return (
    <span className={cx('inline-flex items-center gap-1.5 font-medium', statusColor[job.status])}>
      <StatusIcon status={job.status} />
      {jobStatusText(job)}
    </span>
  )
}

export function ErrorText({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null
  return <p className={cx('selectable whitespace-pre-line text-fail', className)}>{error instanceof Error ? error.message : String(error)}</p>
}

export function Empty({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-1.5 p-8 text-center text-fg-3">
      <div className="text-[13px] font-medium text-fg-2">{title}</div>
      {children}
    </div>
  )
}

/** 细进度条 */
export function Progress({ pct, className }: { pct: number; className?: string }) {
  return (
    <div className={cx('h-1 overflow-hidden rounded-full bg-hover', className)}>
      <div className="h-full rounded-full bg-run transition-[width] duration-300" style={{ width: `${Math.max(2, pct)}%` }} />
    </div>
  )
}

// ---------- 确认框 ----------

/**
 * 原生 <dialog>：Esc 和点遮罩都能关，焦点先落在第一个按钮上（放不会造成损失的那个）。
 * 挂载即打开，关闭时调用 onClose，由调用方卸载。
 */
export function Dialog({ title, children, footer, onClose }: { title: ReactNode; children: ReactNode; footer: ReactNode; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const d = ref.current
    if (d && !d.open) d.showModal()
  }, [])
  return createPortal(
    <dialog
      ref={ref}
      onClose={onClose}
      onClick={(e) => e.target === e.currentTarget && e.currentTarget.close()}
      className="pop m-auto w-[400px] rounded-[10px] border border-line-strong bg-panel p-0 text-fg shadow-float backdrop:bg-black/35"
    >
      <div className="px-4 pt-4 pb-3">
        <h2 className="text-[13px] font-semibold">{title}</h2>
        <div className="mt-1.5 space-y-2 leading-relaxed text-fg-2">{children}</div>
      </div>
      <div className="flex justify-end gap-2 px-4 pb-4">{footer}</div>
    </dialog>,
    document.body,
  )
}

export const closeDialog = (e: React.MouseEvent) => e.currentTarget.closest('dialog')?.close()

// ---------- 提示条 ----------

type Toast = { id: number; text: string; tone?: 'fail' }
const ToastCtx = createContext<(text: string, tone?: 'fail') => void>(() => {})

export const useToast = () => useContext(ToastCtx)

/** 操作结果的提示条，右下角，4 秒后消失 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const push = useCallback((text: string, tone?: 'fail') => {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t.slice(-2), { id, text, tone }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 4000)
  }, [])
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="pointer-events-none fixed right-4 bottom-4 z-50 flex flex-col items-end gap-2" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={cx(
              'rise max-w-[420px] rounded-[6px] border border-line-strong bg-panel px-3 py-2 shadow-float',
              t.tone === 'fail' ? 'text-fail' : 'text-fg',
            )}
          >
            {t.text}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}
