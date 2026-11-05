import { useQuery } from '@tanstack/react-query'
import { ArrowRight } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { GpuGrid, gpuBusy, shortName, useGpus } from '../components/GpuGrid'
import { HostMetrics } from '../components/HostMetrics'
import { JobList } from '../components/JobRow'
import { QueueBanner, QueuePauseButton, QueuePauseError } from '../components/QueueControls'
import { StarError } from '../components/StarButton'
import { Card, cx, ErrorBox, SkeletonRows, StatusIcon } from '../components/ui'
import { now, type Job } from '@danlu/core'
import { api } from '../lib/api'
import { useActiveJobs, useRecentFailures } from '../lib/queries'

function Group({ icon, title, count, extra, children }: { icon: ReactNode; title: string; count?: number; extra?: ReactNode; children: ReactNode }) {
  return (
    <section>
      <div className="flex h-9 items-center gap-2 border-b border-line bg-subtle px-3 text-ui sm:px-4">
        {icon}
        <h2 className="font-medium">{title}</h2>
        {count !== undefined && <span className="text-fg-3">{count}</span>}
        {extra && <div className="ml-auto">{extra}</div>}
      </div>
      {children}
    </section>
  )
}

function GroupEmpty({ children }: { children: ReactNode }) {
  return <div className="px-4 py-3 text-ui text-fg-3 sm:pl-11">{children}</div>
}

function Summary({ running, queued }: { running: number; queued: number }) {
  const gpus = useGpus()
  const failed = useRecentFailures().data ?? 0
  const list = gpus.data?.gpus ?? []
  const free = list.filter((g) => !gpuBusy(g)).length
  const models = [...new Set(list.map((g) => shortName(g.name)))]
  const age = gpus.data ? now() - gpus.data.updated_at : 0
  const stale = gpus.data && age > 15

  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-ui sm:gap-x-5">
      <span className="inline-flex items-center gap-1.5">
        <StatusIcon status="running" decorative />
        <b className="font-semibold">{running}</b>
        <span className="text-fg-2">运行中</span>
      </span>
      <span className="inline-flex items-center gap-1.5">
        <StatusIcon status="queued" decorative />
        <b className="font-semibold">{queued}</b>
        <span className="text-fg-2">排队</span>
      </span>
      {failed > 0 && (
        // 手机上把点击区撑到 40px 高，不改变排版
        <Link to="/jobs?status=failed&recent=24h" className="inline-flex items-center gap-1.5 hover:underline max-sm:-my-2.5 max-sm:py-2.5" title="最近 24 小时内失败的任务">
          <StatusIcon status="failed" decorative />
          <b className="font-semibold text-fail">{failed}</b>
          {/* 手机上写短的 24h，和"失败"之间不留整个间距，摘要才放得下一行 */}
          <span className="text-fail">
            失败
            <span className="ml-1 text-[11px] text-fg-3 sm:hidden">24h</span>
          </span>
          <span className="text-xs text-fg-3 max-sm:hidden">24 小时内</span>
        </Link>
      )}
      {list.length > 0 && (
        <span className="inline-flex items-center gap-1.5">
          <span className={cx('mx-[5px] size-1.5 rounded-full', free ? 'bg-ok' : 'bg-fg-4')} />
          <b className="font-semibold">
            {free}
            <span className="font-normal text-fg-3"> / {list.length}</span>
          </b>
          <span className="text-fg-2">
            <span className="sm:hidden">空闲</span>
            <span className="max-sm:hidden">张卡空闲</span>
          </span>
          {models.length === 1 && <span className="hidden text-fg-3 sm:inline">· {models[0]}</span>}
        </span>
      )}
      {gpus.data && (
        <span className={cx('ml-auto text-xs', stale ? 'text-warn' : 'text-fg-3 max-sm:hidden')} title="GPU 数据的更新时间">
          {stale ? `GPU 数据 ${Math.round(age)} 秒没有更新` : '实时'}
        </span>
      )}
    </div>
  )
}

/**
 * 主机资源排在任务列表后面，手机和电脑都一样：首屏要回答的是任务跑完没有、跑到哪了、卡空不空，
 * CPU 和内存不在其中，放在 GPU 下面会把失败的任务挤出首屏。
 */
export function Home() {
  const active = useActiveJobs()
  const recent = useQuery({ queryKey: ['jobs', 'recent'], queryFn: () => api.jobs({ status: 'succeeded,failed,cancelled', size: 8 }), refetchInterval: 5000 })

  const all = active.data?.jobs ?? []
  const running = all.filter((j) => j.status === 'running').sort((a, b) => a.id - b.id)
  const silent = running.filter((j) => j.silent_since).length
  const queued = all.filter((j) => j.status === 'queued').sort((a, b) => (a.queue_pos ?? 0) - (b.queue_pos ?? 0))
  const done: Job[] = recent.data?.jobs ?? []

  return (
    <div className="space-y-4 sm:space-y-5">
      <h1 className="sr-only">总览</h1>
      <QueueBanner />
      <StarError />
      <Summary running={running.length} queued={queued.length} />
      <GpuGrid />

      <ErrorBox error={active.error ?? recent.error} />
      <Card className="overflow-hidden [&>section+section]:border-t [&>section+section]:border-line">
        {active.isLoading ? (
          <SkeletonRows rows={3} />
        ) : (
          <>
            <Group icon={<StatusIcon status="running" decorative />} title="运行中" count={running.length}
              extra={silent > 0 && <span className="text-xs text-warn">{silent} 个无新输出</span>}>
              <JobList jobs={running} showCopy empty={<GroupEmpty>炉子空着，没有正在运行的任务</GroupEmpty>} />
            </Group>
            <Group icon={<StatusIcon status="queued" decorative />} title="排队" count={queued.length} extra={<QueuePauseButton />}>
              <QueuePauseError />
              <JobList jobs={queued} showCopy empty={<GroupEmpty>队列是空的</GroupEmpty>} />
            </Group>
          </>
        )}
        <Group
          icon={<StatusIcon status="succeeded" className="text-fg-3" decorative />}
          title="最近完成"
          extra={
            <Link to="/jobs" className="-mr-2 inline-flex h-9 items-center gap-1 px-2 text-xs text-fg-3 hover:text-fg">
              全部任务
              <ArrowRight className="size-3.5" aria-hidden />
            </Link>
          }
        >
          {recent.isLoading ? <SkeletonRows rows={2} /> : <JobList jobs={done} showCopy empty={<GroupEmpty>还没有出炉的任务</GroupEmpty>} />}
        </Group>
      </Card>
      <HostMetrics />
    </div>
  )
}
