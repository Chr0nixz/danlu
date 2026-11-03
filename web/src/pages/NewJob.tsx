import { useMutation, useQuery } from '@tanstack/react-query'
import { Check, FolderOpen, History, Minus, Plus } from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { gpuBusy, useGpus } from '../components/GpuGrid'
import { Alert, Button, Card, cx, ErrorBox, FieldError, FormRow, Input, Kbd, Segmented, Select, Skeleton, Textarea } from '../components/ui'
import { failText, type GPU, type Job } from '@danlu/core'
import { api } from '../lib/api'

type JobFormMode = 'new' | 'copy' | 'rerun'

/** 和代码无关的失败分类：重跑时默认用原任务的快照 */
const sameCodeKinds = ['reboot', 'killed', 'signal', 'nccl']

function rerunName(name: string) {
  const m = name.match(/^(.*)-r(\d+)$/)
  return m ? `${m[1]}-r${Number(m[2]) + 1}` : `${name}-r2`
}

function parseEnv(text: string): Record<string, string> {
  const env: Record<string, string> = {}
  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const i = line.indexOf('=')
    if (i <= 0) throw new Error(`格式应为 KEY=VALUE：${line}`)
    const k = line.slice(0, i).trim()
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(k)) throw new Error(`变量名只能用字母、数字和下划线，且不能以数字开头：${k}`)
    env[k] = line.slice(i + 1)
  }
  return env
}

export function NewJob() {
  const [params] = useSearchParams()
  const copyId = Number(params.get('copy')) || null
  const sourceId = copyId ?? (Number(params.get('from')) || null)
  const formMode: JobFormMode = copyId ? 'copy' : sourceId ? 'rerun' : 'new'
  const source = useQuery({ queryKey: ['job', sourceId], queryFn: () => api.job(sourceId!), enabled: !!sourceId })
  if (sourceId && source.isLoading) {
    return (
      <div className="mx-auto max-w-3xl space-y-4">
        <Skeleton className="h-7 w-48" />
        <Skeleton className="h-[480px] w-full rounded-lg" />
      </div>
    )
  }
  if (sourceId && source.error) return <ErrorBox error={source.error} />
  return <JobForm key={`${formMode}-${sourceId ?? ''}`} source={source.data ?? null} formMode={formMode} />
}

type Field = 'workdir' | 'cmd' | 'gpu' | 'env'

