import { projectOf, type JobStatus } from '@danlu/core'
import { t } from '../lib/i18n'
import { AlertTriangle, Plus, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { FilterMenu } from '../components/Filters'
import { freeCount, GpuStrip } from '../components/GpuStrip'
import { JobList } from '../components/JobList'
import { Button, Empty, Kbd } from '../components/ui'
import { markRead, type Alert, type FleetView } from '../lib/backend'
import { fleetJobs, sortJobs, useReadSet, type FleetJob } from '../lib/fleet'
import { useListKeys } from '../lib/keys'
import { useWorkspace } from '../lib/workspace'

type StatusFilter = 'unread' | JobStatus

/** 搜索框里的词：编号、名称、命令、目录都算 */
export function matches(f: FleetJob, q: string) {
  if (!q) return true
  const s = q.toLowerCase()
  const j = f.job
  return [f.key, j.name, j.cmd, j.workdir].some((x) => x.toLowerCase().includes(s))
}

/** 总览：上面是所有服务器的空卡条，下面是所有服务器的任务（运行中、排队、24 小时内结束的） */
export function Overview({ view, onServer }: { view: FleetView; onServer: (name: string) => void }) {
  const { pane, openJob, openSubmit, search } = useWorkspace()
  const read = useReadSet(view)
  const [server, setServer] = useState<string | null>(null)
  const [project, setProject] = useState<string | null>(null)
  const [status, setStatus] = useState<StatusFilter | null>(null)

  const all = useMemo(() => fleetJobs(view.servers), [view.servers])
  const list = useMemo(
    () =>
      sortJobs(
        all.filter(
          (f) =>
            (!server || f.server === server) &&
            (!project || projectOf(f.job.workdir) === project) &&
            (!status || (status === 'unread' ? f.job.status === 'failed' && !read.has(f.key) : f.job.status === status)) &&
            matches(f, search),
        ),
        read,
      ),
    [all, server, project, status, search, read],
  )
  const selected = pane?.kind === 'job' ? `${pane.server}-${pane.id}` : null
  const cursor = useListKeys(list, selected, (f) => openJob(f.server, f.job.id))

  const projects = useMemo(() => {
    const m = new Map<string, number>()
    for (const f of all) m.set(projectOf(f.job.workdir), (m.get(projectOf(f.job.workdir)) ?? 0) + 1)
    return [...m.entries()].sort((a, b) => b[1] - a[1]).map(([p, n]) => ({ value: p, label: p, count: n }))
  }, [all])
  const count = (s: JobStatus) => all.filter((f) => f.job.status === s).length
  const unread = all.filter((f) => f.job.status === 'failed' && !read.has(f.key)).length
  const free = view.servers.reduce((n, s) => n + freeCount(s.gpus ?? []), 0)

  if (view.servers.length === 0) return null

  return (
    <section className="flex h-full min-w-0 flex-1 flex-col overflow-hidden rounded-[10px] border border-line bg-panel shadow-panel">
      <div className="shrink-0 border-b border-line p-2.5">
        <GpuStrip servers={view.servers} onServer={onServer} />
      </div>
      {view.alerts.length > 0 && <Alerts alerts={view.alerts} />}
      <div className="flex shrink-0 items-center gap-1 px-2.5 pt-2.5 pb-1.5">
        {view.servers.length > 1 && (
          <FilterMenu label={t('overview.filter_server')} value={server} onChange={setServer} options={view.servers.map((s) => ({ value: s.name, label: s.name }))} />
        )}
        <FilterMenu label={t('overview.filter_project')} value={project} onChange={setProject} options={projects} />
        <FilterMenu
          label={t('overview.filter_status')}
          value={status}
          onChange={setStatus}
          options={[
            { value: 'unread' as const, label: t('overview.filter_unread'), count: unread },
            { value: 'running' as const, label: t('status.running'), count: count('running') },
            { value: 'queued' as const, label: t('overview.filter_queued'), count: count('queued') },
            { value: 'failed' as const, label: t('status.failed'), count: count('failed') },
            { value: 'succeeded' as const, label: t('status.succeeded'), count: count('succeeded') },
            { value: 'cancelled' as const, label: t('status.cancelled'), count: count('cancelled') },
          ]}
        />
        <span className="num ml-auto pr-2 text-[11.5px] text-fg-3">
          {t('overview.summary', { running: count('running'), queued: count('queued'), free })}
        </span>
        <Button variant="primary" onClick={() => openSubmit()}>
          <Plus />
          {t('overview.new_job')}
          <Kbd className="border-on-primary/30 text-on-primary/70">N</Kbd>
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-1.5">
        {list.length === 0 ? (
          <Empty title={all.length === 0 ? t('overview.empty_title') : t('overview.empty_filter_title')}>
            {all.length === 0 ? <p>{t('overview.empty_body')}</p> : <p>{t('overview.empty_filter_body')}</p>}
          </Empty>
        ) : (
          <JobList jobs={list} selected={selected} cursor={cursor} read={read} onSelect={(f) => openJob(f.server, f.job.id)} />
        )}
      </div>
    </section>
  )
}

/** 服务器异常：连不上、nvidia-smi 报错、外部进程影响到你的任务。后两种可以确认后收起 */
function Alerts({ alerts }: { alerts: Alert[] }) {
  return (
    <div className="shrink-0 space-y-px border-b border-line px-2.5 py-1.5">
      {alerts.map((a) => (
        <div key={a.key} className="flex h-7 items-center gap-2 rounded-[6px] px-2 text-fg-2">
          <AlertTriangle className={a.kind === 'offline' || a.kind === 'gpu_error' ? 'size-3.5 shrink-0 text-fail' : 'size-3.5 shrink-0 text-warn'} />
          <span className="min-w-0 flex-1 truncate" title={a.text}>
            {a.text}
          </span>
          {(a.kind === 'external' || a.kind === 'blocked') && (
            <button type="button" onClick={() => markRead([a.key])} className="grid size-5 place-items-center rounded-[4px] text-fg-3 hover:bg-hover hover:text-fg" aria-label={t('overview.dismiss')} title={t('overview.dismiss')}>
              <X className="size-3.5" />
            </button>
          )}
        </div>
      ))}
    </div>
  )
}
