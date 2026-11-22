import { t } from './i18n.ts'
import type { Job, JobStatus, QueueState } from './types'

export function duration(sec: number): string {
  sec = Math.max(0, Math.floor(sec))
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  if (d > 0) return t('time.duration_dh', { d, h })
  if (h > 0) return t('time.duration_hm', { h, m })
  if (m > 0) return t('time.duration_ms', { m, s })
  return t('time.duration_s', { s })
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
  if (diff < 60) return t('time.just_now')
  if (diff < 3600) return t('time.minutes_ago', { n: Math.floor(diff / 60) })
  if (diff < 86400) return t('time.hours_ago', { n: Math.floor(diff / 3600) })
  if (diff < 7 * 86400) return t('time.days_ago', { n: Math.floor(diff / 86400) })
  return fmtTime(ts).slice(0, 10)
}

export function mb(v: number): string {
  return v >= 1024 ? `${(v / 1024).toFixed(1)} GB` : `${v} MB`
}

export function bytes(n: number): string {
  // 数据盘、网卡累计流量常到几十 TB，写成 13969.8 GB 读不出量级
  if (n >= 2 ** 40) return `${(n / 2 ** 40).toFixed(1)} TB`
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`
  return `${n} B`
}

const statusKeys = {
  queued: 'status.queued',
  running: 'status.running',
  succeeded: 'status.succeeded',
  failed: 'status.failed',
  cancelled: 'status.cancelled',
} as const

/** 是函数不是常量表：文案随当前语言取值，切换语言后重渲染即生效 */
export function statusLabel(s: JobStatus): string {
  return t(statusKeys[s])
}

export function jobStatusText(j: Job): string {
  if (j.status === 'running' && j.cancel_requested) return t('status.cancelling')
  return statusLabel(j.status)
}

export const isActive = (j: Job) => j.status === 'queued' || j.status === 'running'

export function gpuText(j: Job): string {
  if (j.gpus_assigned.length) return `GPU ${j.gpus_assigned.join(',')}`
  if (j.gpus_pinned.length) return t('gpu.pinned', { list: j.gpus_pinned.join(',') })
  return j.gpus_req ? t('gpu.count', { n: j.gpus_req }) : t('gpu.none')
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

/** 读取 tqdm 方括号里的剩余时间；未知值或非标准格式不展示 */
export function progressRemaining(line: string): string | null {
  const matches = [...line.matchAll(/\d{1,3}%\s*\|[^|]*\|[^[]*\[[^\]]*?<\s*([^,\]]+?)\s*,/g)]
  if (matches.length === 0) return null

  let value = matches[matches.length - 1][1].trim()
  if (!value || value === '?') return null

  let days = ''
  const dayMatch = value.match(/^(\d+)d\s+(.+)$/)
  if (dayMatch) {
    days = dayMatch[1]
    value = dayMatch[2]
  }

  const parts = value.split(':')
  if ((parts.length !== 2 && parts.length !== 3) || parts.some((part) => !/^\d+$/.test(part))) return null

  const padded = parts.slice(1).map((part) => part.padStart(2, '0'))
  const normalized = parts.length === 2
    ? `${Number(parts[0])}:${padded[0]}`
    : `${Number(parts[0])}:${padded[0]}:${padded[1]}`
  return days ? t('time.days_time', { d: Number(days), t: normalized }) : normalized
}

/**
 * 剩余时间已经单独显示时，从 tqdm 的方括号里去掉"已用<剩余"和速率，后面的 loss 等指标留着：
 * "[02:02<03:51, 1.13it/s, loss=0.3940]" → "[loss=0.3940]"；方括号里只有时间和速率时整个去掉。
 */
export function stripProgressTiming(text: string): string {
  return text
    .replace(
      /\s*\[\s*(?:\d+d\s+)?[\d:]+<[^,\]]*(?:,\s*[^,\]]*\/[^,\]]*)?(?:,\s*([^\]]*))?\]/g,
      (_, rest?: string) => (rest?.trim() ? ` [${rest.trim()}]` : ''),
    )
    .replace(/\s{2,}/g, ' ')
    .trim()
}

/** 失败任务的原因：退出码加报错行（找不到报错行时用最后一行输出） */
export function failText(j: Job): string {
  const why = failReasonText(j)
  const line = j.error_line || (j.last_line && compactProgress(j.last_line).text)
  return line ? `${why} · ${line}` : why
}

const signalNames: Record<number, string> = {
  2: 'SIGINT', 3: 'SIGQUIT', 4: 'SIGILL', 6: 'SIGABRT', 7: 'SIGBUS', 8: 'SIGFPE',
  9: 'SIGKILL', 11: 'SIGSEGV', 13: 'SIGPIPE', 14: 'SIGALRM', 15: 'SIGTERM',
}

/**
 * 失败原因文字：有机器码（fail_kind/fail_detail）就按当前语言合成，
 * 旧记录没有分类时回退服务器持久化的 fail_reason 原文。
 * start 类的详情是自由文本（诊断信息），译前缀后附原文。
 */
export function failReasonText(j: Job): string {
  switch (j.fail_kind) {
    case 'cuda_oom':
      return j.ext_seen ? t('fail.cuda_oom_ext') : t('fail.cuda_oom')
    case 'host_oom':
      switch (j.fail_detail) {
        case 'worker': return t('fail.host_oom_worker')
        case 'guess137': return t('fail.host_oom_guess')
        case 'confirmed': return t('fail.host_oom')
        default: return j.fail_reason || t('fail.host_oom')
      }
    case 'nccl': return t('fail.nccl')
    case 'exception': return t('fail.exception')
    case 'reboot': return t('fail.reboot')
    case 'killed': return t('fail.killed')
    case 'signal': {
      const n = j.exit_code !== null && j.exit_code > 128 ? j.exit_code - 128 : null
      const sig = j.fail_detail || (n !== null ? signalNames[n] ?? t('fail.signal_num', { n }) : '')
      return sig ? t('fail.signal', { sig }) : j.fail_reason || t('fail.signal_generic')
    }
    case 'exit':
      return j.exit_code !== null ? t('fail.exit_code', { code: j.exit_code }) : j.fail_reason || t('fail.failed')
    case 'start':
      // fail_detail 是不带前缀的原始启动错误（诊断原文，不翻译）；旧服务器没有它时回退 fail_reason 原文
      if (j.fail_detail) return t('fail.start_detail', { detail: j.fail_detail })
      return j.fail_reason || t('fail.start')
    default:
      return j.fail_reason || (j.exit_code !== null ? t('fail.exit_code', { code: j.exit_code }) : t('fail.failed'))
  }
}

/** 失败是否"只有退出码"（没有别的原因文字）：优先按机器码，旧服务器回退原文匹配 */
export function exitCodeOnly(j: Job): boolean {
  if (j.fail_kind) return j.fail_kind === 'exit'
  return !j.fail_reason || /^退出码 -?\d+$/.test(j.fail_reason)
}

/**
 * 队列暂停原因：按 kind 合成（本地化）；旧服务器没有结构化字段时回退 reason 原文。
 * 磁盘暂停的具体余量数字在 disk_available_gb/disk_min_gb 里，没有就用通用文案。
 */
export function queuePauseText(q: QueueState): string {
  if (!q.paused) return ''
  switch (q.kind) {
    case 'disk':
      if (q.disk_available_gb != null && q.disk_min_gb != null) {
        return t('queue.paused_disk', { available: q.disk_available_gb, min: q.disk_min_gb })
      }
      return q.reason || t('queue.paused_disk_generic')
    case 'fail_fast':
      if (q.job_id != null && q.fail_seconds != null) return t('queue.paused_fail_fast', { id: q.job_id, seconds: q.fail_seconds })
      if (q.job_id != null) return t('queue.paused_fail_fast_plain', { id: q.job_id })
      return q.reason || ''
    case 'manual':
      return t('queue.paused_manual')
    default:
      return q.reason || ''
  }
}

/**
 * 和代码无关的失败分类：服务器重启、被外部终止、收到信号、多卡通信出错。
 * 这类失败默认用原任务的快照原样重跑，界面上的重跑按钮也写"原样重跑"。
 */
export const sameCodeFailKinds = ['reboot', 'killed', 'signal', 'nccl']

/** 项目名就是工作目录的最后一级：~/code/vit → vit */
export function projectOf(workdir: string): string {
  const parts = workdir.replace(/\/+$/, '').split('/')
  return parts[parts.length - 1] || workdir
}