function JobForm({ source, formMode }: { source: Job | null; formMode: JobFormMode }) {
  const nav = useNavigate()
  const gpus = useGpus()
  const conda = useQuery({ queryKey: ['conda'], queryFn: api.condaEnvs, staleTime: 60_000 })
  const recent = useQuery({ queryKey: ['recentWorkdirs'], queryFn: api.recentWorkdirs })
  const formRef = useRef<HTMLFormElement>(null)
  const uid = useId()
  const ids = { workdir: `${uid}-workdir`, cmd: `${uid}-cmd`, conda: `${uid}-conda`, gpu: `${uid}-gpu`, name: `${uid}-name`, env: `${uid}-env` }

  const rerun = formMode === 'rerun'
  const canRestore = !!source?.snapshot_ok && !source.pruned_at && (source.pid > 0 || source.ended_at !== null)
  // 被重启、外部终止这类和代码无关的失败，默认用原任务的快照原样重跑；其余默认用工作目录现在的代码（多半刚改过）
  const [useSnapshot, setUseSnapshot] = useState(() => rerun && canRestore && sameCodeKinds.includes(source?.fail_kind ?? ''))
  const snapshotSelected = rerun && useSnapshot
  const [name, setName] = useState(source && rerun ? rerunName(source.name) : source?.name ?? '')
  const [workdir, setWorkdir] = useState(source?.workdir ?? '')
  const activeWorkdir = snapshotSelected ? source!.workdir : workdir
  const [condaEnv, setCondaEnv] = useState(source?.conda_env ?? '')
  const [cmd, setCmd] = useState(source?.cmd ?? '')
  const [mode, setMode] = useState<'count' | 'pinned'>(source?.gpus_pinned.length ? 'pinned' : 'count')
  const [count, setCount] = useState(source?.gpus_req ?? 1)
  const [pinned, setPinned] = useState<number[]>(source?.gpus_pinned ?? [])
  const [envText, setEnvText] = useState(source ? Object.entries(source.env).map(([k, v]) => `${k}=${v}`).join('\n') : '')
  const [inheritBashrc, setInheritBashrc] = useState(source?.inherit_bashrc ?? false)
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({})

  // 改了哪个字段就清掉哪个字段的错误
  const edit =
    <T,>(field: Field, set: (v: T) => void) =>
    (v: T) => {
      set(v)
      if (errors[field]) setErrors((e) => ({ ...e, [field]: undefined }))
    }

  // 没选过 conda 环境时，默认选第一个非 base 的环境
  useEffect(() => {
    if (!source && !condaEnv && conda.data?.envs.length) {
      setCondaEnv(conda.data.envs.find((e) => e.name !== 'base')?.name ?? conda.data.envs[0].name)
    }
  }, [conda.data, source, condaEnv])

  const submit = useMutation({
    mutationFn: api.submit,
    onSuccess: (j) => nav(`/jobs/${j.id}`),
  })

  const gpuList = gpus.data?.gpus ?? []
  const total = gpuList.length
  const freeCount = gpuList.filter((g) => !gpuBusy(g)).length

  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    if (submit.isPending || (snapshotSelected && !canRestore)) return
    const errs: Partial<Record<Field, string>> = {}
    if (!activeWorkdir.trim()) errs.workdir = '请填写工作目录'
    if (!cmd.trim()) errs.cmd = '请填写要运行的命令'
    if (mode === 'pinned' && !pinned.length) errs.gpu = '请至少选择一张卡'
    let env: Record<string, string> = {}
    try {
      env = parseEnv(envText)
    } catch (err) {
      errs.env = (err as Error).message
    }
    setErrors(errs)
    // 焦点移到第一个出错的字段，读屏会读出它的错误说明
    const first = (['workdir', 'cmd', 'gpu', 'env'] as Field[]).find((f) => errs[f])
    if (first) {
      document.getElementById(ids[first])?.focus()
      return
    }
    submit.mutate({
      name: name.trim(),
      workdir: activeWorkdir.trim().replace(/\/+$/, '') || '/',
      conda_env: condaEnv,
      cmd,
      env,
      inherit_bashrc: inheritBashrc,
      gpus: mode === 'count' ? count : pinned.length,
      pinned_gpus: mode === 'pinned' ? pinned : [],
      rerun_of: rerun ? source?.id ?? null : null,
      code_from: snapshotSelected ? source!.id : undefined,
    })
  }

  const need = mode === 'count' ? count : pinned.length
  const pick = (i: number) => gpuList.find((g) => g.index === i)
  // 指定了被外部进程占着的卡：丹炉永远不会分配它，严格先来先跑下这个任务会一直堵在队首
  const pinnedExternal = mode === 'pinned' ? pinned.filter((i) => pick(i)?.external.length) : []
  const pinnedBusy = mode === 'pinned' && pinned.some((i) => {
    const g = pick(i)
    return g && gpuBusy(g)
  })
  const willQueue = need > 0 && (mode === 'count' ? need > freeCount : pinnedBusy)
  const condaHint = conda.data && !conda.data.available ? conda.data.error : conda.data?.error || '任务启动前自动激活。'
  const describe = (f: Field, hint = true) => cx(hint && `${ids[f]}-hint`, errors[f] && `${ids[f]}-err`) || undefined

  return (
    <form
      ref={formRef}
      noValidate
      onSubmit={onSubmit}
      onKeyDown={(e) => {
        // Ctrl/⌘ + Enter 提交
        if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) formRef.current?.requestSubmit()
      }}
      className="mx-auto max-w-3xl space-y-4"
    >
      <div>
        <h1 className="text-lg font-semibold">{source ? `${rerun ? '重跑' : '复制'}任务 #${source.id}` : '新建任务'}</h1>
        <p className="mt-0.5 text-ui text-fg-3">
          {source
            ? `设置已从「${source.name}」复制过来，${rerun ? '可以先改参数再提交。' : '修改后提交为独立的新任务。'}`
            : '提交后按顺序排队，有足够的空卡就自动开始。'}
        </p>
        {rerun && source?.status === 'failed' && (
          <p className="mt-1 text-ui text-fail [overflow-wrap:anywhere]">
            原任务失败：<span className="font-mono text-[12.5px]">{failText(source)}</span>
          </p>
        )}
      </div>
      {rerun && source && !snapshotSelected && <CodeChangeNotice source={source} workdir={workdir} />}

      <Card className="divide-y divide-line">
        {rerun && source && (
          <FormRow
            label="代码版本"
            hint={snapshotSelected && !canRestore ? '快照已不可用。请选择现在的工作目录再提交。' : snapshotSelected
              ? '在独立目录恢复代码。未收进快照的文件可能来自原目录，命令中写死的绝对路径也仍指向原处。conda 使用当前环境。'
              : source.pruned_at ? '源任务的快照已清理。' : !canRestore ? '源任务的快照还不可用。' : '可改用原任务的快照，再调整这次运行的参数。'}
          >
            <CodeSourcePicker name={`${uid}-code`} source={source} workdir={workdir} canRestore={canRestore} snapshot={snapshotSelected} onChange={setUseSnapshot} />
          </FormRow>
        )}
        <FormRow label={snapshotSelected ? '原工作目录' : '工作目录'} hint={snapshotSelected ? '从这里补齐数据和权重。命令在恢复后的目录中执行。' : '服务器上的绝对路径，输入或按 ↓ 列出子目录。启动时会自动保存一份代码快照。'} htmlFor={ids.workdir}>
          {snapshotSelected ? (
            <Input id={ids.workdir} aria-describedby={`${ids.workdir}-hint`} value={activeWorkdir} readOnly className="font-mono" />
          ) : <WorkdirInput
            id={ids.workdir}
            describedBy={describe('workdir')}
            invalid={!!errors.workdir}
            value={workdir}
            onChange={edit('workdir', setWorkdir)}
            recent={recent.data?.dirs ?? []}
          />}
          <FieldError id={`${ids.workdir}-err`}>{errors.workdir}</FieldError>
        </FormRow>

        <FormRow label="命令" hint={snapshotSelected
          ? '用 bash 在恢复后的目录中执行，可以写多行。torchrun 的端口会自动分配。'
          : '在工作目录下用 bash 执行，可以写多行。torchrun 不用写 --master_port，会自动分配。'} htmlFor={ids.cmd}>
          <Textarea
            id={ids.cmd}
            aria-describedby={describe('cmd')}
            aria-invalid={!!errors.cmd || undefined}
            value={cmd}
            onChange={(e) => edit('cmd', setCmd)(e.target.value)}
            rows={4}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            placeholder="例如 torchrun --nproc_per_node 2 train.py --lr 3e-4"
            className="font-mono text-[12.5px]"
          />
          <FieldError id={`${ids.cmd}-err`}>{errors.cmd}</FieldError>
        </FormRow>

        <FormRow label="conda 环境" hint={condaHint} htmlFor={ids.conda}>
          <Select id={ids.conda} aria-describedby={`${ids.conda}-hint`} value={condaEnv} onChange={(e) => setCondaEnv(e.target.value)} disabled={conda.isLoading}>
            <option value="">不使用 conda</option>
            {conda.data?.envs.map((e) => (
              <option key={e.path} value={e.name}>
                {e.name}
              </option>
            ))}
            {condaEnv && !conda.data?.envs.some((e) => e.name === condaEnv) && <option value={condaEnv}>{condaEnv}</option>}
          </Select>
        </FormRow>

        <FormRow label="GPU" hint={`共 ${total} 张，现在空闲 ${freeCount} 张。整卡独占。`} describedBy={errors.gpu && `${ids.gpu}-err`}>
          <div className="space-y-3">
            <Segmented
              label="GPU 分配方式"
              value={mode}
              onChange={edit('gpu', setMode)}
              options={[
                { value: 'count', label: '按张数' },
                { value: 'pinned', label: '指定卡号' },
              ]}
            />
            {mode === 'count' ? (
              <div className="flex items-center gap-3">
                <div role="group" aria-label="GPU 张数" className="inline-flex h-9 items-stretch overflow-hidden rounded-md border border-line-strong">
                  <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40" onClick={() => setCount((c) => Math.max(0, c - 1))} disabled={count <= 0} aria-label="少一张">
                    <Minus className="size-4" aria-hidden />
                  </button>
                  <output aria-live="polite" className="grid w-10 place-items-center border-x border-line-strong text-sm font-semibold">
                    {count}
                  </output>
                  <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40" onClick={() => setCount((c) => Math.min(total || 64, c + 1))} disabled={total > 0 && count >= total} aria-label="多一张">
                    <Plus className="size-4" aria-hidden />
                  </button>
                </div>
                <span className="text-ui text-fg-2">{count === 0 ? '不使用 GPU' : '张卡，自动挑空闲的'}</span>
              </div>
            ) : (
              <GpuPicker id={ids.gpu} describedBy={errors.gpu && `${ids.gpu}-err`} gpus={gpuList} value={pinned} onChange={edit('gpu', setPinned)} />
            )}
            <FieldError id={`${ids.gpu}-err`}>{errors.gpu}</FieldError>
            {pinnedExternal.length > 0 ? (
              <p className="text-xs leading-relaxed text-warn">
                GPU {pinnedExternal.join(', ')} 被外部进程占用，丹炉不会分配它。任务会一直排在队首、挡住后面所有任务，直到那个进程退出。
              </p>
            ) : (
              willQueue && <p className="text-xs leading-relaxed text-warn">现在空闲的卡不够，提交后会排队，等卡空出来自动开始。</p>
            )}
          </div>
        </FormRow>

        <FormRow label="任务名" hint="留空则用目录名加时间。" htmlFor={ids.name}>
          <Input id={ids.name} aria-describedby={`${ids.name}-hint`} value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 vit-lr3e-4" maxLength={100} />
        </FormRow>

        <FormRow label="环境变量" hint="可选，每行一个 KEY=VALUE。" htmlFor={ids.env}>
          <Textarea
            id={ids.env}
            aria-describedby={describe('env')}
            aria-invalid={!!errors.env || undefined}
            className="font-mono text-[12.5px]"
            rows={2}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            value={envText}
            onChange={(e) => edit('env', setEnvText)(e.target.value)}
            placeholder={'例如 WANDB_PROJECT=vit\nOMP_NUM_THREADS=8'}
          />
          <FieldError id={`${ids.env}-err`}>{errors.env}</FieldError>
        </FormRow>

        <FormRow label="Bash 环境" hint="缺失或加载超时会启动失败">
          <label className="inline-flex min-h-8 items-center gap-2 text-ui text-fg-2">
            <input
              type="checkbox"
              checked={inheritBashrc}
              onChange={(e) => setInheritBashrc(e.target.checked)}
              className="size-4 accent-fg"
            />
            加载服务器 ~/.bashrc 导出的变量
          </label>
        </FormRow>

        {/* 手机上提交栏吸在底部导航上方，长表单不用滚到底才能提交 */}
        <div className="flex flex-col-reverse gap-3 rounded-b-lg bg-subtle px-4 py-3 max-sm:sticky max-sm:bottom-[calc(3.5rem+env(safe-area-inset-bottom))] max-sm:z-10 sm:flex-row sm:items-center sm:px-5">
          <ErrorBox error={submit.error} className="sm:mr-auto" />
          <div className="flex gap-2 sm:ml-auto">
            <Button onClick={() => nav(-1)}>返回</Button>
            <Button type="submit" variant="primary" loading={submit.isPending} disabled={submit.isPending || (snapshotSelected && !canRestore)} className="flex-1 sm:flex-none">
              提交任务
              <Kbd>Ctrl ↵</Kbd>
            </Button>
          </div>
        </div>
      </Card>
    </form>
  )
}

