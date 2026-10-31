import type { Job, JobStatus } from './types'

export function duration(sec: number): string {
  sec = Math.max(0, Math.floor(sec))
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  if (d > 0) return `${d}天${h}小时`
  if (h > 0) return `${h}小时${m}分`
  if (m > 0) return `${m}分${s}秒`
  return `${s}秒`
}

export const now = () => Date.now() / 1000

/** 运行中的任务算到现在，结束的任务算到结束时间 */
export function jobDuration(j: Job): number | null {
  if (!j.started_at) return null
  return (j.ended_at ?? now()) - j.started_at
}

export function fmtTime(ts: number | null | undefined): string {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export function timeAgo(ts: number | null | undefined): string {
  if (!ts) return '-'
  const diff = now() - ts
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  if (diff < 7 * 86400) return `${Math.floor(diff / 86400)} 天前`
  return fmtTime(ts).slice(0, 10)
}

export function mb(v: number): string {
  return v >= 1024 ? `${(v / 1024).toFixed(1)} GB` : `${v} MB`
}

export function bytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`
  return `${n} B`
}

export const statusLabel: Record<JobStatus, string> = {
  queued: '排队中',
  running: '运行中',
  succeeded: '成功',
  failed: '失败',
  cancelled: '已取消',
}

export function jobStatusText(j: Job): string {
  if (j.status === 'running' && j.cancel_requested) return '取消中'
  return statusLabel[j.status]
}

export const isActive = (j: Job) => j.status === 'queued' || j.status === 'running'

export function gpuText(j: Job): string {
  if (j.gpus_assigned.length) return `GPU ${j.gpus_assigned.join(',')}`
  if (j.gpus_pinned.length) return `指定 GPU ${j.gpus_pinned.join(',')}`
  return j.gpus_req ? `${j.gpus_req} 张卡` : '不用 GPU'
}

/**
 * tqdm 进度条里的方块字符在列表里会变成一大块深色，还会把后面的数字挤出去。
 * "84%|████▍     | 84/100 [loss=0.88]" → "84% · 84/100 [loss=0.88]"，百分比单独拿出来画细条。
 */
export function compactProgress(line: string): { text: string; short: string; pct: number | null } {
  const m = line.match(/(\d{1,3})%\s*\|[▀-▟#\s]*\|/)
  const text = line.replace(/\s*\|[▀-▟#\s]*\|\s*/g, ' · ').replace(/\s{2,}/g, ' ')
  // 宽度不够时：有百分比就去掉重复的 75/100，给 loss 之类的数字留位置
  const short = m ? text.replace(/ · \d+\/\d+\s*/, ' ') : text
  return { text, short, pct: m ? Math.min(100, Number(m[1])) : null }
}

/** 失败任务的原因：退出码加报错行（找不到报错行时用最后一行输出） */
export function failText(j: Job): string {
  const why = j.fail_reason || (j.exit_code !== null ? `退出码 ${j.exit_code}` : '失败')
  const line = j.error_line || (j.last_line && compactProgress(j.last_line).text)
  return line ? `${why} · ${line}` : why
}

/** 项目名就是工作目录的最后一级：~/code/vit → vit */
export function projectOf(workdir: string): string {
  const parts = workdir.replace(/\/+$/, '').split('/')
  return parts[parts.length - 1] || workdir
}
