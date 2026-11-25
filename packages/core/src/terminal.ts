import { AnsiUp } from 'ansi_up'

/**
 * 模拟终端的回车：每行只保留最后一个 \r 之后的内容，tqdm 进度条就会原地刷新而不是刷屏。
 * 行尾的 \r 先去掉：print(..., end="\r") 只是把光标移回行首，这一行在终端上还看得见，不能吃成空行。
 * 规则与 Go 的 logs.CollapseCR 相同
 */
export function collapseCR(raw: string): string {
  return raw
    .split('\n')
    .map((line) => {
      let end = line.length
      while (end > 0 && line.charCodeAt(end - 1) === 13) end--
      const i = line.lastIndexOf('\r', end - 1)
      return line.slice(i + 1, end)
    })
    .join('\n')
}

/**
 * 去掉永远收不完的 CSI 开头的 ESC。ansi_up 遇到 ESC [ 后面先是可打印 ASCII、再是非 ASCII 字符（截断的转义序列后面接中文、
 * 二进制输出）时判成"序列还没收完"，之后的内容全卡在它的缓冲里，日志框不再更新。
 * 合法的序列（ESC [ 31m 之类）和后面跟换行、冒号的非法序列 ansi_up 自己处理，这里不动；只有 ASCII 的半截序列可能是还没写完，也不动。
 * 去掉 ESC 和 ansi_up 丢弃非法序列的做法一样：只丢 ESC，其余照常显示
 */
const hopelessCSI = /\x1b(?=\[(?![\x3c-\x3f]?[\d;]*[\x20-\x2f]?[\x40-\x7e])[\x20-\x7e]*[^\x00-\x7e])/g
const sanitize = (s: string) => (s.includes('\x1b[') ? s.replace(hopelessCSI, '') : s)

/**
 * 日志里 OSC 8 超链接的画法。ansi_up 默认输出不带 target 的 <a>：点了会在当前页把面板导航到外站
 * （桌面客户端是整个无边框主窗口），日志内容来自训练脚本，不可信。
 * - tab：新标签页打开，不带来源，不给新页面 window.opener；
 * - text：只留文字。桌面客户端用它：webview 里的链接会在应用里打开外站页面。
 * 只认 http、https，其余协议（javascript: 之类）一律只留文字。
 */
export type LogLinks = 'tab' | 'text'

// 引号写成 \u 转义：i18n 审计的注释剥离不认正则字面量，正则里的引号会被当成字符串开头
const htmlEntities: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '\u0022': '&quot;', '\u0027': '&#x27;' }
const escapeHtml = (s: string) => s.replace(/[&<>\u0022\u0027]/g, (c) => htmlEntities[c])

function newAnsiUp(links: LogLinks) {
  const au = new AnsiUp()
  // 用 class 输出颜色，配色在 index.css 里按深色背景调过（默认的深蓝在黑底上看不清）
  au.use_classes = true
  // process_hyperlink 在类型里是 private，挂在实例上覆盖；LogRenderer 复制状态时它跟着一起复制
  ;(au as unknown as { process_hyperlink: (pkt: { url: string; text: string }) => string }).process_hyperlink = (pkt) => {
    const scheme = pkt.url.split(':', 1)[0].toLowerCase()
    if (links === 'text' || (scheme !== 'http' && scheme !== 'https')) return escapeHtml(pkt.text)
    return `<a href="${escapeHtml(pkt.url)}" target="_blank" rel="noopener noreferrer">${escapeHtml(pkt.text)}</a>`
  }
  return au
}

/** 整段日志转成 HTML（桌面客户端用）。链接默认只留文字，见 LogLinks */
export function logToHtml(raw: string, links: LogLinks = 'text'): string {
  return newAnsiUp(links).ansi_to_html(sanitize(collapseCR(raw)))
}

/**
 * 增量地把日志转成 HTML。已经换过行的部分只转换一次，之后每次只重画最后一行（tqdm 用 \r 原地刷新的就是这一行），
 * 实时日志每秒更新几次，不用每次把几百 KB 重新转换、重新排版。
 * 日志只在末尾变长时走增量；开头变了（往前加载、超长后截掉前面）就从头重建。
 */
export class LogRenderer {
  private src = ''
  /** src 里已经换行、转成 HTML 的长度 */
  private done = 0
  private au: AnsiUp
  private links: LogLinks

  /** links 见 LogLinks；面板默认新标签页打开 */
  constructor(links: LogLinks = 'tab') {
    this.links = links
    this.au = newAnsiUp(links)
  }

  /**
   * reset 为真时调用方先清空已有内容；lines 是新增的完整行，追加在已有内容后面；
   * tail 是还没换行的最后一行，替换上一次的 tail。
   */
  update(raw: string): { reset: boolean; lines: string; tail: string } {
    const reset = !raw.startsWith(this.src)
    if (reset) {
      this.done = 0
      this.au = newAnsiUp(this.links)
    }
    this.src = raw
    const end = raw.lastIndexOf('\n') + 1
    let lines = ''
    if (end > this.done) {
      lines = this.au.ansi_to_html(sanitize(collapseCR(raw.slice(this.done, end))))
      this.done = end
    }
    // 最后一行下次还会变：用当前颜色状态的副本转换，不改动真正的状态。
    // ansi_up 的状态都是实例上的普通字段（颜色、粗体等），浅拷贝就够了
    const rest = raw.slice(this.done)
    const tail = rest ? Object.assign(Object.create(AnsiUp.prototype) as AnsiUp, this.au).ansi_to_html(sanitize(collapseCR(rest))) : ''
    return { reset, lines, tail }
  }
}