function GpuPicker({ id, describedBy, gpus, value, onChange }: { id: string; describedBy?: string; gpus: GPU[]; value: number[]; onChange: (v: number[]) => void }) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {gpus.map((g, n) => {
        const on = value.includes(g.index)
        const state = g.job ? `#${g.job.id} 在用` : g.external.length ? '外部进程' : '空闲'
        const tone = g.job ? 'text-run' : g.external.length ? 'text-warn' : 'text-ok'
        return (
          <button
            type="button"
            key={g.index}
            // 第一张卡带上字段 id，校验失败时焦点落在这里，并读出错误说明
            id={n === 0 ? id : undefined}
            aria-describedby={n === 0 ? describedBy : undefined}
            aria-pressed={on}
            onClick={() => onChange(on ? value.filter((x) => x !== g.index) : [...value, g.index].sort((a, b) => a - b))}
            className={cx(
              'relative rounded-md border px-3 py-2 text-left transition-colors duration-150',
              on ? 'border-fg bg-subtle ring-1 ring-fg' : 'border-line-strong hover:bg-subtle',
            )}
          >
            <div className="flex items-center justify-between">
              <span className="text-ui font-semibold">GPU {g.index}</span>
              {on && <Check className="size-3.5" strokeWidth={3} aria-hidden />}
            </div>
            <div className={cx('mt-0.5 truncate text-xs', tone)}>
              {state}
              <span className="text-fg-3"> · {g.util}%</span>
            </div>
          </button>
        )
      })}
    </div>
  )
}

