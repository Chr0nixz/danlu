import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUpRight, Bell, BellOff, Check, ChevronLeft, Copy, GitCompareArrows, NotebookPen, Pencil, RotateCw, Star, Terminal as TerminalIcon, X } from 'lucide-react'
import { useId, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { CancelDialog } from '../components/JobRow'
import { CodeBrowser } from '../components/CodeBrowser'
import { LogViewer } from '../components/LogViewer'
import { ProfileSummary } from '../components/ResourceProfile'
import { StarButton, StarError, StarMenuItem } from '../components/StarButton'
import { Alert, announce, Button, buttonClass, Card, Commit, cx, Empty, ErrorBox, FieldError, GpuChips, Input, Menu, menuItemClass, Skeleton, StatusIcon, StatusLabel, tabPanelProps, Tabs, Textarea } from '../components/ui'
import { ApiError, compactProgress, duration, fmtTime, isActive, jobDuration, now, sameCodeFailKinds, stripProgressTiming, type Job } from '@danlu/core'
import { api } from '../lib/api'

type Tab = 'log' | 'info' | 'code'
const tabs: Tab[] = ['log', 'info', 'code']

export function JobDetail() {
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const notifyToggle = useMutation({
    mutationFn: ({ id, on }: { id: number; on: boolean }) => api.setNotifyDone(id, on),
    onSuccess: (updated) => {
      qc.setQueryData(['job', updated.id], updated)
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      announce(updated.notify_done ? '已开启结束提醒' : '已关闭结束提醒')
    },
    onError: (_error, { id }) => void qc.invalidateQueries({ queryKey: ['job', id] }),
  })
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, staleTime: 60_000 })
  const q = useQuery({
    queryKey: ['job', id],
    queryFn: () => api.job(id),
    refetchInterval: (query) => {
      const job = query.state.data
      return job && !isActive(job) && !job.recipe?.pending ? false : 2000
    },
  })
  // 标签页放在地址里（?tab=code），commit 链接可以直接打开代码快照，刷新也不丢
  const [params, setParams] = useSearchParams()
  const tab = tabs.find((t) => t === params.get('tab')) ?? 'log'
  const offsetParam = params.get('offset')
  const parsedOffset = offsetParam === null ? undefined : Number(offsetParam)
  const logOffset = parsedOffset !== undefined && Number.isSafeInteger(parsedOffset) && parsedOffset >= 0 ? parsedOffset : undefined
  const setTab = (t: Tab) =>
    setParams(
      (p) => {
        if (t === 'log') p.delete('tab')
        else {
          p.set('tab', t)
          p.delete('offset')
        }
        return p
      },
      { replace: true },
    )
  const [confirming, setConfirming] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [memoEditing, setMemoEditing] = useState(false)
  const renameButton = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)

  const showLog = (offset?: number) => {
    setParams(
      (p) => {
        p.delete('tab')
        if (offset === undefined) p.delete('offset')
        else p.set('offset', String(offset))
        return p
      },
      { replace: true },
    )
    const smooth = !matchMedia('(prefers-reduced-motion: reduce)').matches
    requestAnimationFrame(() => panel.current?.scrollIntoView({ block: 'start', behavior: smooth ? 'smooth' : 'auto' }))
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
  const terminal = settings.data?.terminal_enabled === true
  // 每种状态只有一个主操作：在跑或排队的是取消，结束了的是重跑（失败的写明"改参数"）
  const canCancel = isActive(j) && !j.cancel_requested
  const notifyLabel = j.notify_done ? '关闭结束时推送' : '开启结束时推送'
  const NotifyIcon = j.notify_done ? Bell : BellOff
  const toggleNotify = () => notifyToggle.mutate({ id: j.id, on: !j.notify_done })
  // 和代码无关的失败（服务器重启、被外部终止等）重跑页默认用原任务的快照，按钮和失败说明里的建议说同一件事
  const sameCode = j.status === 'failed' && sameCodeFailKinds.includes(j.fail_kind ?? '')
  const rerunLabel = sameCode ? '原样重跑' : j.status === 'failed' ? '改参数重跑' : '重跑'
  const rerunTitle = sameCode ? '默认用原任务的代码快照再跑一次，提交前可以改参数' : '用同样的设置再跑一次，可以先改参数'
  // 次要操作用幽灵按钮：一排里只有实验记录链接和主操作带边框，一眼分得出主次
  const minor = buttonClass('ghost', 'sm')
  const tracker = (cls: string) =>
    j.tracker_url && (
      <a href={j.tracker_url} target="_blank" rel="noreferrer" className={cls}>
        {trackerName}
        <ArrowUpRight aria-hidden />
      </a>
    )
  const primary = (cls: string) =>
    canCancel ? (
      <Button size="sm" variant="danger" className={cls} onClick={() => setConfirming(true)}>
        <X aria-hidden />
        取消任务
      </Button>
    ) : (
      <Link to={`/new?from=${j.id}`} className={cx(buttonClass(isActive(j) ? 'secondary' : 'primary', 'sm'), cls)} title={rerunTitle}>
        <RotateCw aria-hidden />
        {rerunLabel}
      </Link>
    )

  return (
    // 手机上区块间距收到 12px，把高度让给日志
    <div className="space-y-3 sm:space-y-4">
      <div className="space-y-2">
        <BackLink />
        {renaming ? (
          <RenameForm
            job={j}
            onDone={() => {
              setRenaming(false)
              requestAnimationFrame(() => renameButton.current?.focus())
            }}
          />
        ) : (
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
            <div className="flex min-w-0 flex-1 items-start gap-1">
              <h1 className="flex min-w-0 items-start gap-2 text-xl font-semibold tracking-tight">
                <StatusIcon status={j.status} cancelling={j.cancel_requested} className="mt-1.5 size-5" />
                <span className="min-w-0 break-all">
                  {j.name}
                  <span className="ml-2 font-normal whitespace-nowrap text-fg-3">#{j.id}</span>
                  {j.starred && <Star role="img" aria-label="已标星" className="ml-2 inline size-4 -translate-y-px fill-current text-fg-3 sm:hidden" />}
                </span>
              </h1>
              <button
                ref={renameButton}
                type="button"
                className={cx(buttonClass('ghost', 'icon-sm'), '-mt-0.5 text-fg-3 max-sm:hidden')}
                aria-label="修改任务名"
                title="修改任务名"
                onClick={() => setRenaming(true)}
              >
                <Pencil aria-hidden />
              </button>
            </div>

            {/* 电脑：操作都露在外面，次要的是幽灵按钮，实验记录和主操作带边框，主操作放最右 */}
            <div className="hidden flex-wrap justify-end gap-1 sm:flex">
              <StarButton job={j} label />
              {isActive(j) && (
                <Button size="icon-sm" variant="ghost" loading={notifyToggle.isPending} aria-label="结束时推送" aria-pressed={!!j.notify_done}
                  title={notifyLabel} onClick={toggleNotify}>
                  <NotifyIcon aria-hidden />
                </Button>
              )}
              {terminal && (
                <Link to={`/terminal?job_id=${j.id}`} className={minor} title="在这个任务的目录和 conda 环境中打开终端">
                  <TerminalIcon aria-hidden />
                  终端
                </Link>
              )}
              <Link to={`/compare?a=${j.id}`} className={minor}>
                <GitCompareArrows aria-hidden />
                和…对比
              </Link>
              <Link to={`/new?copy=${j.id}`} className={minor} title="复制任务设置，提交为独立的新任务">
                <Copy aria-hidden />
                复制任务
              </Link>
              {canCancel && (
                <Link to={`/new?from=${j.id}`} className={minor} title={rerunTitle}>
                  <RotateCw aria-hidden />
                  {rerunLabel}
                </Link>
              )}
              {tracker(cx(buttonClass('secondary', 'sm'), 'ml-1'))}
              {primary('ml-1')}
            </div>

            {/* 手机：一行放主操作和实验记录，其余收进"更多"，把首屏留给日志 */}
            <div className="flex items-center gap-2 sm:hidden">
              {primary('px-3.5 text-sm')}
              {tracker(cx(buttonClass('secondary', 'sm'), 'px-3.5 text-sm'))}
              <Menu label="更多操作" className="ml-auto">
                <StarMenuItem job={j} />
                {/* 没写过备注时，手机上"写备注"收在这里，不占日志上方的一行 */}
                {!j.memo && (
                  <button type="button" role="menuitem" className={menuItemClass} onClick={() => setMemoEditing(true)}>
                    <NotebookPen aria-hidden />
                    写备注
                  </button>
                )}
                {isActive(j) && (
                  <button type="button" role="menuitem" className={menuItemClass} disabled={notifyToggle.isPending} onClick={toggleNotify}>
                    <NotifyIcon aria-hidden />
                    {notifyLabel}
                  </button>
                )}
                <button type="button" role="menuitem" className={menuItemClass} onClick={() => setRenaming(true)}>
                  <Pencil aria-hidden />
                  改名
                </button>
                {canCancel && (
                  <Link role="menuitem" to={`/new?from=${j.id}`} className={menuItemClass}>
                    <RotateCw aria-hidden />
                    {rerunLabel}
                  </Link>
                )}
                <Link role="menuitem" to={`/new?copy=${j.id}`} className={menuItemClass}>
                  <Copy aria-hidden />
                  复制任务
                </Link>
                {terminal && (
                  <Link role="menuitem" to={`/terminal?job_id=${j.id}`} className={menuItemClass}>
                    <TerminalIcon aria-hidden />
                    终端
                  </Link>
                )}
                <Link role="menuitem" to={`/compare?a=${j.id}`} className={menuItemClass}>
                  <GitCompareArrows aria-hidden />
                  和…对比
                </Link>
              </Menu>
            </div>
          </div>
        )}
      </div>

      <JobMemo key={j.id} job={j} editing={memoEditing} onEditing={setMemoEditing} />
      <MetaStrip j={j} />
      <StarError />
      {notifyToggle.variables?.id === j.id && <ErrorBox error={notifyToggle.error} />}

      {j.status === 'failed' && <FailBox j={j} onShowLog={showLog} />}
      {j.status !== 'failed' && j.fail_reason && <Alert>{j.fail_reason}</Alert>}
      {j.notes && <Alert>运行说明：{j.notes}</Alert>}
      {j.status === 'running' && j.silent_since && (
        // 操作写成一行文字链接：不撑高提示框（日志要留在首屏），点击区各自向上下撑到 44px。
        // 取消任务用失败红并单独靠右，和两个只是去看一眼的操作分开，这时候最容易手抖
        <Alert tone="warn">
          <div className="flex flex-wrap items-center gap-x-5 gap-y-1">
            <span className="max-sm:basis-full">已 {duration(now() - j.silent_since)}没有新输出</span>
            <button type="button" className={cx(alertAction, alertActionTone)} onClick={() => showLog()}>看日志末尾</button>
            {terminal && <Link to={`/terminal?job_id=${j.id}`} className={cx(alertAction, alertActionTone)}>打开终端</Link>}
            {canCancel && (
              <button type="button" className={cx(alertAction, 'ml-auto text-fail decoration-fail/40 hover:decoration-fail')} onClick={() => setConfirming(true)}>
                取消任务
              </button>
            )}
          </div>
        </Alert>
      )}
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
        <div className="pt-3 sm:pt-4" {...tabPanelProps('job', tab)}>
          {tab === 'log' &&
            (j.pruned_at ? <PrunedFiles job={j} /> : j.status === 'queued' ? (
              <Card className="px-4 py-12 text-center text-ui text-fg-3">
                任务还在排队{j.queue_pos ? `（第 ${j.queue_pos} 位）` : ''}，开始运行后这里会显示日志
                {j.wait_reason && <p className="mt-2 px-2 break-words text-fg-2">{j.wait_reason}</p>}
              </Card>
            ) : (
              <LogViewer key={j.id} jobId={j.id} offset={logOffset} onJump={showLog} />
            ))}
          {tab === 'info' && <Overview j={j} />}
          {tab === 'code' && (j.pruned_at ? <PrunedFiles job={j} /> : <CodeBrowser jobId={j.id} />)}
        </div>
      </div>
      {confirming && <CancelDialog job={j} onClose={() => setConfirming(false)} />}
    </div>
  )
}

