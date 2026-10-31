import { projectOf, type JobStatus } from '@danlu/core'
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
          <FilterMenu label="服务器" value={server} onChange={setServer} options={view.servers.map((s) => ({ value: s.name, label: s.name }))} />
        )}
        <FilterMenu label="项目" value={project} onChange={setProject} options={projects} />
        <FilterMenu
          label="状态"
          value={status}
          onChange={setStatus}
          options={[
            { value: 'unread' as const, label: '没看过的失败', count: unread },
            { value: 'running' as const, label: '运行中', count: count('running') },
            { value: 'queued' as const, label: '排队', count: count('queued') },
            { value: 'failed' as const, label: '失败', count: count('failed') },
            { value: 'succeeded' as const, label: '成功', count: count('succeeded') },
            { value: 'cancelled' as const, label: '已取消', count: count('cancelled') },
          ]}
        />
        <span className="num ml-auto pr-2 text-[11.5px] text-fg-3">
          运行 {count('running')} · 排队 {count('queued')} · 空卡 {free}
        </span>
        <Button variant="primary" onClick={() => openSubmit()}>
          <Plus />
          新建任务
          <Kbd className="border-on-primary/30 text-on-primary/70">N</Kbd>
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-1.5">
        {list.length === 0 ? (
          <Empty title={all.length === 0 ? '炉火已熄' : '没有符合筛选的任务'}>
            {all.length === 0 ? <p>24 小时内没有任务。按 N 新建一个。</p> : <p>换个筛选条件试试</p>}
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
            <button type="button" onClick={() => markRead([a.key])} className="grid size-5 place-items-center rounded-[4px] text-fg-3 hover:bg-hover hover:text-fg" aria-label="知道了" title="知道了">
              <X className="size-3.5" />
            </button>
          )}
        </div>
      ))}
    </div>
  )
}