/**
 * 重跑用哪份代码：两张单选卡片，各自写出 commit 和有没有未提交改动，选之前就知道两边差在哪。
 * 用原生单选框，方向键切换和禁用都交给浏览器
 */
function CodeSourcePicker({ name, source, workdir, canRestore, snapshot, onChange }: { name: string; source: Job; workdir: string; canRestore: boolean; snapshot: boolean; onChange: (snapshot: boolean) => void }) {
  const head = useQuery({ queryKey: ['githead', workdir], queryFn: () => api.gitHead(workdir), enabled: !!workdir })
  const version = (branch: string | undefined, commit: string | undefined, dirty: boolean | undefined) => (
    <>
      {branch && `${branch} @ `}
      <span className="font-mono">{commit?.slice(0, 7)}</span>
      {dirty && <span className="text-warn"> · 有未提交改动</span>}
    </>
  )
  const current = !workdir ? '先填工作目录' : head.isLoading ? '正在读取…' : head.data?.is_git && head.data.commit ? version(head.data.branch, head.data.commit, head.data.dirty) : '不是 git 仓库'
  const snap = source.pruned_at ? '已清理' : !canRestore ? '还不可用' : source.git_commit ? version(source.git_branch, source.git_commit, source.git_dirty) : '非 git，已存快照'
  const options = [
    { value: false, title: '现在的工作目录', detail: current, disabled: false },
    { value: true, title: `任务 #${source.id} 的快照`, detail: snap, disabled: !canRestore },
  ]
  return (
    <div className="grid grid-cols-2 gap-2">
      {options.map((o) => (
        <label
          key={String(o.value)}
          className={cx(
            'relative min-w-0 cursor-pointer rounded-md border px-3 py-2 transition-colors duration-150',
            'has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-run/70 has-disabled:cursor-not-allowed has-disabled:opacity-45',
            snapshot === o.value ? 'border-fg bg-subtle ring-1 ring-fg' : 'border-line-strong hover:bg-subtle',
          )}
        >
          <input type="radio" name={name} className="sr-only" checked={snapshot === o.value} disabled={o.disabled} onChange={() => onChange(o.value)} />
          <span className="flex items-center justify-between gap-2">
            <span className="truncate text-ui font-semibold">{o.title}</span>
            {snapshot === o.value && <Check className="size-3.5 shrink-0" strokeWidth={3} aria-hidden />}
          </span>
          <span className="mt-0.5 line-clamp-2 text-xs text-fg-3 [overflow-wrap:anywhere]">{o.detail}</span>
        </label>
      ))}
    </div>
  )
}