/** 提示框里的文字操作：一行字那么高，点击区向四周撑出去（上下各 12px，共 44px），不改变排版 */
const alertAction = 'relative shrink-0 font-medium underline underline-offset-2 before:absolute before:-inset-x-2 before:-inset-y-3'
const alertActionTone = 'text-fg decoration-fg/30 hover:decoration-fg'

function PrunedFiles({ job }: { job: Job }) {
  return (
    <Card className="space-y-2 px-4 py-8 text-center text-ui">
      <p className="font-medium">日志和代码快照已按保留策略清理</p>
      <p className="text-fg-3">清理于 {fmtTime(job.pruned_at)}。任务记录、环境信息和训练产物仍保留，可在“详情”中查看。</p>
    </Card>
  )
}

/**
 * 任务备注，就地编辑。编辑状态由详情页持有：手机上没写过备注时，这里不占一行，入口在更多操作里。
 */
function JobMemo({ job, editing, onEditing }: { job: Job; editing: boolean; onEditing(editing: boolean): void }) {
  const qc = useQueryClient()
  const button = useRef<HTMLButtonElement>(null)
  const errorId = useId()
  const [draft, setDraft] = useState(job.memo ?? '')
  const tooLong = Array.from(draft).length > 500
  const close = () => {
    onEditing(false)
    requestAnimationFrame(() => button.current?.focus())
  }
  const save = useMutation({
    mutationFn: (memo: string) => api.setMemo(job.id, memo),
    onSuccess: (updated) => {
      qc.setQueryData(['job', job.id], updated)
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      void qc.invalidateQueries({ queryKey: ['compare'] })
      void qc.invalidateQueries({ queryKey: ['trace'] })
      announce('备注已保存')
      close()
    },
  })
  // 每次进入编辑都从已保存的备注开始，上一次的报错不带过来
  const [wasEditing, setWasEditing] = useState(editing)
  if (editing !== wasEditing) {
    setWasEditing(editing)
    if (editing) {
      setDraft(job.memo ?? '')
      save.reset()
    }
  }
  if (!editing) {
    return (
      <button ref={button} type="button" aria-label={job.memo ? '编辑备注' : '写备注'} title={job.memo || undefined}
        className={cx('inline-flex max-w-full items-center gap-2 rounded px-1 text-left text-ui text-fg-3 hover:bg-hover hover:text-fg max-sm:min-h-11', !job.memo && 'max-sm:hidden')}
        onClick={() => onEditing(true)}>
        <Pencil className="size-3.5 shrink-0" aria-hidden />
        <span className="line-clamp-2 whitespace-pre-wrap [overflow-wrap:anywhere]">{job.memo || '写备注…'}</span>
      </button>
    )
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!tooLong && !save.isPending) save.mutate(draft)
  }
  return (
    <form onSubmit={submit} className="space-y-1">
      <div className="flex items-start gap-2">
        <Textarea aria-label="任务备注" autoFocus rows={2} value={draft}
          className="min-w-0 flex-1 max-sm:text-base" readOnly={save.isPending}
          aria-invalid={tooLong || undefined} aria-describedby={tooLong ? errorId : undefined}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (e.key === 'Escape' && !save.isPending) {
              e.preventDefault()
              close()
            }
            if (e.key === 'Enter' && !e.shiftKey && matchMedia('(min-width: 640px) and (pointer: fine)').matches) {
              e.preventDefault()
              e.currentTarget.form?.requestSubmit()
            }
          }} />
        <Button type="submit" variant="primary" size="sm" loading={save.isPending} disabled={tooLong}>保存</Button>
        <Button size="sm" disabled={save.isPending} onClick={close}>取消</Button>
      </div>
      {tooLong && <FieldError id={errorId}>备注最多 500 字</FieldError>}
      <ErrorBox error={save.error} />
    </form>
  )
}

