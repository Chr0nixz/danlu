import { useSyncExternalStore } from 'react'

// 只存在当前浏览器里的偏好：跟屏幕大小、视力、输入方式有关，手机和电脑各设各的。
// 浏览器禁用了站点存储（隐私模式、屏蔽 Cookie）时读写会抛错，用默认值，不能让页面打不开。

function read(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    // 存不下时只是下次打开回到默认值
  }
}

// ---------- 网页终端字号 ----------

const terminalKey = 'danlu.terminal.fontSize'
export const TERMINAL_FONT = { min: 11, max: 20, default: 13 }

export function storedTerminalFontSize() {
  const value = Number(read(terminalKey))
  return Number.isInteger(value) && value >= TERMINAL_FONT.min && value <= TERMINAL_FONT.max ? value : TERMINAL_FONT.default
}

export function saveTerminalFontSize(size: number) {
  write(terminalKey, size === TERMINAL_FONT.default ? null : String(size))
}

// ---------- 日志和代码字号 ----------

// index.html 里的内联脚本在页面加载前读同一个键，提前设好 --log-font-size，避免闪一下
const logKey = 'danlu.log.fontSize'
export const LOG_FONT_SIZES = [11, 12, 12.5, 13, 14, 15, 16]
export const LOG_FONT_DEFAULT = 12.5

export function storedLogFontSize() {
  const value = Number(read(logKey))
  return LOG_FONT_SIZES.includes(value) ? value : LOG_FONT_DEFAULT
}

/** 保存并立即应用到页面上所有日志和代码框 */
export function setLogFontSize(size: number) {
  const style = document.documentElement.style
  if (size === LOG_FONT_DEFAULT) {
    write(logKey, null)
    style.removeProperty('--log-font-size')
  } else {
    write(logKey, String(size))
    style.setProperty('--log-font-size', `${size}px`)
  }
}

// ---------- 单键快捷键 ----------

// N、/、J、K、? 不带修饰键，语音输入会把说出的字母当成按键（WCAG 2.1.4 要求能关掉）。
// 只关这几个：Ctrl/⌘ + Enter 提交和 Esc 不受影响。按键时现读，别的标签页改了也立即生效
const shortcutsKey = 'danlu.shortcuts'
const shortcutListeners = new Set<() => void>()

export function shortcutsEnabled() {
  return read(shortcutsKey) !== 'off'
}

export function setShortcutsEnabled(on: boolean) {
  write(shortcutsKey, on ? null : 'off')
  shortcutListeners.forEach((f) => f())
}

function subscribeShortcuts(onChange: () => void) {
  shortcutListeners.add(onChange)
  const onStorage = (e: StorageEvent) => (e.key === shortcutsKey || e.key === null) && onChange()
  window.addEventListener('storage', onStorage)
  return () => {
    shortcutListeners.delete(onChange)
    window.removeEventListener('storage', onStorage)
  }
}

/** 单键快捷键开没开；关掉时界面上也不显示 N、/ 这类提示 */
export function useShortcutsEnabled() {
  return useSyncExternalStore(subscribeShortcuts, shortcutsEnabled)
}