/** 重跑时比较工作目录当前代码和原任务的快照，说清楚会用哪份代码 */
function CodeChangeNotice({ source, workdir }: { source: Job; workdir: string }) {
  const head = useQuery({ queryKey: ['githead', workdir], queryFn: () => api.gitHead(workdir), enabled: !!workdir })
  if (!head.data) return null
  const code = (
    <Link to={`/jobs/${source.id}?tab=code`} className="font-medium underline decoration-current/40 underline-offset-2 hover:decoration-current">
      原任务的代码快照
    </Link>
  )
  if (!head.data.is_git || !source.git_commit) {
    return (
      <Alert>
        工作目录不是 git 仓库，没法比较代码。重跑会使用目录里现在的文件；{code}可以查看和下载。
      </Alert>
    )
  }
  const msgs: string[] = []
  if (head.data.commit !== source.git_commit) msgs.push(`当前 commit 是 ${head.data.commit?.slice(0, 8)}，原任务是 ${source.git_commit.slice(0, 8)}`)
  if (head.data.dirty) msgs.push('工作目录有未提交的改动，看不出和原任务运行时是否一样')
  else if (source.git_dirty) msgs.push('原任务运行时有未提交的改动，现在工作目录是干净的')
  if (!msgs.length) {
    return (
      <Alert>
        工作目录现在的代码和原任务相同：commit {source.git_commit.slice(0, 8)}，都没有未提交的改动。
      </Alert>
    )
  }
  return (
    <Alert tone="warn">
      重跑会使用工作目录<b>现在</b>的代码，不是原任务快照里的那份：
      <ul className="mt-1 list-disc pl-4">
        {msgs.map((m) => (
          <li key={m}>{m}</li>
        ))}
      </ul>
      {code}可以查看和下载。
    </Alert>
  )
}

