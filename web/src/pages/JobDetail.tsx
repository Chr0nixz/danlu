import { useQuery } from '@tanstack/react-query'
import { ArrowDown, ArrowUpRight, Check, ChevronLeft, Copy, RotateCw, X } from 'lucide-react'
import { useRef, useState, type ReactNode } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { CancelDialog } from '../components/JobRow'
import { CodeBrowser } from '../components/CodeBrowser'
import { LogViewer } from '../components/LogViewer'
import { Alert, Button, buttonClass, Card, Commit, cx, Empty, ErrorBox, GpuChips, Skeleton, StatusIcon, StatusLabel, tabPanelProps, Tabs } from '../components/ui'
import { ApiError, compactProgress, duration, fmtTime, isActive, jobDuration, now, type Job } from '@danlu/core'
import { api } from '../lib/api'

type Tab = 'log' | 'info' | 'code'
const tabs: Tab[] = ['log', 'info', 'code']

export function JobDetail() {
  const id = Number(useParams().id)
  const q = useQuery({
    queryKey: ['job', id],
    queryFn: () => api.job(id),
    refetchInterval: (query) => (query.state.data && !isActive(query.state.data) ? false : 2000),
  })
  // 标签页放在地址里（?tab=code），commit 链接可以直接打开代码快照，刷新也不丢
  const [params, setParams] = useSearchParams()
  const tab = tabs.find((t) => t === params.get('tab')) ?? 'log'
  const setTab = (t: Tab) =>
    setParams(
      (p) => {
        if (t === 'log') p.delete('tab')
        else p.set('tab', t)
        return p
      },
      { replace: true },
    )
  const [confirming, setConfirming] = useState(false)
  const panel = useRef<HTMLDivElement>(null)

  const showLogEnd = () => {
    setTab('log')
    requestAnimationFrame(() => panel.current?.scrollIntoView({ block: 'start', behavior: 'smooth' }))
  }

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
  if (q.error) {
    // 编号不存在或不是数字；安全入口失效也是 404，但那种要显示原文，告诉用户重新打开入口地址
    const missing = q.error instanceof ApiError && (q.error.status === 400 || q.error.message === '任务不存在')
    return (
      <div className="space-y-4">
        <BackLink />
        {missing ? (
          <Card>
            <Empty action={<Link to="/jobs" className={buttonClass('secondary', 'sm')}>回到任务列表</Link>}>任务 #{id} 不存在，可能输错了编号</Empty>
          </Card>
        ) : (
          <ErrorBox error={q.error} />
        )}
      </div>
    )
  }
  const j = q.data!
  const trackerName = j.tracker_url.includes('swanlab') ? 'SwanLab' : 'W&B'

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <BackLink />
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
              <Button size="sm" variant="danger" onClick={() => setConfirming(true)}>
                <X />
                取消
              </Button>
            )}
          </div>
        </div>
      </div>

      <MetaStrip j={j} />

      {j.status === 'failed' && <FailBox j={j} onShowLog={showLogEnd} />}
      {j.status !== 'failed' && j.fail_reason && <Alert>{j.fail_reason}</Alert>}
      {j.cancel_requested && j.status === 'running' && <Alert tone="warn">已发送结束信号，正在等进程退出；超时会强制结束。</Alert>}

      <div ref={panel} className="scroll-mt-16">
        <Tabs
          id="job"
          value={tab}
          onChange={setTab}
          tabs={[
            { value: 'log', label: '日志' },
            { value: 'info', label: '详情' },
            { value: 'code', label: '代码' },
          ]}
        />
        <div className="pt-4" {...tabPanelProps('job', tab)}>
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
      {confirming && <CancelDialog job={j} onClose={() => setConfirming(false)} />}
    </div>
  )
}

function BackLink() {
  return (
    <Link to="/jobs" className="inline-flex items-center gap-0.5 text-ui text-fg-3 hover:text-fg">
      <ChevronLeft className="size-4" aria-hidden />
      任务
    </Link>
  )
}

/**
 * 失败原因。最醒目的位置放最有用的信息：日志里的报错行（找不到时用最后一行输出），
 * 而不是状态栏里已经有的退出码；退出码以外的原因（被外部终止、启动失败、清理了残留进程）单独写一行。
 */
function FailBox({ j, onShowLog }: { j: Job; onShowLog: () => void }) {
  const line = j.error_line || (j.last_line && compactProgress(j.last_line).text)
  const extra = j.fail_reason && !/^退出码 -?\d+$/.test(j.fail_reason) ? j.fail_reason : ''
  return (
    <Alert tone="fail">
      {line ? (
        <>
          {!j.error_line && <div className="text-xs">没有找到报错行，最后一行输出是：</div>}
          <div className="font-mono text-[12.5px] leading-relaxed [overflow-wrap:anywhere]">{line}</div>
          {extra && <div className="mt-0.5">{extra}</div>}
        </>
      ) : (
        <div>{j.fail_reason || '任务失败，没有留下输出'}</div>
      )}
      {j.started_at && (
        <button type="button" onClick={onShowLog} className="mt-1 inline-flex items-center gap-1 text-xs font-medium underline decoration-fail/40 underline-offset-2 hover:decoration-fail">
          看日志末尾
          <ArrowDown className="size-3.5" aria-hidden />
        </button>
      )}
    </Alert>
  )
}

function Meta({ label, children, className, live }: { label: ReactNode; children: ReactNode; className?: string; live?: boolean }) {
  return (
    <div className={cx('min-w-0 space-y-1', className)}>
      <div className="truncate text-xs text-fg-3">{label}</div>
      <div className="flex min-h-5 min-w-0 items-center gap-1.5 text-ui text-fg" aria-live={live ? 'polite' : undefined}>
        {children}
      </div>
    </div>
  )
}

/** 详情页顶部的一排关键信息：状态、卡、环境、时长、代码版本 */
function MetaStrip({ j }: { j: Job }) {
  const d = jobDuration(j)
  const gpus = j.gpus_assigned.length ? j.gpus_assigned : j.gpus_pinned
  return (
    <Card className="grid grid-cols-2 gap-x-4 gap-y-3 px-4 py-3 sm:grid-cols-3 lg:grid-cols-[repeat(6,auto)] lg:justify-between">
      <Meta label="状态" live>
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
      <Meta
        label={
          <>
            代码版本
            {j.git_dirty && <span className="text-warn"> · 有未提交改动</span>}
          </>
        }
      >
        {j.git_commit ? (
          <>
            <Commit sha={j.git_commit} dirty={j.git_dirty} to="?tab=code" className="text-ui text-fg" />
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
        {j.inherit_bashrc && <Row label="Bash 环境">启动时加载服务器 ~/.bashrc</Row>}
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
