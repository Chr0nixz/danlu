import { useQuery } from '@tanstack/react-query'
import { ArrowUpRight, Check, ChevronLeft, Copy, RotateCw, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'
import { CodeBrowser } from '../components/CodeBrowser'
import { confirmCancel, useCancel } from '../components/JobRow'
import { LogViewer } from '../components/LogViewer'
import { Alert, Button, buttonClass, Card, Commit, cx, ErrorBox, GpuChips, Skeleton, StatusIcon, StatusLabel, Tabs } from '../components/ui'
import { api, type Job } from '../lib/api'
import { duration, fmtTime, isActive, jobDuration, now } from '../lib/format'

export function JobDetail() {
  const id = Number(useParams().id)
  const q = useQuery({
    queryKey: ['job', id],
    queryFn: () => api.job(id),
    refetchInterval: (query) => (query.state.data && !isActive(query.state.data) ? false : 2000),
  })
  const [tab, setTab] = useState<'log' | 'info' | 'code'>('log')
  const cancel = useCancel()

  if (q.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-7 w-72" />
        <Skeleton className="h-16 w-full rounded-lg" />
        <Skeleton className="h-[50vh] w-full rounded-lg" />
      </div>
    )
  }
  if (q.error) return <ErrorBox error={q.error} />
  const j = q.data!
  const trackerName = j.tracker_url.includes('swanlab') ? 'SwanLab' : 'W&B'

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Link to="/jobs" className="inline-flex items-center gap-0.5 text-ui text-fg-3 hover:text-fg">
          <ChevronLeft className="size-4" />
          任务
        </Link>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
          <h1 className="flex min-w-0 flex-1 items-start gap-2 text-xl font-semibold tracking-tight">
            <StatusIcon status={j.status} cancelling={j.cancel_requested} className="mt-1.5 size-5" />
            <span className="min-w-0 break-all">
              {j.name}
              <span className="ml-2 font-normal text-fg-3">#{j.id}</span>
            </span>
          </h1>
          <div className="flex gap-2 [&>*]:flex-1 sm:[&>*]:flex-none">
            {j.tracker_url && (
              <a href={j.tracker_url} target="_blank" rel="noreferrer" className={buttonClass('secondary', 'sm')}>
                {trackerName}
                <ArrowUpRight />
              </a>
            )}
            <Link to={`/new?from=${j.id}`} className={buttonClass('secondary', 'sm')} title="用同样的设置再跑一次，可以先改参数">
              <RotateCw />
              重跑
            </Link>
            {isActive(j) && !j.cancel_requested && (
              <Button size="sm" variant="danger" loading={cancel.isPending} onClick={() => confirmCancel(j) && cancel.mutate(j)}>
                <X />
                取消
              </Button>
            )}
          </div>
        </div>
      </div>

      <MetaStrip j={j} />

      {j.status === 'failed' && j.fail_reason && <Alert tone="fail">{j.fail_reason}</Alert>}
      {j.status !== 'failed' && j.fail_reason && <Alert>{j.fail_reason}</Alert>}
      {j.cancel_requested && j.status === 'running' && <Alert tone="warn">已发送结束信号，正在等进程退出；超时会强制结束。</Alert>}

      <div>
        <Tabs
          value={tab}
          onChange={setTab}
          tabs={[
            { value: 'log', label: '日志' },
            { value: 'info', label: '详情' },
            { value: 'code', label: '代码' },
          ]}
        />
        <div className="pt-4">
          {tab === 'log' &&
            (j.status === 'queued' ? (
              <Card className="px-4 py-12 text-center text-ui text-fg-3">
                任务还在排队{j.queue_pos ? `（第 ${j.queue_pos} 位）` : ''}，开始运行后这里会显示日志
              </Card>
            ) : (
              <LogViewer jobId={j.id} />
            ))}
          {tab === 'info' && <Overview j={j} />}
          {tab === 'code' && <CodeBrowser jobId={j.id} />}
        </div>
      </div>
    </div>
  )
}

function Meta({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div className={cx('min-w-0 space-y-1', className)}>
      <div className="text-xs text-fg-3">{label}</div>
      <div className="flex min-h-5 min-w-0 items-center gap-1.5 text-ui text-fg">{children}</div>
    </div>
  )
}