/** 就地改名：Enter 保存，Esc 放弃，名字没变就直接退出。已经启动过的任务只改记录，输出目录不跟着改 */
function RenameForm({ job: j, onDone }: { job: Job; onDone: () => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState(j.name)
  const hintId = useId()
  const rename = useMutation({
    mutationFn: (next: string) => api.rename(j.id, next),
    onSuccess: (updated) => {
      qc.setQueryData(['job', j.id], updated)
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      announce(`任务 #${j.id} 已改名为 ${updated.name}`)
      onDone()
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const next = name.trim()
    if (next === j.name) onDone()
    else rename.mutate(next)
  }
  const launched = !!(j.output_dir || j.started_at)
  return (
    <form onSubmit={submit} className="space-y-2">
      <div className="flex items-center gap-2">
        <Input
          aria-label={`任务 #${j.id} 的名字`}
          aria-describedby={launched ? hintId : undefined}
          aria-invalid={rename.isError || undefined}
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.preventDefault()
              onDone()
            }
          }}
          onFocus={(e) => e.currentTarget.select()}
          autoFocus
          maxLength={100}
          className="flex-1"
        />
        <Button type="submit" variant="primary" size="sm" loading={rename.isPending} className="max-sm:px-3.5 max-sm:text-sm">
          保存
        </Button>
        <Button size="sm" onClick={onDone} className="max-sm:px-3.5 max-sm:text-sm">
          取消
        </Button>
      </div>
      {launched && (
        <p id={hintId} className="text-xs text-fg-3">
          只改任务记录里的名字；输出目录和 DANLU_JOB_NAME 仍是启动时的名字。
        </p>
      )}
      <ErrorBox error={rename.error} />
    </form>
  )
}

