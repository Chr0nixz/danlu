import { useQuery } from '@tanstack/react-query'
import { ArrowRight } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { GpuGrid, gpuBusy, shortName, useGpus } from '../components/GpuGrid'
import { HostMetrics } from '../components/HostMetrics'
import { JobList } from '../components/JobRow'
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
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-ui sm:gap-x-5">
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
          <span className="text-fail">失败</span>
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

export function Home() {
  const active = useActiveJobs()
  const recent = useQuery({ queryKey: ['jobs', 'recent'], queryFn: () => api.jobs({ status: 'succeeded,failed,cancelled', size: 8 }), refetchInterval: 5000 })

  const all = active.data?.jobs ?? []
  const running = all.filter((j) => j.status === 'running').sort((a, b) => a.id - b.id)
  const queued = all.filter((j) => j.status === 'queued').sort((a, b) => (a.queue_pos ?? 0) - (b.queue_pos ?? 0))
  const done: Job[] = recent.data?.jobs ?? []

  return (
    <div className="space-y-4 sm:space-y-5">
      <Summary running={running.length} queued={queued.length} />
      <GpuGrid />
      <HostMetrics />

      <ErrorBox error={active.error ?? recent.error} />
      <Card className="overflow-hidden [&>section+section]:border-t [&>section+section]:border-line">
        {active.isLoading ? (
          <SkeletonRows rows={3} />
        ) : (
          <>
            <Group icon={<StatusIcon status="running" decorative />} title="运行中" count={running.length}>
              <JobList jobs={running} empty={<GroupEmpty>炉子空着，没有正在运行的任务</GroupEmpty>} />
            </Group>
            <Group icon={<StatusIcon status="queued" decorative />} title="排队" count={queued.length}>
              <JobList jobs={queued} empty={<GroupEmpty>队列是空的</GroupEmpty>} />
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
          {recent.isLoading ? <SkeletonRows rows={2} /> : <JobList jobs={done} empty={<GroupEmpty>还没有出炉的任务</GroupEmpty>} />}
        </Group>
      </Card>
    </div>
  )
}
