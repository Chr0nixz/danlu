// 只存在当前浏览器里的显示偏好：跟屏幕大小、视力有关，手机和电脑各设各的。
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
