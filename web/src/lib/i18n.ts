import { createT, registerMessages, resolveLocale, setLocale, type Locale } from '@danlu/core'
import { zhCN } from '../locales/zh-CN'
import { en } from '../locales/en'

registerMessages('zh-CN', zhCN)
registerMessages('en', en)

/** 带键位类型的 t：键集 = 面板键 ∪ core 键。组件里一律用它，不用 core 的裸 t。 */
export const t = createT(zhCN)

// 语言是显示偏好，只存当前浏览器（和主题同一约定）；手机和电脑各设各的。
const KEY = 'danlu-lang'
export type LangPref = 'auto' | Locale

export function storedLang(): LangPref {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'zh-CN' || v === 'en') return v
  } catch {
    // 隐私模式等情况下 localStorage 不可用，按跟随系统处理
  }
  return 'auto'
}

/** 按保存的选择和浏览器语言解析并立即应用 */
export function applyLocale(): void {
  setLocale(resolveLocale(storedLang(), navigator.language))
}

/** 保存选择并立即生效；index.html 的内联脚本会在下次加载时提前应用，避免闪烁 */
export function setLang(pref: LangPref): void {
  try {
    if (pref === 'auto') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, pref)
  } catch {
    // 同上
  }
  applyLocale()
}

applyLocale()