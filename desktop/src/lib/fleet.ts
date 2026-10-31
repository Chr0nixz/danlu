// 多台服务器的汇总。轮询在 Rust 层，这里只订阅它推来的 "fleet" 事件，再整理成界面要的样子。

import { type Job } from '@danlu/core'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { listen } from '@tauri-apps/api/event'
import { useEffect, useMemo, useState } from 'react'
import { getFleet, listServers, type FleetView, type ServerSnap } from './backend'

export function useServers() {
  return useQuery({ queryKey: ['servers'], queryFn: listServers, staleTime: Infinity })
}

const empty: FleetView = { servers: [], alerts: [], read: [], polled_at: 0 }

export function useFleetView(): FleetView {
  const [view, setView] = useState<FleetView>(empty)
  const qc = useQueryClient()
  useEffect(() => {
    let alive = true
    getFleet().then((v) => alive && v.polled_at && setView(v))
    const off = listen<FleetView>('fleet', (e) => {
      setView(e.payload)
      // 打开着的任务详情跟着刷新
      qc.invalidateQueries({ queryKey: ['job'] })
    })
    return () => {
      alive = false
      off.then((f) => f())
    }
  }, [qc])
  return view
}

export interface FleetJob {
  server: string
  job: Job
  /** 全局唯一的键，也是显示的编号：gpu1-42 */
  key: string
}

export const jobKey = (server: string, id: number) => `${server}-${id}`

export function fleetJobs(servers: ServerSnap[]): FleetJob[] {
  return servers.flatMap((s) => s.jobs.map((job) => ({ server: s.name, job, key: jobKey(s.name, job.id) })))
}

/** 失败而且没在右侧打开看过 */
export const isUnread = (f: FleetJob, read: Set<string>) => f.job.status === 'failed' && !read.has(f.key)

/** 总览的排序：没看过的失败 → 运行中 → 排队 → 已结束；同一类里按时间 */
export function sortJobs(list: FleetJob[], read: Set<string>): FleetJob[] {
  const rank = (f: FleetJob) => {
    if (isUnread(f, read)) return 0
    const s = f.job.status
    return s === 'running' ? 1 : s === 'queued' ? 2 : 3
  }
  const time = (f: FleetJob) => {
    const j = f.job
    if (j.status === 'running') return j.started_at ?? 0
    if (j.status === 'queued') return -(j.queue_pos ?? 0) // 排队：靠前的先
    return j.ended_at ?? 0
  }
  return [...list].sort((a, b) => rank(a) - rank(b) || time(b) - time(a) || a.key.localeCompare(b.key))
}

export function useReadSet(view: FleetView) {
  return useMemo(() => new Set(view.read), [view.read])
}

/** 服务器是否算离线：连续 30 秒连不上（和通知的口径一样），刚启动还没连上过的也算 */
export function isOffline(s: ServerSnap, now = Date.now()) {
  if (s.online) return false
  return !s.updated_at || (s.failing_since !== null && now - s.failing_since >= 30_000)
}
