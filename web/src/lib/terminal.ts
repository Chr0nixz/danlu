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

export function logToHtml(raw: string): string {
  const au = new AnsiUp()
  // 用 class 输出颜色，配色在 index.css 里按深色背景调过（默认的深蓝在黑底上看不清）
  au.use_classes = true
  return au.ansi_to_html(collapseCR(raw))
}
