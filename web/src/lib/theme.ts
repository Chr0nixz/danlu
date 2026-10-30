export type Theme = 'system' | 'light' | 'dark'

const KEY = 'danlu-theme'

export function getTheme(): Theme {
  try {
    const t = localStorage.getItem(KEY)
    if (t === 'light' || t === 'dark') return t
  } catch {
    // 隐私模式等情况下 localStorage 不可用，按跟随系统处理
  }
  return 'system'
}

/** 保存并立即应用；index.html 里的内联脚本会在下次加载时提前应用，避免闪烁 */
export function setTheme(t: Theme) {
  try {
    if (t === 'system') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, t)
  } catch {
    // 同上
  }
  if (t === 'system') delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = t
}
