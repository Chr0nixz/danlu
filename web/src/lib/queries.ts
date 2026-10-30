import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { now } from './format'

/** 运行中和排队的任务。首页和标签页标题共用同一份缓存 */
export function useActiveJobs() {
  return useQuery({ queryKey: ['jobs', 'active'], queryFn: () => api.jobs({ status: 'running,queued', size: 200 }), refetchInterval: 3000 })
}

/** 最近 24 小时内失败的任务数，用来回答"有没有挂" */
export function useRecentFailures() {
  return useQuery({
    queryKey: ['jobs', 'failed-24h'],
    queryFn: () => api.jobs({ status: 'failed', size: 50 }),
    refetchInterval: 10_000,
    select: (r) => r.jobs.filter((j) => (j.ended_at ?? 0) > now() - 86400).length,
  })
}
