import { Check, ChevronDown } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { cx } from './ui'

/** 筛选下拉：[服务器▾] [项目▾] [状态▾]。选了值的按钮带底色 */
export function FilterMenu<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: T | null
  options: { value: T; label: string; count?: number }[]
  onChange: (v: T | null) => void
}) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(false)
    window.addEventListener('mousedown', close)
    return () => window.removeEventListener('mousedown', close)
  }, [open])
  const current = options.find((o) => o.value === value)
  return (
    <div ref={box} className="relative">
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => e.key === 'Escape' && open && (e.stopPropagation(), setOpen(false))}
        className={cx(
          'inline-flex h-6 items-center gap-1 rounded-[6px] px-2 text-[11.5px] transition-colors duration-[120ms]',
          current ? 'bg-hover font-medium text-fg shadow-[inset_0_0_0_1px_var(--line-strong)]' : 'text-fg-2 hover:bg-hover hover:text-fg',
        )}
      >
        {current ? current.label : label}
        <ChevronDown className="size-3 text-fg-3" />
      </button>
      {open && (
        <div role="listbox" className="pop absolute top-full left-0 z-30 mt-1 min-w-[150px] rounded-[8px] border border-line-strong bg-panel p-1 shadow-float">
          {[{ value: null as T | null, label: `全部${label}` }, ...options].map((o) => (
            <button
              key={o.value ?? '*'}
              type="button"
              role="option"
              aria-selected={o.value === value}
              onClick={() => {
                onChange(o.value)
                setOpen(false)
              }}
              className="flex h-7 w-full items-center gap-2 rounded-[5px] px-2 text-left hover:bg-hover"
            >
              <Check className={cx('size-3.5', o.value === value ? 'text-fg' : 'text-transparent')} />
              <span className="flex-1 truncate">{o.label}</span>
              {'count' in o && o.count !== undefined && <span className="num text-[11px] text-fg-3">{o.count}</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
