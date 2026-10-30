import type { Job, JobStatus } from './api'

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
