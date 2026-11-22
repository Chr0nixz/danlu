import { apiErrorText, compactProgress, duration, failText, fmtTime, isActive, jobDuration, projectOf, type Job } from '@danlu/core'
import { t } from '../lib/i18n'
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
      toast(t('job.rerun_toast', { key: jobKey(server, n.id) }))
      refreshFleet()
      openJob(server, n.id)
    },
    onError: (e) => toast(t('job.rerun_failed', { error: apiErrorText(e) }), 'fail'),
  })

  const download = async (path: string, filename: string) => {
    try {
      const saved = await saveFile(server, path, filename)
      toast(t('job.saved_to', { path: saved }))
      revealFile(saved)
    } catch (e) {
      toast(String(e), 'fail')
    }
  }

  if (!j) {
    return (
      <div className="flex h-full flex-col p-4">
        <div className="flex justify-end">
          <Button variant="ghost" className="px-1.5" aria-label={t('job.close_aria')} onClick={closePane}>
            <X />
          </Button>
        </div>
        {q.error ? <ErrorText error={q.error} /> : <p className="text-fg-3">{t('job.loading')}</p>}
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
        <Button variant="ghost" className="-mt-0.5 -mr-1 px-1.5" title={t('job.close_title')} aria-label={t('job.close_aria')} onClick={closePane}>
          <X />
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-1.5 px-1">
        {isActive(j) && (
          <Button variant="danger" disabled={j.cancel_requested} onClick={() => setCancelling(true)}>
            <Square />
            {j.cancel_requested ? t('job.cancelling') : t('common.cancel')}
          </Button>
        )}
        {!isActive(j) && (
          <Button loading={rerun.isPending} onClick={() => rerun.mutate(j)} title={t('job.rerun_hint')}>
            <RotateCcw />
            {t('job.rerun')}
          </Button>
        )}
        <Button onClick={() => openSubmit(draftFrom(j, server))} title={t('job.copy_hint')}>
          <Copy />
          {t('common.copy')}
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
          <Row k={t('job.row_reason')}>
            <span className="selectable text-fail">{failText(j)}</span>
          </Row>
        )}
        {line && (
          <Row k={t('job.row_progress')}>
            <span className="num selectable text-[11.5px] text-fg-2">{line}</span>
          </Row>
        )}
        <Row k={t('job.row_cmd')}>
          <span className="num selectable text-[11.5px]">{j.cmd}</span>
        </Row>
        <Row k={t('job.row_dir')}>
          <span className="num selectable text-[11.5px]">
            {j.workdir}
            {j.conda_env && <span className="text-fg-3"> · conda {j.conda_env}</span>}
          </span>
        </Row>
        <Row k={t('job.row_code')}>
          {j.git_commit ? (
            <span className="num selectable text-[11.5px]">
              {j.git_commit.slice(0, 8)}
              {j.git_branch && <span className="text-fg-3"> · {j.git_branch}</span>}
              {j.git_dirty && <span className="text-warn">{t('job.dirty_mark')}</span>}
            </span>
          ) : (
            <span className="text-fg-3">{j.snapshot_note || (j.started_at ? t('job.not_git') : t('job.snapshot_at_start'))}</span>
          )}
        </Row>
        <Row k={t('job.row_cards')}>
          <span className="num text-[11.5px]">
            {j.gpus_assigned.length ? gpuRange(j.gpus_assigned) : j.gpus_pinned.length ? t('job.pinned', { range: gpuRange(j.gpus_pinned) }) : t('job.cards_count', { n: j.gpus_req })}
            {Object.keys(j.env).length > 0 && <span className="text-fg-3"> · {Object.entries(j.env).map(([k, v]) => `${k}=${v}`).join(' ')}</span>}
          </span>
        </Row>
        {j.inherit_bashrc && <Row k="Bash">{t('job.bashrc_value')}</Row>}
        <Row k={t('job.row_time')}>
          <span className="num text-[11.5px] text-fg-2">
            {fmtTime(j.created_at).slice(5)}
            {j.started_at && t('job.started_at', { time: fmtTime(j.started_at).slice(11) })}
            {j.ended_at && t('job.ended_at', { time: fmtTime(j.ended_at).slice(11) })}
            {j.rerun_of && <span className="text-fg-3">{t('job.rerun_of', { key: jobKey(server, j.rerun_of) })}</span>}
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
              { value: 'log', label: t('job.tab_log') },
              { value: 'code', label: t('job.tab_code') },
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
          {j.status === 'queued' ? t('job.queued_pos', { pos: j.queue_pos ?? '-' }) : t('job.never_ran')}
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
      toast(t('job.cancelled_toast', { key: jobKey(server, j.id) }))
      refreshFleet()
      onClose()
    },
  })
  const running = j.status === 'running'
  const d = jobDuration(j)
  return (
    <Dialog
      title={t('job.cancel_title', { key: jobKey(server, j.id) })}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>{running ? t('job.keep_running') : t('job.keep_queued')}</Button>
          <Button variant="danger" loading={cancel.isPending} onClick={() => cancel.mutate()}>
            {t('job.cancel_confirm')}
          </Button>
        </>
      }
    >
      <p className="font-medium break-all text-fg">{j.name}</p>
      <p>
        {running
          ? t('job.cancel_running_body', { duration: d !== null ? duration(d) : '-' })
          : `${j.queue_pos ? t('job.cancel_queued_prefix', { pos: j.queue_pos }) : ''}${t('job.cancel_queued_body')}`}
      </p>
      <ErrorText error={cancel.error} />
    </Dialog>
  )
}
