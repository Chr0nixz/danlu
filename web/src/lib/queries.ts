import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { now, type Job } from '@danlu/core'

/** "最近失败"的时间窗口：24 小时 */
export const RECENT_SEC = 86400

/** 首页每组最多列出的任务数（也是接口一页的上限） */
export const ACTIVE_PAGE = 200

/**
 * 运行中和排队的任务。首页和标签页标题共用同一份缓存。
 * 两组分开查、都按 id 从旧到新：排队任务就是队列顺序，多于一页时截掉的是队尾；合在一起按 id 倒序取一页的话，
 * 截掉的正是最早的运行中任务和队首。计数用各自的 total，不受一页的上限影响。
 * 两次请求之间刚好有任务开始运行时，它可能在两组里各出现一次，留在运行中那组
 */
export function useActiveJobs() {
  return useQuery({
    queryKey: ['jobs', 'active'],
    queryFn: async () => {
      const [running, queued] = await Promise.all([
        api.jobs({ status: 'running', order: 'asc', size: ACTIVE_PAGE }),
        api.jobs({ status: 'queued', order: 'asc', size: ACTIVE_PAGE }),
      ])
      const ids = new Set(running.jobs.map((j) => j.id))
      return { running: running.jobs, runningTotal: running.total, queued: queued.jobs.filter((j) => !ids.has(j.id)), queuedTotal: queued.total }
    },
    refetchInterval: 3000,
  })
}

/**
 * 最近 24 小时内失败的任务数，用来回答"有没有挂"。和总览摘要链接到的任务列表用同一个口径。
 * 按结束时间倒序取一条，latest 就是最近挂掉的那个：手机首屏放不下最近完成的列表时，摘要下面直接点名它
 */
export function useRecentFailures() {
  return useQuery({
    queryKey: ['jobs', 'failed-24h'],
    queryFn: () => api.jobs({ status: 'failed', ended_after: Math.floor(now() - RECENT_SEC), order: 'ended', size: 1 }),
    refetchInterval: 10_000,
    select: (r) => ({ total: r.total, latest: r.jobs[0] as Job | undefined }),
  })
}

/** 队列状态，暂停或恢复后其他页面也立即看到 */
export function useQueueState() {
  // staleTime：首页的横幅和"暂停队列"按钮先后挂载，不设的话后挂载的那个会再请求一次
  return useQuery({ queryKey: ['queue'], queryFn: api.queue, refetchInterval: 3000, staleTime: 2000 })
}

/** 没有结束的 GPU 预约。首页列表和预约按钮共用一份缓存 */
export function useReservations() {
  return useQuery({ queryKey: ['reservations'], queryFn: api.reservations, refetchInterval: 3000, staleTime: 2000 })
}