function BackLink() {
  return (
    // 点击区向四周撑到 44px 高，不改变排版
    <Link to="/jobs" className="relative inline-flex items-center gap-0.5 text-ui text-fg-3 before:absolute before:-inset-x-2 before:-inset-y-3 hover:text-fg">
      <ChevronLeft className="size-4" aria-hidden />
      任务
    </Link>
  )
}

/** 按失败分类给的下一步：只是建议，不替用户断定原因；退出码这类没法归类的不给 */
const failHint: Partial<Record<string, string>> = {
  cuda_oom: '显存不够：减小 batch size，或多申请一张卡再重跑。',
  host_oom: '主机内存不够：减少 DataLoader 的 num_workers，或等别的任务结束再重跑。',
  nccl: '多卡通信出错：可以先点"原样重跑"；反复出现时加环境变量 NCCL_DEBUG=INFO 查看原因。',
  exception: '代码报错：按报错行改好代码，再重跑。',
  reboot: '代码没问题的话，点"原样重跑"：默认用原任务的快照。',
  killed: '进程是被丹炉以外的程序结束的。确认原因后，可以点"原样重跑"：默认用原任务的快照。',
  signal: '不是你发的信号时，先看日志末尾找原因；要再跑一次，"原样重跑"默认用原任务的快照。',
  start: '任务没能启动：检查工作目录、conda 环境和 ~/.bashrc，再重跑。',
}