/** 详情页顶部的一排关键信息：状态、卡、环境、时长、代码版本 */
function MetaStrip({ j }: { j: Job }) {
  const d = jobDuration(j)
  const gpus = j.gpus_assigned.length ? j.gpus_assigned : j.gpus_pinned
  return (
    <Card className="grid grid-cols-2 gap-x-4 gap-y-3 px-4 py-3 sm:grid-cols-3 lg:grid-cols-[repeat(6,auto)] lg:justify-between">
      <Meta label="状态">
        <StatusLabel job={j} />
        {j.exit_code !== null && j.status !== 'succeeded' && <span className="text-fg-3">退出码 {j.exit_code}</span>}
      </Meta>
      <Meta label="GPU">
        {gpus.length ? <GpuChips ids={gpus} /> : <span className="text-fg-2">{j.gpus_req ? `${j.gpus_req} 张，未分配` : '不用 GPU'}</span>}
      </Meta>
      <Meta label="conda 环境">
        <span className="truncate">{j.conda_env || <span className="text-fg-3">不使用</span>}</span>
      </Meta>
      <Meta label={j.status === 'running' ? '已运行' : j.status === 'queued' ? '已等待' : '耗时'}>
        {j.status === 'queued' ? duration(now() - j.created_at) : d !== null ? duration(d) : <span className="text-fg-3">-</span>}
      </Meta>
      <Meta label="代码版本">
        {j.git_commit ? (
          <>
            <Commit sha={j.git_commit} dirty={j.git_dirty} className="text-ui text-fg" />
            {j.git_branch && <span className="truncate text-fg-3">{j.git_branch}</span>}
          </>
        ) : (
          <span className="truncate text-fg-3">{j.snapshot_ok ? '非 git，已存快照' : j.status === 'queued' ? '启动时保存' : '无快照'}</span>
        )}
      </Meta>
      <Meta label={j.ended_at ? '结束于' : j.started_at ? '开始于' : '提交于'}>
        <span className="truncate">{fmtTime(j.ended_at ?? j.started_at ?? j.created_at).slice(5, 16)}</span>
      </Meta>
    </Card>
  )
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1 px-4 py-3 sm:grid-cols-[128px_1fr] sm:gap-4">
      <dt className="text-ui text-fg-3">{label}</dt>
      <dd className="min-w-0 break-all text-ui text-fg">{children}</dd>
    </div>
  )
}

function CopyBlock({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="group relative">
      <pre className="term overflow-x-auto rounded-md p-3 pr-10 whitespace-pre-wrap break-all">{text}</pre>
      <button
        type="button"
        className="absolute right-1.5 top-1.5 grid size-7 place-items-center rounded text-[#8f8f8f] hover:bg-white/10 hover:text-white"
        onClick={() => {
          navigator.clipboard?.writeText(text)
          setCopied(true)
          setTimeout(() => setCopied(false), 1500)
        }}
        aria-label="复制命令"
        title="复制"
      >
        {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
      </button>
    </div>
  )
}

function Overview({ j }: { j: Job }) {
  const env = Object.entries(j.env)
  const link = 'text-fg underline decoration-line-strong hover:decoration-fg'
  return (
    <Card>
      <dl className="divide-y divide-line">
        <Row label="命令">
          <CopyBlock text={j.cmd} />
        </Row>
        <Row label="工作目录">
          <span className="font-mono text-[12.5px]">{j.workdir}</span>
        </Row>
        <Row label="GPU">
          申请 {j.gpus_pinned.length ? `指定卡 ${j.gpus_pinned.join(', ')}` : `${j.gpus_req} 张`}
          {j.gpus_assigned.length > 0 && <>，分到 GPU {j.gpus_assigned.join(', ')}</>}
        </Row>
        {j.master_port > 0 && (
          <Row label="端口">
            {j.master_port} <span className="text-fg-3">（已注入 PET_MASTER_PORT）</span>
          </Row>
        )}
        {env.length > 0 && (
          <Row label="环境变量">
            <div className="font-mono text-[12.5px] leading-relaxed">
              {env.map(([k, v]) => (
                <div key={k}>
                  <span className="text-fg-3">{k}=</span>
                  {v}
                </div>
              ))}
            </div>
          </Row>
        )}
        <Row label="代码版本">
          {j.git_commit ? (
            <span className="font-mono text-[12.5px]">
              {j.git_branch && `${j.git_branch} @ `}
              {j.git_commit.slice(0, 12)}
              {j.git_dirty && <span className="ml-2 font-sans text-warn">有未提交的改动，已保存在快照里</span>}
            </span>
          ) : j.snapshot_ok ? (
            '不是 git 仓库，代码已保存在快照里'
          ) : (
            j.snapshot_note || '启动时保存'
          )}
        </Row>
        {j.tracker_url && (
          <Row label="实验记录">
            <a href={j.tracker_url} target="_blank" rel="noreferrer" className={link}>
              {j.tracker_url}
            </a>
          </Row>
        )}
        {j.rerun_of && (
          <Row label="重跑自">
            <Link to={`/jobs/${j.rerun_of}`} className={link}>
              任务 #{j.rerun_of}
            </Link>
          </Row>
        )}
        <Row label="提交时间">{fmtTime(j.created_at)}</Row>
        <Row label="开始时间">{fmtTime(j.started_at)}</Row>
        <Row label="结束时间">{fmtTime(j.ended_at)}</Row>
        {j.pid > 0 && <Row label="进程">pid {j.pid}</Row>}
      </dl>
    </Card>
  )
}
