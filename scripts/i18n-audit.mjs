// i18n 审计：扫描前端源码里残留的用户可见中文（注释自动排除），
// 以及绕过 apiErrorText 直接显示错误 .message 的地方（服务器的中文原文会漏进英文界面）。
// 用法：node scripts/i18n-audit.mjs [--quiet]
// 有残留时退出码 1（CI 门禁）。已知例外在 ALLOWLIST / MESSAGE_ALLOWLIST 里，新增例外必须写明原因。
//
// 实现是一个识别字符串/模板/注释的小状态机：剥掉注释后，代码里剩下的 CJK
// 就是文案（或数据/逻辑匹配）。不依赖 TS 编译器 API，node 直接跑。
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

const ROOTS = ['web/src', 'desktop/src', 'packages/core/src']
const SKIP_DIRS = new Set(['locales', 'node_modules'])
// 品牌名两种语言都保留；其余例外按文件精确放行
const BRAND = '丹炉'
const ALLOWLIST = [
  // 旧服务器回退：无 fail_kind 时按原文匹配/显示（docs/I18N.md 的边界）
  { file: 'web/src/pages/JobDetail.tsx', match: '任务不存在' },
  { file: 'web/src/pages/JobDetail.tsx', match: '退出码' },
  { file: 'packages/core/src/format.ts', match: '退出码' },
  // 开发者不变量错误（不是用户界面文案，同 Rust 的 expect）
  { file: 'desktop/src/lib/workspace.tsx', match: 'useWorkspace 必须在 WorkspaceCtx 里用' },
]

// \u9519\u8bef\u5bf9\u8c61\u7684 .message\uff08e.message\u3001err.message\u3001q.error.message\u3001(error as Error).message\uff09\uff1a
// \u663e\u793a\u9519\u8bef\u4e00\u5f8b\u7ecf apiErrorText\uff0c\u5b83\u6309\u9519\u8bef\u7801\u672c\u5730\u5316\uff0c\u8ba4\u4e0d\u51fa\u65f6\u624d\u56de\u9000\u539f\u6587\u3002
const ERROR_MESSAGE = /\b(?:e|err|error|\w*[Ee]rror)\)?\??\.message\b/
const MESSAGE_ALLOWLIST = [
  // apiErrorText \u81ea\u5df1\u7684\u56de\u9000
  { file: 'packages/core/src/client.ts', match: 'err.message' },
  // \u5224\u65ad\u662f\u4e0d\u662f\u9875\u9762\u6587\u4ef6\u52a0\u8f7d\u5931\u8d25\uff0c\u4e0d\u663e\u793a
  { file: 'web/src/components/ErrorBoundary.tsx', match: 'chunkError.test(e.message)' },
  // \u65e7\u670d\u52a1\u5668\u56de\u9000\uff1a\u6ca1\u6709\u9519\u8bef\u7801\u65f6\u6309\u539f\u6587\u5224\u65ad\u4efb\u52a1\u4e0d\u5b58\u5728\uff0c\u4e0d\u663e\u793a
  { file: 'web/src/pages/JobDetail.tsx', match: "q.error.message === '\u4efb\u52a1\u4e0d\u5b58\u5728'" },
]

const CJK = /[\u4e00-\u9fff\u3000-\u303f\uff00-\uffef]/

function* files(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) {
      if (!SKIP_DIRS.has(name)) yield* files(p)
    } else if (/\.(ts|tsx)$/.test(name)) {
      yield p
    }
  }
}

/** 剥掉行注释与块注释（识别单双引号字符串和模板串，避免误剥字符串里的斜杠） */
function stripComments(src) {
  let out = ''
  let i = 0
  const n = src.length
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (c === '/' && c2 === '/') {
      while (i < n && src[i] !== '\n') i++
      continue
    }
    if (c === '/' && c2 === '*') {
      i += 2
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++
      i += 2
      out += ' '
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      const quote = c
      out += c
      i++
      while (i < n) {
        if (src[i] === '\\') {
          out += src[i] + (src[i + 1] ?? '')
          i += 2
          continue
        }
        out += src[i]
        if (src[i] === quote) {
          i++
          break
        }
        i++
      }
      continue
    }
    out += c
    i++
  }
  return out
}

// 复数条目（en 目录里值是 {one, other} 对象的键）：按 count/n 选形式，调用点不传计数就永远是 other，
// 单卡机器上会写成"1 GPUs"。只认 locales 下的 en 目录（en.ts 或 en/ 子目录）。
function* localeFiles(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      if (name !== 'node_modules') yield* localeFiles(p)
    } else if (/[\\/]locales[\\/]en([\\/]|\.ts$)/.test(p) && p.endsWith('.ts')) {
      yield p
    }
  }
}
const pluralKeys = new Set()
for (const root of ROOTS) {
  for (const file of localeFiles(root)) {
    for (const m of readFileSync(file, 'utf8').matchAll(/^\s*'([\w.]+)':\s*\{/gm)) pluralKeys.add(m[1])
  }
}

/** t('key', …) 调用的参数部分（从键后到配平的右括号） */
function callArgs(src, from) {
  let depth = 1
  for (let i = from; i < src.length; i++) {
    if (src[i] === '(') depth++
    else if (src[i] === ')' && --depth === 0) return src.slice(from, i)
  }
  return src.slice(from)
}

const violations = []
for (const root of ROOTS) {
  for (const file of files(root)) {
    const stripped = stripComments(readFileSync(file, 'utf8'))
    for (const m of stripped.matchAll(/\bt\(\s*'([\w.]+)'/g)) {
      if (!pluralKeys.has(m[1])) continue
      if (/\b(?:n|count)\b\s*[:,}]/.test(callArgs(stripped, m.index + m[0].length))) continue
      const line = stripped.slice(0, m.index).split('\n').length
      violations.push(`${file}:${line}: 复数文案 ${m[1]} 要传 n 或 count，否则英文永远用复数形式`)
    }
    const lines = stripped.split('\n')
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i]
      if (ERROR_MESSAGE.test(line) && !MESSAGE_ALLOWLIST.some((a) => file === a.file && line.includes(a.match))) {
        violations.push(`${file}:${i + 1}: 错误要经 apiErrorText 显示，不要直接用 .message: ${line.trim().slice(0, 120)}`)
      }
      if (!CJK.test(line)) continue
      // 品牌名放行（去掉品牌后仍含 CJK 才算残留）
      const withoutBrand = line.split(BRAND).join('')
      if (!CJK.test(withoutBrand)) continue
      if (ALLOWLIST.some((a) => file === a.file && line.includes(a.match))) continue
      violations.push(`${file}:${i + 1}: ${line.trim().slice(0, 120)}`)
    }
  }
}

if (violations.length) {
  console.error(`i18n 审计：发现 ${violations.length} 处问题：`)
  for (const v of violations) console.error('  ' + v)
  console.error('文案抽进目录、错误经 apiErrorText 显示（docs/I18N.md）；确属数据/例外的加进 ALLOWLIST 并写明原因。')
  process.exit(1)
}
if (!process.argv.includes('--quiet')) console.log('i18n 审计通过：无残留用户可见中文，错误都经 apiErrorText 显示。')