/**
 * 失败原因。最醒目的位置放最有用的信息：日志里的报错行（找不到时用最后一行输出），
 * 而不是状态栏里已经有的退出码；退出码以外的原因（被外部终止、启动失败）单独写一行，
 * 然后是按分类给的下一步，重跑按钮就在页面顶部。
 * 和代码无关的失败（服务器重启、被外部终止、收到信号）反过来：原因本身就是答案，写在第一行；
 * 日志里本来就没有报错行，最后一行输出只说明任务跑到了哪里，放在后面，不写"没有找到报错行"。
 */
function FailBox({ j, onShowLog }: { j: Job; onShowLog: (offset?: number) => void }) {
  // 没有报错行时用最后一行输出；它是 tqdm 进度条的话，已用时间和速率对失败的任务没有用，去掉
  const line = j.error_line || (j.last_line && stripProgressTiming(compactProgress(j.last_line).text))
  const extra = j.fail_reason && !/^退出码 -?\d+$/.test(j.fail_reason) ? j.fail_reason : ''
  const hint = failHint[j.fail_kind ?? '']
  const reasonFirst = !!extra && !j.error_line && sameCodeFailKinds.includes(j.fail_kind ?? '')
  const mono = 'font-mono text-[12.5px] leading-relaxed [overflow-wrap:anywhere]'
  return (
    <Alert tone="fail">
      {reasonFirst ? (
        <>
          <div className="font-medium">{extra}</div>
          {line && (
            <div className="mt-0.5">
              <span className="text-xs">中断前的最后一行输出：</span>
              <span className={mono}>{line}</span>
            </div>
          )}
        </>
      ) : line ? (
        <>
          {!j.error_line && <div className="text-xs">没有找到报错行，最后一行输出是：</div>}
          <div className={mono}>{line}</div>
          {extra && <div className="mt-0.5">{extra}</div>}
        </>
      ) : (
        <div>{j.fail_reason || '任务失败，没有留下输出'}</div>
      )}
      {hint && <div className="mt-0.5 text-fg">{hint}</div>}
      {j.started_at && !j.pruned_at && (
        <button
          type="button"
          onClick={() => onShowLog(j.traceback_offset)}
          className="relative mt-1 inline-flex items-center gap-1 text-xs font-medium underline decoration-fail/40 underline-offset-2 before:absolute before:-inset-x-2 before:-inset-y-3.5 hover:decoration-fail"
        >
          {j.traceback_offset !== undefined ? '跳到报错' : '看日志末尾'}
          <ArrowDown className="size-3.5" aria-hidden />
        </button>
      )}
    </Alert>
  )
}

