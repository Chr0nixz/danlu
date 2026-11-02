import { AnsiUp } from 'ansi_up'

/** 模拟终端的回车：每行只保留最后一个 \r 之后的内容，tqdm 进度条就会原地刷新而不是刷屏 */
export function collapseCR(raw: string): string {
  return raw
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => {
      const i = line.lastIndexOf('\r')
      return i >= 0 ? line.slice(i + 1) : line
    })
    .join('\n')
}

function newAnsiUp() {
  const au = new AnsiUp()
  // 用 class 输出颜色，配色在 index.css 里按深色背景调过（默认的深蓝在黑底上看不清）
  au.use_classes = true
  return au
}

export function logToHtml(raw: string): string {
  return newAnsiUp().ansi_to_html(collapseCR(raw))
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
  private au = newAnsiUp()

  /**
   * reset 为真时调用方先清空已有内容；lines 是新增的完整行，追加在已有内容后面；
   * tail 是还没换行的最后一行，替换上一次的 tail。
   */
  update(raw: string): { reset: boolean; lines: string; tail: string } {
    const reset = !raw.startsWith(this.src)
    if (reset) {
      this.done = 0
      this.au = newAnsiUp()
    }
    this.src = raw
    const end = raw.lastIndexOf('\n') + 1
    let lines = ''
    if (end > this.done) {
      lines = this.au.ansi_to_html(collapseCR(raw.slice(this.done, end)))
      this.done = end
    }
    // 最后一行下次还会变：用当前颜色状态的副本转换，不改动真正的状态。
    // ansi_up 的状态都是实例上的普通字段（颜色、粗体等），浅拷贝就够了
    const rest = raw.slice(this.done)
    const tail = rest ? Object.assign(Object.create(AnsiUp.prototype) as AnsiUp, this.au).ansi_to_html(collapseCR(rest)) : ''
    return { reset, lines, tail }
  }
}
