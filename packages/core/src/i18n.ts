// 多语言运行时：locale 状态、目录注册、插值与复数、带键位类型的 t 工厂。
// 目录以 zh-CN 为源语言；en 的类型镜像 zh 的键集，缺键或多键在 typecheck 就会报错。
// 调用点用各应用 lib/i18n.ts 导出的 t（键 = 应用键 ∪ core 键）；core 内部用这里的 t。
// 切换语言时根组件以 key={locale} 重挂整棵树，t 是纯函数，不需要每个组件订阅。
import { useSyncExternalStore } from 'react'
import { zhCN as coreZh } from './locales/zh-CN.ts'
import { en as coreEn } from './locales/en.ts'

export type Locale = 'zh-CN' | 'en'
/** 一条文案：普通字符串，或按 Intl.PluralRules 选择的复数对象（中文只有 other） */
export type Message = string | { one?: string; other: string }
export type Catalog = Record<string, Message>
export type TParams = Record<string, string | number>

let locale: Locale = 'zh-CN'
const listeners = new Set<() => void>()
const catalogs: Record<Locale, Catalog> = { 'zh-CN': {}, en: {} }

/** 应用启动时注册自己的目录；core 域目录在本模块加载时已注册 */
export function registerMessages(l: Locale, c: Catalog): void {
  Object.assign(catalogs[l], c)
}

export function getLocale(): Locale {
  return locale
}

export function setLocale(l: Locale): void {
  if (l === locale) return
  locale = l
  for (const fn of listeners) fn()
}

export function subscribeLocale(fn: () => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

/** 根组件和语言切换器用它订阅；其余组件直接用 t，靠根重挂刷新 */
export function useLocale(): Locale {
  return useSyncExternalStore(subscribeLocale, getLocale, getLocale)
}

/**
 * stored 是用户显式选择（'auto'、空或非法值都表示跟随系统）。
 * sysLang 取不到时按中文（产品默认语言）。
 */
export function resolveLocale(stored: string | null | undefined, sysLang: string | null | undefined): Locale {
  if (stored === 'zh-CN' || stored === 'en') return stored
  if (!sysLang) return 'zh-CN'
  return sysLang.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

const pluralRules = new Map<Locale, Intl.PluralRules>()

function render(msg: Message, l: Locale, params?: TParams): string {
  // count 和 n 是同一个计数的两种写法：选复数和插值都认，模板里写哪个都行
  let p = params
  if (p && (p.count != null || p.n != null)) {
    const n = p.count ?? p.n
    p = { ...p, count: n, n }
  }
  let s: string
  if (typeof msg === 'string') {
    s = msg
  } else {
    let rules = pluralRules.get(l)
    if (!rules) {
      rules = new Intl.PluralRules(l)
      pluralRules.set(l, rules)
    }
    const n = Number(p?.count ?? 0)
    s = rules.select(n) === 'one' && msg.one != null ? msg.one : msg.other
  }
  if (!p) return s
  // 缺参数时保留占位符原样，便于发现漏传
  return s.replace(/\{(\w+)\}/g, (m, k: string) => (p[k] != null ? String(p[k]) : m))
}

/** 查当前语言目录；缺键回退中文，再缺返回键名本身（审计脚本负责抓漏网） */
export function translate(key: string, params?: TParams): string {
  const msg = catalogs[locale][key] ?? catalogs['zh-CN'][key]
  return msg == null ? key : render(msg, locale, params)
}

registerMessages('zh-CN', coreZh)
registerMessages('en', coreEn)

export type CoreKey = keyof typeof coreZh

/** 用应用的 zh 目录创建带键位类型的 t；拼错键会被 tsc 拦下。目录本身经 registerMessages 注册。 */
export function createT<Z extends Catalog>(_zh: Z) {
  return (key: keyof Z | CoreKey, params?: TParams): string => translate(key as string, params)
}

/** core 内部（format.ts 等）用的 t */
export const t = createT(coreZh)