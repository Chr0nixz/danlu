import { createT, registerMessages, resolveLocale, setLocale } from '@danlu/core'
import { zhCN } from '../locales/zh-CN'
import { en } from '../locales/en'

registerMessages('zh-CN', zhCN)
registerMessages('en', en)

/** 带键位类型的 t：键集 = 客户端键 ∪ core 键。组件里一律用它，不用 core 的裸 t。 */
export const t = createT(zhCN)

/**
 * 应用 Rust 偏好里的 lang（'auto' | 'zh-CN' | 'en'）。
 * 语言存在 state.json（Rust 发通知也要用），两个窗口经 prefs-changed 事件同步。
 * webview 的 navigator.language 就是系统语言，auto 时用它解析。
 */
export function applyPrefLang(lang: string | undefined): void {
  setLocale(resolveLocale(lang, navigator.language))
}

// 偏好还没取回来时先按系统语言渲染，取回后由 useLangSync 校正
applyPrefLang(undefined)