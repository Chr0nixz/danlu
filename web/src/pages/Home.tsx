import { useQuery } from '@tanstack/react-query'
import { ArrowRight, ChevronRight } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { GpuGrid, gpuBusy, shortName, useGpus } from '../components/GpuGrid'
import { HostMetrics } from '../components/HostMetrics'
import { JobList } from '../components/JobRow'
import { QueueBanner, QueuePauseButton, QueuePauseError } from '../components/QueueControls'
import { QueueOpError } from '../components/QueueOps'
import { ReservationList, ReserveButton } from '../components/Reservations'
import { StarError } from '../components/StarButton'
import { Card, cx, ErrorBox, SkeletonRows, StatusIcon } from '../components/ui'
import { failReasonText, timeAgoShort, useNow, type Job } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
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

/** 一组多于一页时，列表末尾说明还有多少没列出，链到任务列表 */
function MoreJobs({ status, shown, total }: { status: 'running' | 'queued'; shown: number; total: number }) {
  if (shown <= 0 || total <= shown) return null
  return (
    <Link to={`/jobs?status=${status}`} className="flex h-9 items-center gap-1 border-t border-line px-4 text-ui text-fg-3 hover:text-fg max-sm:h-11 sm:pl-11">
      {t('home.more_jobs', { n: total - shown })}
      <ArrowRight className="size-3.5" aria-hidden />
    </Link>
  )
}

function Summary({ running, queued }: { running: number; queued: number }) {
  const gpus = useGpus()
  const recentFailures = useRecentFailures().data
  const failed = recentFailures?.total ?? 0
  const list = gpus.data?.gpus ?? []
  const free = list.filter((g) => !gpuBusy(g)).length
  const models = [...new Set(list.map((g) => shortName(g.name)))]
  // 每秒重画：GPU 数据停了以后轮询结果不再变，不靠定时器的话"数据 N 秒没有更新"永远出不来
  const at = useNow()
  const age = gpus.data ? at - gpus.data.updated_at : 0
  const stale = gpus.data && age > 15

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-ui sm:gap-x-5">
        <span className="inline-flex items-center gap-1.5">
          <StatusIcon status="running" decorative />
          <b className="font-semibold">{running}</b>
          <span className="text-fg-2">{t('status.running')}</span>
        </span>
        <span className="inline-flex items-center gap-1.5">
          <StatusIcon status="queued" decorative />
          <b className="font-semibold">{queued}</b>
          <span className="text-fg-2">{t('home.queued')}</span>
        </span>
        {failed > 0 && (
          // 手机上把点击区撑到 44px 高，不改变排版
          <Link to="/jobs?status=failed&recent=24h" className="inline-flex items-center gap-1.5 hover:underline max-sm:-my-3 max-sm:py-3" title={t('home.failed_title')}>
            <StatusIcon status="failed" decorative />
            <b className="font-semibold text-fail">{failed}</b>
            {/* 手机上写短的 24h，和"失败"之间不留整个间距，摘要才放得下一行 */}
            <span className="text-fail">
              {t('status.failed')}
              <span className="ml-1 text-[11px] text-fg-3 sm:hidden">24h</span>
            </span>
            <span className="text-xs text-fg-3 max-sm:hidden">{t('home.within_24h')}</span>
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
              <span className="sm:hidden">{t('home.free_short')}</span>
              <span className="max-sm:hidden">{t('home.free_cards')}</span>
            </span>
            {models.length === 1 && <span className="hidden text-fg-3 sm:inline">· {models[0]}</span>}
          </span>
        )}
        {gpus.data && (
          <span className="ml-auto inline-flex items-center gap-3">
            <span className={cx('text-xs', stale ? 'text-warn' : 'text-fg-3 max-sm:hidden')} title={t('home.gpu_updated_title')}>
              {stale ? t('home.gpu_stale', { n: Math.round(age) }) : t('home.live')}
            </span>
            {/* 按钮比摘要的文字行高，用负外边距收回去，摘要行不因它变高。
                手机上四个计数已经占满一行，放不下它：挪到顶栏右侧（Layout 里的 PhoneReserve） */}
            {list.length > 0 && <ReserveButton className="-mr-2 -my-1.5 max-sm:hidden" />}
          </span>
        )}
      </div>
      {recentFailures?.latest && (
        <div className="mt-2.5 sm:hidden">
          <LatestFailure j={recentFailures.latest} />
        </div>
      )}
    </div>
  )
}

