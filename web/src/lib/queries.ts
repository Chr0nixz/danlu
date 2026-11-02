import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { now } from '@danlu/core'

/** "最近失败"的时间窗口：24 小时 */
export const RECENT_SEC = 86400

/** 运行中和排队的任务。首页和标签页标题共用同一份缓存 */
export function useActiveJobs() {
  return useQuery({ queryKey: ['jobs', 'active'], queryFn: () => api.jobs({ status: 'running,queued', size: 200 }), refetchInterval: 3000 })
}

/** 最近 24 小时内失败的任务数，用来回答"有没有挂"。和总览摘要链接到的任务列表用同一个口径 */
export function useRecentFailures() {
  return useQuery({
    queryKey: ['jobs', 'failed-24h'],
    queryFn: () => api.jobs({ status: 'failed', ended_after: Math.floor(now() - RECENT_SEC), size: 1 }),
    refetchInterval: 10_000,
    select: (r) => r.total,
  })
}

/** 队列状态，暂停或恢复后其他页面也立即看到 */
export function useQueueState() {
  return useQuery({ queryKey: ['queue'], queryFn: api.queue, refetchInterval: 3000 })
}
