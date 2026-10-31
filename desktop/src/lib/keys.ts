import { useEffect, useState } from 'react'
import { typingIn } from './platform'

/**
 * 列表的键盘操作：↑ ↓ 移动光标，Enter 打开右侧详情。右侧已经开着时，详情跟着光标走。
 * 返回光标所在行的 key。
 */
export function useListKeys<T extends { key: string }>(list: T[], selected: string | null, open: (item: T) => void) {
  const [cursor, setCursor] = useState<string | null>(null)
  // 点了某一行，光标也落到那里
  useEffect(() => {
    if (selected) setCursor(selected)
  }, [selected])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (typingIn(e) || e.ctrlKey || e.metaKey || e.altKey || document.querySelector('dialog[open]')) return
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp' && e.key !== 'Enter') return
      if (!list.length) return
      e.preventDefault()
      const i = list.findIndex((x) => x.key === cursor)
      if (e.key === 'Enter') {
        if (i >= 0) open(list[i])
        return
      }
      const next = i < 0 ? 0 : e.key === 'ArrowDown' ? Math.min(list.length - 1, i + 1) : Math.max(0, i - 1)
      setCursor(list[next].key)
      if (selected) open(list[next])
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [list, cursor, selected, open])
  return cursor
}
