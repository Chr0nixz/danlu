import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, FolderOpen, History, Minus, Plus } from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { gpuBusy, useGpus } from '../components/GpuGrid'
import { GpuPicker } from '../components/GpuPicker'
import { modKey } from '../components/Shortcuts'
import { Alert, Button, Card, cx, ErrorBox, FieldError, FormRow, Input, Kbd, Segmented, Select, Skeleton, Textarea } from '../components/ui'
import { apiErrorText, failText, sameCodeFailKinds, type Job, type Prefs, type SubmitReq } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { usePrefs } from '../lib/prefs'

// edit：修改排队中的任务（/new?edit=<id>），保存后原地更新，编号和排队位置都不变（FEATURES 3.1）
type JobFormMode = 'new' | 'copy' | 'rerun' | 'edit'

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
    if (i <= 0) throw new Error(t('newjob.env_format', { line }))
    const k = line.slice(0, i).trim()
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(k)) throw new Error(t('newjob.env_name_invalid', { name: k }))
    env[k] = line.slice(i + 1)
  }
  return env
}

/**
 * 没提交的表单内容存在 sessionStorage 里：手机浏览器回收标签页、误点了别处或刷新之后，回来还在。
 * 只存和默认值不一样的内容；提交成功或点"重新填写"后清掉；关掉标签页就没有了。
 */
interface Draft {
  name: string
  memo: string
  workdir: string
  condaEnv: string
  cmd: string
  mode: 'count' | 'pinned'
  count: number
  pinned: number[]
  envText: string
  inheritBashrc: boolean
  useSnapshot: boolean
}

/**
 * 命令自己选卡的常见写法：直接给 CUDA_VISIBLE_DEVICES 赋值（引用变量本身的不算），
 * 或 deepspeed --num_gpus（它会忽略 CUDA_VISIBLE_DEVICES，从 0 号卡开始用）。返回命中的写法
 */
function cmdPicksGpus(cmd: string): string | null {
  if (/\bCUDA_VISIBLE_DEVICES\s*=\s*(?![\x22\x27]?\$)/.test(cmd)) return 'CUDA_VISIBLE_DEVICES='
  const ds = /\bdeepspeed\b[^\n]*?(--num[_-]gpus)\b/.exec(cmd)
  return ds ? `deepspeed ${ds[1]}` : null
}

/**
 * 命令第一行开头的 Python 类启动器（python、torchrun、accelerate、deepspeed、pip），跳过前面的 FOO=bar 赋值。
 * 不选 conda 环境时它们只在系统 PATH 里找：找不到就是退出码 127，找到了也常是系统自带的 Python。
 */
function cmdLauncher(cmd: string): string | null {
  const first = cmd.split('\n').map((l) => l.trim()).find((l) => l && !l.startsWith('#')) ?? ''
  const m = /^(?:[A-Za-z_]\w*=\S*\s+)*(python[\d.]*|torchrun|accelerate|deepspeed|pip[\d.]*)(?=\s|$)/.exec(first)
  return m ? m[1] : null
}

function readDraft(key: string): Partial<Draft> | null {
  try {
    const raw = sessionStorage.getItem(key)
    const draft: unknown = raw ? JSON.parse(raw) : null
    return draft && typeof draft === 'object' ? (draft as Partial<Draft>) : null
  } catch {
    return null
  }
}

function writeDraft(key: string, draft: Draft | null) {
  try {
    if (draft) sessionStorage.setItem(key, JSON.stringify(draft))
    else sessionStorage.removeItem(key)
  } catch {
    // 存储不可用时只是不保留草稿
  }
}