function Meta({ label, children, className, live, wrap }: { label: ReactNode; children: ReactNode; className?: string; live?: boolean; wrap?: boolean }) {
  return (
    <div className={cx('min-w-0 space-y-1', className)}>
      <div className="line-clamp-2 text-xs text-fg-3">{label}</div>
      <div className={cx('flex min-h-5 min-w-0 items-center gap-x-1.5 gap-y-0.5 text-ui text-fg', wrap && 'flex-wrap')} aria-live={live ? 'polite' : undefined}>
        {children}
      </div>
    </div>
  )
}

/** 详情页顶部的一排关键信息：状态、卡、环境、时长、代码版本。手机上三列两行，日志框才能留在首屏 */
function MetaStrip({ j }: { j: Job }) {
  const d = jobDuration(j)
  const gpus = j.gpus_assigned.length ? j.gpus_assigned : j.gpus_pinned
  const exited = j.exit_code !== null && j.status !== 'succeeded'
  return (
    <Card className="grid grid-cols-3 gap-x-3 gap-y-2.5 px-3 py-2.5 sm:gap-x-4 sm:gap-y-3 sm:px-4 sm:py-3 lg:grid-cols-[repeat(6,auto)] lg:justify-between">
      {/* 手机上一格只有 100px 上下，"失败"后面放不下退出码，会多折出一行；写到标签里 */}
      <Meta
        label={
          <>
            状态
            {exited && <span className="sm:hidden"> · 退出码 {j.exit_code}</span>}
          </>
        }
        live
        wrap
      >
        <StatusLabel job={j} />
        {exited && <span className="text-fg-3 max-sm:hidden">退出码 {j.exit_code}</span>}
      </Meta>
      <Meta label="GPU" wrap>
        {gpus.length ? <GpuChips ids={gpus} className="flex-wrap" /> : <span className="text-fg-2">{j.gpus_req ? `${j.gpus_req} 张，未分配` : '不用 GPU'}</span>}
      </Meta>
      <Meta label="conda 环境">
        <span className="truncate">{j.conda_env || <span className="text-fg-3">不使用</span>}</span>
      </Meta>
      <Meta label={j.status === 'running' ? '已运行' : j.status === 'queued' ? '已等待' : '耗时'}>
        {j.status === 'queued' ? duration(now() - j.created_at) : d !== null ? duration(d) : <span className="text-fg-3">-</span>}
      </Meta>
      <Meta
        label={
          // 手机上一列只有 100px 上下，写全了会折成两行，把日志框往下挤；写短，琥珀字仍然说明圆点的意思
          <>
            <span className="sm:hidden">代码</span>
            <span className="max-sm:hidden">代码版本</span>
            {j.git_dirty && (
              <span className="text-warn">
                {' · '}
                <span className="sm:hidden">有改动</span>
                <span className="max-sm:hidden">有未提交改动</span>
              </span>
            )}
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

/**
 * 复制到剪贴板，结果如实反馈：浏览器只在 HTTPS（或本机地址）下开放剪贴板，tls = off 时用 IP 打开面板就复制不了；
 * 开放了也可能被拒绝（例如页面没有焦点）。失败时不能照样显示对勾。结果也读给读屏听。
 */
function useCopy(text: string) {
  const [state, setState] = useState<'idle' | 'ok' | 'fail'>('idle')
  const [why, setWhy] = useState('')
  const timer = useRef<number>(undefined)
  const copy = async () => {
    window.clearTimeout(timer.current)
    if (!navigator.clipboard) {
      setWhy('浏览器只在 HTTPS 下允许复制')
      setState('fail')
    } else {
      try {
        await navigator.clipboard.writeText(text)
        setState('ok')
      } catch {
        setWhy('浏览器拒绝了复制')
        setState('fail')
      }
    }
    timer.current = window.setTimeout(() => setState('idle'), 3000)
  }
  const icon = state === 'ok' ? <Check className="size-4" aria-hidden /> : state === 'fail' ? <X className="size-4" aria-hidden /> : <Copy className="size-4" aria-hidden />
  const status = (
    <span role="status" className="sr-only">
      {state === 'ok' ? '已复制' : state === 'fail' ? `复制失败，${why}` : ''}
    </span>
  )
  const title = state === 'fail' ? `复制失败：${why}，请手动选中` : undefined
  return { state, copy, icon, status, title }
}

function CopyBlock({ text }: { text: string }) {
  const c = useCopy(text)
  return (
    <div className="group relative">
      <pre className="term overflow-x-auto rounded-md p-3 pr-10 whitespace-pre-wrap break-all">{text}</pre>
      <button
        type="button"
        className={cx('absolute right-1.5 top-1.5 grid size-7 place-items-center rounded hover:bg-white/10', c.state === 'fail' ? 'text-[#ff7b72]' : 'text-[#8f8f8f] hover:text-white')}
        onClick={c.copy}
        aria-label="复制命令"
        title={c.title ?? '复制'}
      >
        {c.icon}
      </button>
      {c.status}
    </div>
  )
}

function CopyValue({ text }: { text: string }) {
  const c = useCopy(text)
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="min-w-0 break-all font-mono text-[12.5px]">{text}</span>
      <button
        type="button"
        className={cx('grid size-7 shrink-0 place-items-center rounded hover:bg-hover', c.state === 'fail' ? 'text-fail' : 'text-fg-3 hover:text-fg')}
        onClick={c.copy}
        aria-label="复制输出目录"
        title={c.title ?? '复制输出目录'}
      >
        {c.icon}
      </button>
      {c.status}
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
        {j.started_at && (
          <Row label="环境包">
            {j.recipe?.env_hash ? (
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span className="font-mono text-xs" title={j.recipe.env_hash}>{j.recipe.env_hash.slice(0, 12)}</span>
                <a href={`/api/jobs/${j.id}/env`} target="_blank" rel="noreferrer" className={link}>查看包列表</a>
              </div>
            ) : j.recipe?.pending ? (
              <span className="text-fg-3">正在记录</span>
            ) : j.recipe?.env_error ? (
              <span className="text-warn">记录失败：{j.recipe.env_error}</span>
            ) : (
              <span className="text-fg-3">没有记录</span>
            )}
          </Row>
        )}
        {j.recipe?.machine && (
          <Row label="运行环境">
            <div className="space-y-1">
              <div>
                {j.recipe.machine.hostname || '主机信息未记录'}
                {j.recipe.machine.driver_version && <> · 驱动 {j.recipe.machine.driver_version}</>}
                {j.recipe.machine.cuda_version && <> · CUDA {j.recipe.machine.cuda_version}</>}
              </div>
              {j.recipe.machine.gpus?.map((gpu) => (
                <div key={gpu.index} className="font-mono text-xs">
                  GPU {gpu.index}{gpu.name ? ` · ${gpu.name}` : ''}{gpu.uuid ? ` · ${gpu.uuid}` : ''}
                </div>
              ))}
              {j.recipe.machine_error && <div className="text-warn">{j.recipe.machine_error}</div>}
            </div>
          </Row>
        )}
        {j.output_dir && (
          <Row label="输出目录">
            <CopyValue text={j.output_dir} />
          </Row>
        )}
        <Row label="GPU">
          申请 {j.gpus_pinned.length ? `指定卡 ${j.gpus_pinned.join(', ')}` : `${j.gpus_req} 张`}
          {j.gpus_assigned.length > 0 && <>，分到 GPU {j.gpus_assigned.join(', ')}</>}
        </Row>
        <Row label="资源"><ProfileSummary job={j} showHint /></Row>
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
        {j.code_from && (
          <Row label="代码来源">
            <Link to={`/jobs/${j.code_from}?tab=code`} className={link}>任务 #{j.code_from} 的快照</Link>
            {j.snapshot_ok && j.snapshot_note && (
              <details className="mt-1 text-fg-2">
                <summary className="cursor-pointer text-ui">查看使用现有文件的情况</summary>
                <p className="mt-1 text-xs [overflow-wrap:anywhere]">{j.snapshot_note}</p>
              </details>
            )}
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