function WorkdirInput({
  id,
  describedBy,
  invalid,
  value,
  onChange,
  recent,
}: {
  id: string
  describedBy?: string
  invalid: boolean
  value: string
  onChange: (v: string) => void
  recent: string[]
}) {
  const [focus, setFocus] = useState(false)
  // 聚焦时先不展开：校验失败时焦点会移到这里，展开的列表会盖住错误说明。输入或按 ↓ 时展开，按 Esc 收起
  const [closed, setClosed] = useState(true)
  const [active, setActive] = useState(-1)
  const [query, setQuery] = useState(value)
  const listId = useId()
  const timer = useRef<number>(undefined)
  useEffect(() => {
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setQuery(value), 200)
  }, [value])
  const dirs = useQuery({ queryKey: ['dirs', query], queryFn: () => api.dirs(query), enabled: focus })
  const options = useMemo(() => (dirs.data?.dirs ?? []).filter((d) => d !== value), [dirs.data, value])
  const open = focus && !closed && options.length > 0
  useEffect(() => setActive(-1), [options])
  useEffect(() => {
    if (active >= 0) document.getElementById(`${listId}-${active}`)?.scrollIntoView({ block: 'nearest' })
  }, [active, listId])

  const choose = (d: string) => {
    onChange(d + '/')
    setActive(-1)
  }

  return (
    <div className="space-y-2">
      <div className="relative">
        <FolderOpen className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-fg-3" aria-hidden />
        <Input
          id={id}
          role="combobox"
          aria-expanded={open}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={open && active >= 0 ? `${listId}-${active}` : undefined}
          aria-describedby={describedBy}
          aria-invalid={invalid || undefined}
          value={value}
          onChange={(e) => {
            onChange(e.target.value)
            setClosed(false)
          }}
          onFocus={() => setFocus(true)}
          onBlur={() => setTimeout(() => {
            setFocus(false)
            setClosed(true)
          }, 150)}
          onKeyDown={(e) => {
            if (!open) {
              if (e.key === 'ArrowDown' && options.length) {
                e.preventDefault()
                setClosed(false)
              }
              return
            }
            const n = options.length
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
              e.preventDefault()
              setActive((a) => (e.key === 'ArrowDown' ? (a + 1) % n : (a - 1 + n) % n))
            } else if (e.key === 'Enter' && active >= 0 && !e.ctrlKey && !e.metaKey) {
              e.preventDefault()
              choose(options[active])
            } else if (e.key === 'Escape') {
              e.preventDefault()
              setClosed(true)
            }
          }}
          placeholder="例如 /home/you/projects/vit"
          className="pl-8 font-mono text-[12.5px]"
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
        />
        {open && (
          <div
            id={listId}
            role="listbox"
            aria-label="子目录"
            className="absolute inset-x-0 top-full z-20 mt-1 max-h-64 overflow-auto rounded-md border border-line bg-surface py-1 shadow-[0_8px_24px_rgb(0_0_0/0.12)]"
          >
            {options.map((d, i) => (
              <div
                key={d}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => choose(d)}
                className={cx(
                  'block w-full cursor-default truncate px-3 py-1.5 text-left font-mono text-[12.5px]',
                  i === active ? 'bg-hover text-fg' : 'text-fg-2 hover:bg-hover hover:text-fg',
                )}
              >
                {d}/
              </div>
            ))}
          </div>
        )}
      </div>
      {recent.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <History className="size-3.5 text-fg-3" aria-label="最近用过" />
          {recent.slice(0, 6).map((d) => (
            <button
              type="button"
              key={d}
              onClick={() => onChange(d)}
              className="h-6 max-w-full truncate rounded border border-line bg-subtle px-1.5 font-mono text-[11.5px] text-fg-2 hover:border-line-strong hover:text-fg max-sm:h-9 max-sm:px-2"
              title={d}
            >
              {d.split('/').filter(Boolean).slice(-2).join('/')}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