export function NewJob() {
  const [params] = useSearchParams()
  const [generation, setGeneration] = useState(0)
  const editId = Number(params.get('edit')) || null
  const copyId = Number(params.get('copy')) || null
  const sourceId = editId ?? copyId ?? (Number(params.get('from')) || null)
  const formMode: JobFormMode = editId ? 'edit' : copyId ? 'copy' : sourceId ? 'rerun' : 'new'
  const source = useQuery({ queryKey: ['job', sourceId], queryFn: () => api.job(sourceId!), enabled: !!sourceId })
  // 表单的初始值来自面板偏好：等它到了再挂载，草稿才能和"刚打开时"正确比较
  const { prefs, ready } = usePrefs()
  if ((sourceId && source.isLoading) || !ready) {
    return (
      <div className="mx-auto max-w-3xl space-y-4">
        <Skeleton className="h-7 w-48" />
        <Skeleton className="h-[480px] w-full rounded-lg" />
      </div>
    )
  }
  // 窗口重新获得焦点时会重取源任务，失败了也不能把填了一半的表单换成报错
  if (sourceId && source.error && !source.data) return <ErrorBox error={source.error} />
  return <JobForm key={`${formMode}-${sourceId ?? ''}-${generation}`} source={source.data ?? null} formMode={formMode} prefs={prefs} onReset={() => setGeneration((g) => g + 1)} />
}

type Field = 'workdir' | 'cmd' | 'gpu' | 'memo' | 'env'

