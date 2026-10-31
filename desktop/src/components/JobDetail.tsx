import { compactProgress, duration, failText, fmtTime, isActive, jobDuration, projectOf, type Job } from '@danlu/core'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Copy, ExternalLink, RotateCcw, Square, X } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { apiFor, markRead, openExternal, refreshFleet, revealFile, saveFile } from '../lib/backend'
import { jobKey } from '../lib/fleet'
import { draftFrom, useWorkspace } from '../lib/workspace'
import { gpuRange, progressOf } from './JobList'
import { LogPane } from './LogPane'
import { SnapshotPane } from './SnapshotPane'
import { Button, closeDialog, Dialog, ErrorText, Kbd, Progress, Segmented, StatusLabel, useToast } from './ui'

function Row({ k, children }: { k: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-fg-3">{k}</dt>
      <dd className="min-w-0 break-all">{children}</dd>
    </>
  )
}

/** 右侧分栏的任务详情：状态、操作、关键信息，下面是实时日志或代码快照 */
export function JobDetail({ server, id, initial }: { server: string; id: number; initial?: Job }) {
  const api = apiFor(server)
  // 每轮汇总刷新时，fleet.ts 会让这个查询重新取一次
  const q = useQuery({ queryKey: ['job', server, id], queryFn: () => api.job(id), initialData: initial, staleTime: 2000 })
  const { closePane, openSubmit, openJob } = useWorkspace()
  const toast = useToast()
  const [tab, setTab] = useState<'log' | 'code'>('log')
  const [cancelling, setCancelling] = useState(false)
  const j = q.data
  const key = jobKey(server, id)

  // 在右侧打开过的失败任务算已读
  useEffect(() => {
    if (j?.status === 'failed') markRead([key])
  }, [key, j?.status])

  const rerun = useMutation({
    mutationFn: (job: Job) =>
      api.submit({
        name: job.name,
        workdir: job.workdir,
        conda_env: job.conda_env,
        cmd: job.cmd,
        env: job.env,
        inherit_bashrc: job.inherit_bashrc ?? false,
        gpus: job.gpus_req,
        pinned_gpus: job.gpus_pinned,
        rerun_of: job.id,
      }),
    onSuccess: (n) => {
      toast(`已重跑为 ${jobKey(server, n.id)}`)
      refreshFleet()
      openJob(server, n.id)
    },
    onError: (e) => toast(`重跑失败：${e instanceof Error ? e.message : e}`, 'fail'),
  })

  const download = async (path: string, filename: string) => {
    try {
      const saved = await saveFile(server, path, filename)
      toast(`已保存到 ${saved}`)
      revealFile(saved)
    } catch (e) {
      toast(String(e), 'fail')
    }
  }

  if (!j) {
    return (
      <div className="flex h-full flex-col p-4">
        <div className="flex justify-end">
          <Button variant="ghost" className="px-1.5" aria-label="关闭详情" onClick={closePane}>
            <X />
          </Button>
        </div>
        {q.error ? <ErrorText error={q.error} /> : <p className="text-fg-3">读取中…</p>}
      </div>
    )
  }

  const pct = progressOf(j)
  const d = jobDuration(j)
  const line = j.status === 'running' && j.last_line ? compactProgress(j.last_line).text : null

  return (
    <div key={key} className="rise flex h-full flex-col gap-3 p-3">
      <div className="flex items-start gap-3 px-1 pt-1">
        <div className="min-w-0 flex-1">
          <div className="num text-[11.5px] text-fg-3">
            {key} · {projectOf(j.workdir)}
          </div>
          <h2 className="selectable mt-0.5 truncate text-[15px] font-semibold">{j.name}</h2>
          <div className="mt-1.5 flex items-center gap-3">
            <StatusLabel job={j} />
            {pct !== null && <Progress pct={pct} className="max-w-[180px] flex-1" />}
            {pct !== null && <span className="num text-[11.5px] text-fg-2">{pct}%</span>}
            {d !== null && <span className="num ml-auto text-[11.5px] text-fg-3">{duration(d)}</span>}
          </div>
        </div>
        <Button variant="ghost" className="-mt-0.5 -mr-1 px-1.5" title="关闭（Esc）" aria-label="关闭详情" onClick={closePane}>
          <X />
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-1.5 px-1">
        {isActive(j) && (
          <Button variant="danger" disabled={j.cancel_requested} onClick={() => setCancelling(true)}>
            <Square />
            {j.cancel_requested ? '正在结束…' : '取消'}
          </Button>
        )}
        {!isActive(j) && (
          <Button loading={rerun.isPending} onClick={() => rerun.mutate(j)} title="原样再跑一次">
            <RotateCcw />
            重跑
          </Button>
        )}
        <Button onClick={() => openSubmit(draftFrom(j, server))} title="复制到提交表单，改完再提交">
          <Copy />
          复制
          <Kbd className="ml-0.5">C</Kbd>
        </Button>
        {j.tracker_url && (
          <Button variant="ghost" onClick={() => openExternal(j.tracker_url)} title={j.tracker_url}>
            <ExternalLink />
            {j.tracker_url.includes('swanlab') ? 'SwanLab' : 'W&B'}
          </Button>
        )}
      </div>

      <dl className="grid grid-cols-[52px_1fr] gap-x-3 gap-y-1 rounded-[10px] border border-line bg-card px-3 py-2.5">
        {j.status === 'failed' && (
          <Row k="原因">
            <span className="selectable text-fail">{failText(j)}</span>
          </Row>
        )}
        {line && (
          <Row k="进度">
            <span className="num selectable text-[11.5px] text-fg-2">{line}</span>
          </Row>
        )}
        <Row k="命令">
          <span className="num selectable text-[11.5px]">{j.cmd}</span>
        </Row>
        <Row k="目录">
          <span className="num selectable text-[11.5px]">
            {j.workdir}
            {j.conda_env && <span className="text-fg-3"> · conda {j.conda_env}</span>}
          </span>
        </Row>
        <Row k="代码">
          {j.git_commit ? (
            <span className="num selectable text-[11.5px]">
              {j.git_commit.slice(0, 8)}
              {j.git_branch && <span className="text-fg-3"> · {j.git_branch}</span>}
              {j.git_dirty && <span className="text-warn"> · 有未提交的改动</span>}
            </span>
          ) : (
            <span className="text-fg-3">{j.snapshot_note || (j.started_at ? '不是 git 仓库' : '启动时记录')}</span>
          )}
        </Row>
        <Row k="卡">
          <span className="num text-[11.5px]">
            {j.gpus_assigned.length ? gpuRange(j.gpus_assigned) : j.gpus_pinned.length ? `指定 ${gpuRange(j.gpus_pinned)}` : `${j.gpus_req} 张`}
            {Object.keys(j.env).length > 0 && <span className="text-fg-3"> · {Object.entries(j.env).map(([k, v]) => `${k}=${v}`).join(' ')}</span>}
          </span>
        </Row>
        {j.inherit_bashrc && <Row k="Bash">启动时加载服务器 ~/.bashrc</Row>}
        <Row k="时间">
          <span className="num text-[11.5px] text-fg-2">
            {fmtTime(j.created_at).slice(5)}
            {j.started_at && ` 开始 ${fmtTime(j.started_at).slice(11)}`}
            {j.ended_at && ` 结束 ${fmtTime(j.ended_at).slice(11)}`}
            {j.rerun_of && <span className="text-fg-3"> · 重跑自 {jobKey(server, j.rerun_of)}</span>}
          </span>
        </Row>
      </dl>

      {j.started_at ? (
        <>
          <Segmented
            value={tab}
            onChange={setTab}
            className="self-start"
            options={[
              { value: 'log', label: '日志' },
              { value: 'code', label: '代码快照' },
            ]}
          />
          {tab === 'log' ? (
            <LogPane key={key} api={api} jobId={j.id} onDownload={() => download(`/api/jobs/${j.id}/log/download`, `danlu-${key}.log`)} />
          ) : (
            <SnapshotPane key={key} server={server} jobId={j.id} onDownload={() => download(`/api/jobs/${j.id}/snapshot/download`, `danlu-${key}-code.tar.gz`)} />
          )}
        </>
      ) : (
        <div className="flex flex-1 items-center justify-center rounded-[8px] bg-term text-[#8b8e97]">
          {j.status === 'queued' ? `排在第 ${j.queue_pos ?? '-'} 位，开始运行后显示日志` : '没有运行过'}
        </div>
      )}

      {cancelling && <CancelDialog server={server} job={j} onClose={() => setCancelling(false)} />}
    </div>
  )
}

/** 取消任务没有快捷键，必须点按钮并确认 */
function CancelDialog({ server, job: j, onClose }: { server: string; job: Job; onClose: () => void }) {
  const toast = useToast()
  const cancel = useMutation({
    mutationFn: () => apiFor(server).cancel(j.id),
    onSuccess: () => {
      toast(`已取消 ${jobKey(server, j.id)}`)
      refreshFleet()
      onClose()
    },
  })
  const running = j.status === 'running'
  const d = jobDuration(j)
  return (
    <Dialog
      title={`取消 ${jobKey(server, j.id)}？`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>{running ? '继续运行' : '保留排队'}</Button>
          <Button variant="danger" loading={cancel.isPending} onClick={() => cancel.mutate()}>
            取消任务
          </Button>
        </>
      }
    >
      <p className="font-medium break-all text-fg">{j.name}</p>
      <p>
        {running
          ? `已经运行 ${d !== null ? duration(d) : '-'}。确定后进程会被结束，不能恢复；日志和代码快照会保留。`
          : `${j.queue_pos ? `排在第 ${j.queue_pos} 位，` : ''}取消后从队列里移除。`}
      </p>
      <ErrorText error={cancel.error} />
    </Dialog>
  )
}
