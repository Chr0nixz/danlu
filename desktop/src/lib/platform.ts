// 平台差异集中在这里：快捷键写法、窗口按钮。macOS 以后支持，现在先留好接缝。

export const isMac = navigator.userAgent.includes('Mac')

/** 显示用的修饰键名 */
export const MOD = isMac ? '⌘' : 'Ctrl'
export const ALT = isMac ? '⌥' : 'Alt'

/** 事件里的"主修饰键"：Windows 上是 Ctrl，macOS 上是 ⌘ */
export const modKey = (e: KeyboardEvent | React.KeyboardEvent) => (isMac ? e.metaKey : e.ctrlKey)

/** "Ctrl+Alt+D" 这样的快捷键写法换成当前平台的显示方式 */
export function shortcutLabel(s: string) {
  return s
    .split('+')
    .map((k) => ({ Ctrl: MOD, Cmd: '⌘', CmdOrCtrl: MOD, Alt: ALT, Option: '⌥', Shift: isMac ? '⇧' : 'Shift' })[k] ?? k)
    .join(isMac ? '' : '+')
}

/** 输入框、文本框里按键不当作全局快捷键 */
export function typingIn(e: KeyboardEvent) {
  const t = e.target as HTMLElement | null
  return !!t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)
}