/**
 * 手机上摘要下面一行：最近挂掉的是哪个、为什么、多久前，点进去就是它。
 * 有两三个任务在跑和排队时，最近完成的列表已经在首屏以下，只看"9 失败"回答不了"挂的是哪个"。
 * 电脑上最近完成的列表就在首屏，不重复。
 */
function LatestFailure({ j }: { j: Job }) {
  useNow() // "N 分钟前"每秒跟着走
  return (
    // 手机上把点击区撑到 44px 高，不改变排版
    <Link to={`/jobs/${j.id}`} className="-my-3 flex min-w-0 items-center gap-1.5 py-3 text-ui">
      <StatusIcon status="failed" className="size-3.5" decorative />
      {/* 失败图标和失败红的原因已经说明这是什么；"最近失败"只给读屏，宽度留给名字和原因 */}
      <span className="sr-only">{t('home.latest_failure')}</span>
      <span className="shrink-0 text-fg-3">#{j.id}</span>
      <span className="min-w-0 truncate font-medium">{j.name}</span>
      <span className="min-w-0 max-w-[40%] shrink-[2] truncate text-fail">{failReasonText(j)}</span>
      <span className="ml-auto shrink-0 text-xs text-fg-3">{timeAgoShort(j.ended_at)}</span>
      <ChevronRight className="-mr-1 size-4 shrink-0 text-fg-3" aria-hidden />
    </Link>
  )
}

/**
 * 主机资源排在任务列表后面，手机和电脑都一样：首屏要回答的是任务跑完没有、跑到哪了、卡空不空，
 * CPU 和内存不在其中，放在 GPU 下面会把失败的任务挤出首屏。
 */
export function Home() {
  const active = useActiveJobs()
  const recent = useQuery({ queryKey: ['jobs', 'recent'], queryFn: () => api.jobs({ status: 'succeeded,failed,cancelled', size: 8 }), refetchInterval: 5000 })

  const running = active.data?.running ?? []
  const silent = running.filter((j) => j.silent_since).length
  const queued = active.data?.queued ?? []
  const runningTotal = active.data?.runningTotal ?? 0
  const queuedTotal = active.data?.queuedTotal ?? 0
  const done: Job[] = recent.data?.jobs ?? []

  return (
    <div className="space-y-4 sm:space-y-5">
      <h1 className="sr-only">{t('home.title')}</h1>
      <QueueBanner />
      <StarError />
      <QueueOpError />
      <Summary running={runningTotal} queued={queuedTotal} />
      <GpuGrid />
      <ReservationList />

      <ErrorBox error={active.error ?? recent.error} />
      <Card className="overflow-hidden [&>section+section]:border-t [&>section+section]:border-line">
        {active.isLoading ? (
          <SkeletonRows rows={3} />
        ) : (
          <>
            <Group icon={<StatusIcon status="running" decorative />} title={t('status.running')} count={runningTotal}
              extra={silent > 0 && <span className="text-xs text-warn">{t('home.silent_count', { n: silent })}</span>}>
              <JobList jobs={running} showCopy empty={<GroupEmpty>{t('home.running_empty')}</GroupEmpty>} />
              <MoreJobs status="running" shown={running.length} total={runningTotal} />
            </Group>
            <Group icon={<StatusIcon status="queued" decorative />} title={t('home.queued')} count={queuedTotal} extra={<QueuePauseButton />}>
              <QueuePauseError />
              <JobList jobs={queued} showCopy empty={<GroupEmpty>{t('home.queued_empty')}</GroupEmpty>} />
              <MoreJobs status="queued" shown={queued.length} total={queuedTotal} />
            </Group>
          </>
        )}
        <Group
          icon={<StatusIcon status="succeeded" className="text-fg-3" decorative />}
          title={t('home.recent_title')}
          extra={
            <Link to="/jobs" className="relative z-10 -mr-2 inline-flex h-9 items-center gap-1 px-2 text-xs text-fg-3 hover:text-fg max-sm:-my-1 max-sm:h-11">
              {t('home.all_jobs')}
              <ArrowRight className="size-3.5" aria-hidden />
            </Link>
          }
        >
          {recent.isLoading ? <SkeletonRows rows={2} /> : <JobList jobs={done} showCopy empty={<GroupEmpty>{t('home.recent_empty')}</GroupEmpty>} />}
        </Group>
      </Card>
      <HostMetrics />
    </div>
  )
}
