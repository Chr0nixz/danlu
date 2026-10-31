import { getCurrentWindow } from '@tauri-apps/api/window'
import { Copy, Flame, Minus, Search, Square, X } from 'lucide-react'
import { useEffect, useState, type Ref } from 'react'
import { isMac, MOD } from '../lib/platform'
import { cx, Kbd } from './ui'

/**
 * 自己绘制的标题栏：左边火焰标志，中间筛选框，Windows 上右边是窗口按钮；
 * macOS 上窗口按钮换成系统的红黄绿圆点（留出左边的位置）。空白处可以拖动窗口、双击最大化。
 */
export function TitleBar({ search, onSearch, inputRef }: { search: string; onSearch: (v: string) => void; inputRef: Ref<HTMLInputElement> }) {
  return (
    <header data-tauri-drag-region className={cx('flex h-[38px] shrink-0 items-center', isMac ? 'pl-[78px]' : 'pl-3')}>
      <span data-tauri-drag-region className="flex w-[180px] items-center gap-2">
        <span className="grid size-[18px] place-items-center rounded-[5px] bg-brand text-white" aria-hidden>
          <Flame className="size-3" strokeWidth={2.5} />
        </span>
        <span data-tauri-drag-region className="font-semibold tracking-[0.02em]">
          丹炉
        </span>
      </span>
      <span data-tauri-drag-region className="flex flex-1 justify-center">
        <label className="flex h-[26px] w-[min(420px,100%)] items-center gap-2 rounded-[6px] border border-line bg-panel/70 px-2 text-fg-3 transition-colors duration-[120ms] focus-within:border-line-strong focus-within:bg-panel">
          <Search className="size-3.5 shrink-0" />
          <input
            ref={inputRef}
            value={search}
            onChange={(e) => onSearch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.stopPropagation()
                if (search) onSearch('')
                else e.currentTarget.blur()
              }
            }}
            placeholder="筛选任务：编号、名称、命令、目录"
            aria-label="筛选任务"
            className="min-w-0 flex-1 bg-transparent text-fg outline-none placeholder:text-fg-3"
            spellCheck={false}
          />
          {!search && <Kbd>{MOD} K</Kbd>}
        </label>
      </span>
      <span data-tauri-drag-region className="flex w-[180px] justify-end self-stretch">
        {!isMac && <WindowButtons />}
      </span>
    </header>
  )
}

function WindowButtons() {
  const win = getCurrentWindow()
  const [maximized, setMaximized] = useState(false)
  useEffect(() => {
    win.isMaximized().then(setMaximized)
    const off = win.onResized(() => win.isMaximized().then(setMaximized))
    return () => {
      off.then((f) => f())
    }
  }, [win])
  const btn = 'grid h-full w-[46px] place-items-center text-fg-2 transition-colors duration-[120ms] [&_svg]:size-3.5'
  return (
    <>
      <button type="button" className={cx(btn, 'hover:bg-hover hover:text-fg')} aria-label="最小化" onClick={() => win.minimize()}>
        <Minus />
      </button>
      <button type="button" className={cx(btn, 'hover:bg-hover hover:text-fg')} aria-label={maximized ? '还原' : '最大化'} onClick={() => win.toggleMaximize()}>
        {maximized ? <Copy className="-scale-x-100" /> : <Square className="!size-3" />}
      </button>
      {/* 关闭只是缩回托盘，退出在托盘菜单里 */}
      <button type="button" className={cx(btn, 'hover:bg-[#c42b1c] hover:text-white')} aria-label="关闭到托盘" title="关闭到托盘" onClick={() => win.close()}>
        <X />
      </button>
    </>
  )
}