function JobForm({ source, formMode, prefs, onReset }: { source: Job | null; formMode: JobFormMode; prefs: Prefs; onReset(): void }) {
  const nav = useNavigate()
  const qc = useQueryClient()
  const gpus = useGpus()
  const conda = useQuery({ queryKey: ['conda'], queryFn: api.condaEnvs, staleTime: 60_000 })
  const recent = useQuery({ queryKey: ['recentWorkdirs'], queryFn: api.recentWorkdirs })
  const formRef = useRef<HTMLFormElement>(null)
  const uid = useId()
  const ids = { workdir: `${uid}-workdir`, cmd: `${uid}-cmd`, conda: `${uid}-conda`, gpu: `${uid}-gpu`, name: `${uid}-name`, memo: `${uid}-memo`, env: `${uid}-env`, notifyOff: `${uid}-notify-off` }

  const rerun = formMode === 'rerun'
  const editing = formMode === 'edit' && !!source
  // 修改排队任务时已经开始或结束了：表单照样显示，但不能保存
  const editClosed = editing && source.status !== 'queued'
  const canRestore = !!source?.snapshot_ok && !source.pruned_at && (source.pid > 0 || source.ended_at !== null)
  const draftKey = `danlu.draft.${formMode}.${source?.id ?? ''}`
  const [saved] = useState(() => readDraft(draftKey))
  // 被重启、外部终止这类和代码无关的失败，默认用原任务的快照原样重跑；其余默认用工作目录现在的代码（多半刚改过）
  const [useSnapshot, setUseSnapshot] = useState(() => saved?.useSnapshot ?? (rerun && canRestore && sameCodeFailKinds.includes(source?.fail_kind ?? '')))
  const snapshotSelected = rerun && useSnapshot
  // 快照重跑出来的排队任务：代码来自源任务，工作目录必须和它一致，修改时不能改
  const lockedWorkdir = snapshotSelected || (editing && source.code_from != null)
  const [name, setName] = useState(saved?.name ?? (source && rerun ? rerunName(source.name) : source?.name ?? ''))
  // 备注和结束提醒说的是那一次运行：重跑和复制不带过去，修改时保留
  const [memo, setMemo] = useState(saved?.memo ?? (editing ? source.memo ?? '' : ''))
  // 默认值在「设置 → 面板偏好」里改，手机和电脑共用
  const [notifyDone, setNotifyDone] = useState(editing ? !!source.notify_done : prefs.new_job_notify_done)
  const [workdir, setWorkdir] = useState(saved?.workdir ?? source?.workdir ?? '')
  const activeWorkdir = lockedWorkdir ? source!.workdir : workdir
  const [condaEnv, setCondaEnv] = useState(saved?.condaEnv ?? source?.conda_env ?? '')
  const [cmd, setCmd] = useState(saved?.cmd ?? source?.cmd ?? '')
  const [mode, setMode] = useState<'count' | 'pinned'>(saved?.mode ?? (source?.gpus_pinned.length ? 'pinned' : 'count'))
  const [count, setCount] = useState(saved?.count ?? source?.gpus_req ?? prefs.new_job_gpus)
  const [pinned, setPinned] = useState<number[]>(saved?.pinned ?? source?.gpus_pinned ?? [])
  const [envText, setEnvText] = useState(saved?.envText ?? (source ? Object.entries(source.env).map(([k, v]) => `${k}=${v}`).join('\n') : ''))
  const [inheritBashrc, setInheritBashrc] = useState(saved?.inheritBashrc ?? source?.inherit_bashrc ?? prefs.new_job_bashrc)
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({})

  // 草稿：和刚打开时不一样才存。默认的 conda 环境是列表到了之后才选上的，不算用户改过
  const draft: Draft = { name, memo, workdir, condaEnv, cmd, mode, count, pinned, envText, inheritBashrc, useSnapshot }
  const picksGpus = cmdPicksGpus(cmd)
  // 有可选的 conda 环境却选了"不使用"，命令又是 python 这一类：提醒一句，不拦提交（.bashrc 里可能激活了别的环境）
  const launcher = !condaEnv && !inheritBashrc && conda.data?.envs.length ? cmdLauncher(cmd) : null
  // 严格限卡开着时程序只看得到分到的卡、从 0 编号，命令里自己选卡的提示要换个说法
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, staleTime: 60_000 }).data
  const strictOn = settings?.strict?.enabled ?? false
  // 服务器没开手机推送时勾"结束时推送"也收不到，勾上的时候说清楚（旧服务器没有 notify 字段，不提示）
  const pushOff = settings?.notify ? !settings.notify.enabled : false
  const draftJson = JSON.stringify(draft)
  const pristine = useRef(saved ? null : draftJson)
  const submitted = useRef(false)
  useEffect(() => {
    if (submitted.current) return
    writeDraft(draftKey, draftJson === pristine.current ? null : draft)
    // draftJson 变了才需要重存
  }, [draftKey, draftJson])

  // 改了哪个字段就清掉哪个字段的错误
  const edit =
    <T,>(field: Field, set: (v: T) => void) =>
    (v: T) => {
      set(v)
      if (errors[field]) setErrors((e) => ({ ...e, [field]: undefined }))
    }

  // 新建任务时默认选第一个非 base 的环境（面板偏好里可以关掉）。只在环境列表第一次到达时选一次，
  // 之后用户选了"不使用 conda"也要保留；恢复出来的草稿里"不使用 conda"也是用户选的，同样保留
  const condaDefaulted = useRef(!!saved || prefs.new_job_conda === 'none')
  useEffect(() => {
    const envs = conda.data?.envs
    if (source || condaDefaulted.current || !envs?.length) return
    condaDefaulted.current = true
    const pick = envs.find((e) => e.name !== 'base')?.name ?? envs[0].name
    if (condaEnv) return
    setCondaEnv(pick)
    // 从刚打开时的内容算，不能用现在的：环境列表到之前用户可能已经填了别的字段
    if (pristine.current) pristine.current = JSON.stringify({ ...(JSON.parse(pristine.current) as Draft), condaEnv: pick })
  }, [conda.data, source])

  // 用键盘进来（按 N、点重跑）时焦点直接落在第一个要填的字段上：新建是工作目录，重跑和复制多半是改命令。
  // 触屏上不自动聚焦，免得一进页面就弹出输入法
  useEffect(() => {
    if (!matchMedia('(pointer: fine)').matches) return
    document.getElementById(source ? ids.cmd : ids.workdir)?.focus({ preventScroll: true })
  }, [])

  // Ctrl/⌘ + Enter 提交，焦点不在表单里也有效
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Enter' || !(e.ctrlKey || e.metaKey) || document.querySelector('dialog[open]')) return
      e.preventDefault()
      formRef.current?.requestSubmit()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const submit = useMutation({
    mutationFn: (r: SubmitReq) => (editing ? api.editJob(source.id, r) : api.submit(r)),
    onSuccess: (j) => {
      submitted.current = true
      writeDraft(draftKey, null)
      if (editing) {
        qc.setQueryData(['job', j.id], j)
        void qc.invalidateQueries({ queryKey: ['jobs'] })
      }
      nav(`/jobs/${j.id}`)
    },
  })

  const gpuList = gpus.data?.gpus ?? []
  const total = gpuList.length
  const freeCount = gpuList.filter((g) => !gpuBusy(g)).length

  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    if (submit.isPending || (snapshotSelected && !canRestore) || editClosed) return
    const errs: Partial<Record<Field, string>> = {}
    if (!activeWorkdir.trim()) errs.workdir = t('newjob.workdir_required')
    if (!cmd.trim()) errs.cmd = t('newjob.cmd_required')
    if (mode === 'pinned' && !pinned.length) errs.gpu = t('gpus.pick_required')
    if (Array.from(memo).length > 500) errs.memo = t('newjob.memo_max')
    let env: Record<string, string> = {}
    try {
      env = parseEnv(envText)
    } catch (err) {
      errs.env = apiErrorText(err)
    }
    setErrors(errs)
    // 焦点移到第一个出错的字段，读屏会读出它的错误说明
    const first = (['workdir', 'cmd', 'gpu', 'memo', 'env'] as Field[]).find((f) => errs[f])
    if (first) {
      document.getElementById(ids[first])?.focus()
      return
    }
    submit.mutate({
      name: name.trim(),
      memo,
      notify_done: notifyDone,
      workdir: activeWorkdir.trim().replace(/\/+$/, '') || '/',
      conda_env: condaEnv,
      cmd,
      env,
      inherit_bashrc: inheritBashrc,
      gpus: mode === 'count' ? count : pinned.length,
      pinned_gpus: mode === 'pinned' ? pinned : [],
      // 修改时这两项服务器不改，原样带上
      rerun_of: editing ? source.rerun_of : rerun ? source?.id ?? null : null,
      code_from: editing ? source.code_from : snapshotSelected ? source!.id : undefined,
    })
  }

  const need = mode === 'count' ? count : pinned.length
  const pick = (i: number) => gpuList.find((g) => g.index === i)
  // 指定了被预约或外部进程占着的卡：丹炉不会分配它，严格先来先跑下这个任务会一直堵在队首
  const pinnedReserved = mode === 'pinned' ? pinned.flatMap((i) => {
    const r = pick(i)?.reservation
    return r ? [{ index: i, id: r.id }] : []
  }) : []
  const pinnedExternal = mode === 'pinned' ? pinned.filter((i) => pick(i)?.external.length && !pick(i)?.reservation) : []
  const pinnedUnknown = mode === 'pinned' ? pinned.filter((i) => (pick(i)?.unknown_mem_mb ?? 0) > 0) : []
  const pinnedBusy = mode === 'pinned' && pinned.some((i) => {
    const g = pick(i)
    return g && gpuBusy(g)
  })
  const willQueue = need > 0 && (mode === 'count' ? need > freeCount : pinnedBusy)
  const condaHint = conda.data && !conda.data.available ? conda.data.error : conda.data?.error || t('newjob.conda_hint')
  const describe = (f: Field, hint = true) => cx(hint && `${ids[f]}-hint`, errors[f] && `${ids[f]}-err`) || undefined

  return (
    <form
      ref={formRef}
      noValidate
      onSubmit={onSubmit}
      className="mx-auto max-w-3xl space-y-4"
    >
      <div>
        <h1 className="text-lg font-semibold">{source ? t(editing ? 'newjob.title_edit' : rerun ? 'newjob.title_rerun' : 'newjob.title_copy', { id: source.id }) : t('newjob.title_new')}</h1>
        <p className="mt-0.5 text-ui text-fg-3">
          {source
            ? t(editing ? 'newjob.subtitle_edit' : rerun ? 'newjob.subtitle_rerun' : 'newjob.subtitle_copy', { name: source.name })
            : t('newjob.subtitle_new')}
        </p>
        {rerun && source?.status === 'failed' && (
          <p className="mt-1 text-ui text-fail [overflow-wrap:anywhere]">
            {t('newjob.source_failed')}<span className="font-mono text-[12.5px]">{failText(source)}</span>
          </p>
        )}
      </div>
      {editClosed && (
        <Alert tone="warn">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span className="min-w-0 flex-1">{t('newjob.edit_closed', { id: source.id })}</span>
            <Link to={`/jobs/${source.id}`} className="relative shrink-0 font-medium text-fg underline decoration-fg/30 underline-offset-2 before:absolute before:-inset-x-2 before:-inset-y-3 hover:decoration-fg">
              {t('newjob.edit_view_job')}
            </Link>
          </div>
        </Alert>
      )}
      {saved && (
        <Alert>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span className="min-w-0 flex-1">{t('newjob.draft_restored')}</span>
            <button
              type="button"
              className="relative shrink-0 font-medium text-fg underline decoration-fg/30 underline-offset-2 before:absolute before:-inset-x-2 before:-inset-y-3 hover:decoration-fg"
              data-draft-reset
              onClick={() => {
                submitted.current = true
                writeDraft(draftKey, null)
                onReset()
              }}
            >
              {t('newjob.draft_reset')}
            </button>
          </div>
        </Alert>
      )}
      {rerun && source && !snapshotSelected && <CodeChangeNotice source={source} workdir={workdir} />}

      <Card className="divide-y divide-line">
        {rerun && source && (
          <FormRow
            label={t('newjob.code_version')}
            hint={snapshotSelected && !canRestore ? t('newjob.code_hint_unavailable') : snapshotSelected
              ? t('newjob.code_hint_snapshot')
              : source.pruned_at ? t('newjob.code_hint_pruned') : !canRestore ? t('newjob.code_hint_not_ready') : t('newjob.code_hint_default')}
          >
            <CodeSourcePicker name={`${uid}-code`} source={source} workdir={workdir} canRestore={canRestore} snapshot={snapshotSelected} onChange={setUseSnapshot} />
          </FormRow>
        )}
        <FormRow label={lockedWorkdir ? t('newjob.workdir_source') : t('newjob.workdir')} hint={lockedWorkdir ? t('newjob.workdir_hint_snapshot') : t('newjob.workdir_hint')} htmlFor={ids.workdir}>
          {lockedWorkdir ? (
            // 只读：衬底色、浅一级的强边线、次要文字色，悬停和聚焦时边线不变，看起来不像可以编辑的输入框。
            // 它仍在 Tab 顺序里（可以选中复制），键盘焦点用和按钮一样的 2px 外框（全局 :focus-visible 的样式）
            <Input id={ids.workdir} aria-describedby={`${ids.workdir}-hint`} value={activeWorkdir} readOnly className="cursor-default border-line-strong! bg-subtle! font-mono text-[12.5px] text-fg-2! hover:border-line-strong! focus:border-line-strong! focus:ring-0! focus-visible:outline-solid!" />
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

        <FormRow label={t('newjob.cmd')} hint={lockedWorkdir
          ? t('newjob.cmd_hint_snapshot')
          : t('newjob.cmd_hint')} htmlFor={ids.cmd}>
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
            placeholder={t('newjob.cmd_placeholder')}
            className="font-mono text-[12.5px]"
          />
          <FieldError id={`${ids.cmd}-err`}>{errors.cmd}</FieldError>
          {picksGpus && (
            <p className="mt-1.5 text-xs leading-relaxed text-warn">
              {t(strictOn ? 'newjob.cmd_picks_gpus_strict' : 'newjob.cmd_picks_gpus', { what: picksGpus })}
            </p>
          )}
        </FormRow>

        <FormRow label={t('newjob.conda')} hint={condaHint} htmlFor={ids.conda}>
          <Select id={ids.conda} aria-describedby={cx(`${ids.conda}-hint`, launcher && `${ids.conda}-warn`)} value={condaEnv} onChange={(e) => setCondaEnv(e.target.value)} disabled={conda.isLoading}>
            <option value="">{t('newjob.conda_none')}</option>
            {conda.data?.envs.map((e) => (
              <option key={e.path} value={e.name}>
                {e.name}
              </option>
            ))}
            {condaEnv && !conda.data?.envs.some((e) => e.name === condaEnv) && <option value={condaEnv}>{condaEnv}</option>}
          </Select>
          {launcher && (
            <p id={`${ids.conda}-warn`} className="mt-1.5 text-xs leading-relaxed text-warn">
              {t('newjob.conda_none_warning', { cmd: launcher })}
            </p>
          )}
        </FormRow>

        <FormRow label="GPU" hint={t('newjob.gpu_hint', { total, free: freeCount, n: total })} describedBy={errors.gpu && `${ids.gpu}-err`}>
          <div className="space-y-3">
            <Segmented
              label={t('newjob.gpu_mode_label')}
              value={mode}
              onChange={edit('gpu', setMode)}
              options={[
                { value: 'count', label: t('gpus.mode_count') },
                { value: 'pinned', label: t('gpus.mode_pinned') },
              ]}
            />
            {mode === 'count' ? (
              <div className="flex items-center gap-3">
                {/* 步进器相当于一个数字输入框，边线和输入框一致 */}
                <div role="group" aria-label={t('newjob.gpu_count_label')} className="inline-flex h-9 items-stretch overflow-hidden rounded-md border border-field-line max-sm:h-[46px]">
                  <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40 max-sm:w-11" onClick={() => setCount((c) => Math.max(0, c - 1))} disabled={count <= 0} aria-label={t('gpus.count_less')}>
                    <Minus className="size-4" aria-hidden />
                  </button>
                  <output aria-live="polite" className="grid w-10 place-items-center border-x border-field-line text-sm font-semibold">
                    {count}
                  </output>
                  <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40 max-sm:w-11" onClick={() => setCount((c) => Math.min(total || 64, c + 1))} disabled={total > 0 && count >= total} aria-label={t('gpus.count_more')}>
                    <Plus className="size-4" aria-hidden />
                  </button>
                </div>
                <span className="text-ui text-fg-2">{count === 0 ? t('newjob.gpu_zero') : t('newjob.gpu_count_suffix', { n: count })}</span>
              </div>
            ) : (
              <GpuPicker id={ids.gpu} describedBy={errors.gpu && `${ids.gpu}-err`} gpus={gpuList} value={pinned} onChange={edit('gpu', setPinned)} />
            )}
            <FieldError id={`${ids.gpu}-err`}>{errors.gpu}</FieldError>
            {pinnedReserved.length > 0 ? (
              <p className="text-xs leading-relaxed text-warn">
                {t('newjob.pinned_reserved_warning', { list: pinnedReserved.map((r) => t('newjob.pinned_reserved_item', { gpu: r.index, id: r.id })).join(t('newjob.reserved_sep')) })}
              </p>
            ) : pinnedExternal.length > 0 ? (
              <p className="text-xs leading-relaxed text-warn">
                {t('newjob.pinned_external_warning', { gpus: pinnedExternal.join(', ') })}
              </p>
            ) : pinnedUnknown.length > 0 ? (
              <p className="text-xs leading-relaxed text-warn">
                {t('newjob.pinned_unknown_warning', { gpus: pinnedUnknown.join(', ') })}
              </p>
            ) : (
              willQueue && <p className="text-xs leading-relaxed text-warn">{t('newjob.will_queue')}</p>
            )}
          </div>
        </FormRow>

        <FormRow label={t('newjob.name')} hint={t('newjob.name_hint')} htmlFor={ids.name}>
          <Input id={ids.name} aria-describedby={`${ids.name}-hint`} value={name} onChange={(e) => setName(e.target.value)} placeholder={t('newjob.name_placeholder')} maxLength={100} />
        </FormRow>

        <FormRow label={t('newjob.memo')} hint={t('newjob.memo_hint')} htmlFor={ids.memo}>
          <Textarea id={ids.memo} rows={2} aria-describedby={describe('memo')} aria-invalid={!!errors.memo || undefined}
            value={memo} onChange={(e) => edit('memo', setMemo)(e.target.value)} />
          <FieldError id={`${ids.memo}-err`}>{errors.memo}</FieldError>
        </FormRow>

        <FormRow label={t('newjob.notify')} hint={t('newjob.notify_hint')}>
          <label className="inline-flex min-h-9 items-center gap-2 text-ui max-sm:min-h-11">
            <input type="checkbox" className="size-4 accent-inv" checked={notifyDone} onChange={(e) => setNotifyDone(e.target.checked)}
              aria-describedby={notifyDone && pushOff ? ids.notifyOff : undefined} />
            {t('newjob.notify_done')}
          </label>
          {notifyDone && pushOff && (
            <p id={ids.notifyOff} className="text-xs leading-relaxed text-warn">
              {t('newjob.notify_push_off')}
              <Link to="/settings/mobile" className="ml-1.5 font-medium text-fg underline decoration-fg/30 underline-offset-2 hover:decoration-fg">{t('newjob.notify_push_settings')}</Link>
            </p>
          )}
        </FormRow>

        <FormRow label={t('newjob.env')} hint={t('newjob.env_hint')} htmlFor={ids.env}>
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
            placeholder={t('newjob.env_placeholder')}
          />
          <FieldError id={`${ids.env}-err`}>{errors.env}</FieldError>
        </FormRow>

        <FormRow label={t('newjob.bashrc')} hint={t('newjob.bashrc_hint')}>
          <label className="inline-flex min-h-8 items-center gap-2 text-ui text-fg-2 max-sm:min-h-11">
            <input
              type="checkbox"
              checked={inheritBashrc}
              onChange={(e) => setInheritBashrc(e.target.checked)}
              className="size-4 accent-fg"
            />
            {t('newjob.bashrc_load')}
          </label>
        </FormRow>

        {/* 提交栏吸在视口底部（手机上在底部导航上方），长表单不用滚到底才能提交 */}
        <div className="sticky bottom-0 z-10 flex flex-col-reverse gap-3 rounded-b-lg bg-subtle px-4 py-3 max-sm:bottom-[calc(3.5rem+env(safe-area-inset-bottom))] sm:flex-row sm:items-center sm:px-5">
          <ErrorBox error={submit.error} className="sm:mr-auto" />
          <div className="flex gap-2 sm:ml-auto">
            <Button onClick={() => nav(-1)}>{t('newjob.back')}</Button>
            <Button type="submit" variant="primary" loading={submit.isPending} disabled={submit.isPending || (snapshotSelected && !canRestore) || editClosed} className="flex-1 sm:flex-none">
              {editing ? t('newjob.save_edit') : t('newjob.submit')}
              <Kbd>{modKey} ↵</Kbd>
            </Button>
          </div>
        </div>
      </Card>
    </form>
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
      {dirty && <span className="text-fg-3">{t('newjob.dirty_mark')}</span>}
    </>
  )
  const current = !workdir ? t('newjob.code_fill_workdir') : head.isLoading ? t('newjob.code_reading') : head.data?.is_git && head.data.commit ? version(head.data.branch, head.data.commit, head.data.dirty) : t('newjob.code_not_git')
  const snap = source.pruned_at ? t('newjob.snap_pruned') : !canRestore ? t('newjob.snap_not_ready') : source.git_commit ? version(source.git_branch, source.git_commit, source.git_dirty) : t('newjob.snap_non_git')
  const options = [
    { value: false, title: t('newjob.code_current'), detail: current, disabled: false },
    { value: true, title: t('newjob.code_snapshot', { id: source.id }), detail: snap, disabled: !canRestore },
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
      {t('newjob.code_link')}
    </Link>
  )
  if (!head.data.is_git || !source.git_commit) {
    return (
      <Alert>
        {t('newjob.not_git_before')}{code}{t('newjob.code_view_after')}
      </Alert>
    )
  }
  const msgs: string[] = []
  if (head.data.commit !== source.git_commit) msgs.push(t('newjob.diff_commit', { current: head.data.commit?.slice(0, 8) ?? '', original: source.git_commit.slice(0, 8) }))
  if (head.data.dirty) msgs.push(t('newjob.diff_dirty'))
  else if (source.git_dirty) msgs.push(t('newjob.diff_source_dirty'))
  if (!msgs.length) {
    return (
      <Alert>
        {t('newjob.code_same', { commit: source.git_commit.slice(0, 8) })}
      </Alert>
    )
  }
  return (
    <Alert tone="warn">
      {t('newjob.diff_intro_before')}<b>{t('newjob.diff_now')}</b>{t('newjob.diff_intro_after')}
      <ul className="mt-1 list-disc pl-4">
        {msgs.map((m) => (
          <li key={m}>{m}</li>
        ))}
      </ul>
      {code}{t('newjob.code_view_after')}
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
          placeholder={t('newjob.workdir_placeholder')}
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
            aria-label={t('newjob.subdirs')}
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
          <History className="size-3.5 text-fg-3" aria-label={t('newjob.recent')} />
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
