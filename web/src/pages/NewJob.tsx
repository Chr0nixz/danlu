import { useMutation, useQuery } from '@tanstack/react-query'
import { Check, FolderOpen, History, Minus, Plus } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { gpuBusy, useGpus } from '../components/GpuGrid'
import { Alert, Button, Card, cx, ErrorBox, FormRow, Input, Kbd, Segmented, Select, Skeleton, Textarea } from '../components/ui'
import { api, type GPU, type Job } from '../lib/api'

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
    if (i <= 0) throw new Error(`环境变量格式应为 KEY=VALUE：${line}`)
    const k = line.slice(0, i).trim()
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(k)) throw new Error(`环境变量名不合法：${k}`)
    env[k] = line.slice(i + 1)
  }
  return env
}

export function NewJob() {
  const [params] = useSearchParams()
  const fromId = Number(params.get('from')) || null
  const source = useQuery({ queryKey: ['job', fromId], queryFn: () => api.job(fromId!), enabled: !!fromId })
  if (fromId && source.isLoading) {
    return (
      <div className="mx-auto max-w-3xl space-y-4">
        <Skeleton className="h-7 w-48" />
        <Skeleton className="h-[480px] w-full rounded-lg" />
      </div>
    )
  }
  if (fromId && source.error) return <ErrorBox error={source.error} />
  return <JobForm key={fromId ?? 'new'} source={source.data ?? null} />
}

function JobForm({ source }: { source: Job | null }) {
  const nav = useNavigate()
  const gpus = useGpus()
  const conda = useQuery({ queryKey: ['conda'], queryFn: api.condaEnvs, staleTime: 60_000 })
  const recent = useQuery({ queryKey: ['recentWorkdirs'], queryFn: api.recentWorkdirs })
  const formRef = useRef<HTMLFormElement>(null)

  const [name, setName] = useState(source ? rerunName(source.name) : '')
  const [workdir, setWorkdir] = useState(source?.workdir ?? '')
  const [condaEnv, setCondaEnv] = useState(source?.conda_env ?? '')
  const [cmd, setCmd] = useState(source?.cmd ?? '')
  const [mode, setMode] = useState<'count' | 'pinned'>(source?.gpus_pinned.length ? 'pinned' : 'count')
  const [count, setCount] = useState(source?.gpus_req ?? 1)
  const [pinned, setPinned] = useState<number[]>(source?.gpus_pinned ?? [])
  const [envText, setEnvText] = useState(source ? Object.entries(source.env).map(([k, v]) => `${k}=${v}`).join('\n') : '')
  const [formError, setFormError] = useState<unknown>(null)

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
    setFormError(null)
    let env: Record<string, string>
    try {
      env = parseEnv(envText)
    } catch (err) {
      setFormError(err)
      return
    }
    if (mode === 'pinned' && !pinned.length) {
      setFormError(new Error('请至少选择一张卡'))
      return
    }
    submit.mutate({
      name: name.trim(),
      workdir: workdir.trim().replace(/\/+$/, '') || '/',
      conda_env: condaEnv,
      cmd,
      env,
      gpus: mode === 'count' ? count : pinned.length,
      pinned_gpus: mode === 'pinned' ? pinned : [],
      rerun_of: source?.id ?? null,
    })
  }

  const need = mode === 'count' ? count : pinned.length
  const pinnedBusy = mode === 'pinned' && pinned.some((i) => {
    const g = gpuList.find((x) => x.index === i)
    return g && gpuBusy(g)
  })
  const willQueue = need > 0 && (mode === 'count' ? need > freeCount : pinnedBusy)
  const condaHint = conda.data && !conda.data.available ? conda.data.error : conda.data?.error || '任务启动前自动激活。'

  return (
    <form
      ref={formRef}
      onSubmit={onSubmit}
      onKeyDown={(e) => {
        // Ctrl/⌘ + Enter 提交
        if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) formRef.current?.requestSubmit()
      }}
      className="mx-auto max-w-3xl space-y-4"
    >
      <div>
        <h1 className="text-lg font-semibold">{source ? `重跑任务 #${source.id}` : '新建任务'}</h1>
        <p className="mt-0.5 text-ui text-fg-3">
          {source ? `设置已从「${source.name}」复制过来，可以先改参数再提交。` : '提交后按顺序排队，有足够的空卡就自动开始。'}
        </p>
      </div>
      {source && <CodeChangeNotice source={source} workdir={workdir} />}

      <Card className="divide-y divide-line">
        <FormRow label="工作目录" hint="服务器上的绝对路径。启动时会自动保存一份代码快照。">
          <WorkdirInput value={workdir} onChange={setWorkdir} recent={recent.data?.dirs ?? []} />
        </FormRow>

        <FormRow label="命令" hint="在工作目录下用 bash 执行，可以写多行。torchrun 不用写 --master_port，会自动分配。">
          <Textarea
            value={cmd}
            onChange={(e) => setCmd(e.target.value)}
            rows={4}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            required
            placeholder="torchrun --nproc_per_node 2 train.py --lr 3e-4"
            className="font-mono text-[12.5px]"
          />
        </FormRow>

        <FormRow label="conda 环境" hint={condaHint}>
          <Select value={condaEnv} onChange={(e) => setCondaEnv(e.target.value)} disabled={conda.isLoading}>
            <option value="">不使用 conda</option>
            {conda.data?.envs.map((e) => (
              <option key={e.path} value={e.name}>
                {e.name}
              </option>
            ))}
            {condaEnv && !conda.data?.envs.some((e) => e.name === condaEnv) && <option value={condaEnv}>{condaEnv}</option>}
          </Select>
        </FormRow>

        <FormRow label="GPU" hint={`共 ${total} 张，现在空闲 ${freeCount} 张。整卡独占。`}>
          <div className="space-y-3">
            <Segmented
              value={mode}
              onChange={setMode}
              options={[
                { value: 'count', label: '按张数' },
                { value: 'pinned', label: '指定卡号' },
              ]}
            />
            {mode === 'count' ? (
              <div className="flex items-center gap-3">
                <div className="inline-flex h-9 items-stretch overflow-hidden rounded-md border border-line-strong">
                  <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40" onClick={() => setCount((c) => Math.max(0, c - 1))} disabled={count <= 0} aria-label="减少">
                    <Minus className="size-4" />
                  </button>
                  <span className="grid w-10 place-items-center border-x border-line-strong text-sm font-semibold">{count}</span>
                  <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40" onClick={() => setCount((c) => Math.min(total || 64, c + 1))} disabled={total > 0 && count >= total} aria-label="增加">
                    <Plus className="size-4" />
                  </button>
                </div>
                <span className="text-ui text-fg-2">{count === 0 ? '不使用 GPU' : '张卡，自动挑空闲的'}</span>
              </div>
            ) : (
              <GpuPicker gpus={gpuList} value={pinned} onChange={setPinned} />
            )}
            {willQueue && <p className="text-xs text-warn">现在空闲的卡不够，提交后会排队，等卡空出来自动开始。</p>}
          </div>
        </FormRow>

        <FormRow label="任务名" hint="留空则用目录名加时间。">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 vit-lr3e-4" maxLength={100} />
        </FormRow>

        <FormRow label="环境变量" hint="可选，每行一个 KEY=VALUE。">
          <Textarea
            className="font-mono text-[12.5px]"
            rows={2}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            value={envText}
            onChange={(e) => setEnvText(e.target.value)}
            placeholder={'WANDB_PROJECT=vit\nOMP_NUM_THREADS=8'}
          />
        </FormRow>

        <div className="flex flex-col-reverse gap-3 rounded-b-lg bg-subtle px-4 py-3 sm:flex-row sm:items-center sm:px-5">
          <ErrorBox error={formError ?? submit.error} className="sm:mr-auto" />
          <div className="flex gap-2 sm:ml-auto">
            <Button onClick={() => nav(-1)}>返回</Button>
            <Button type="submit" variant="primary" loading={submit.isPending} className="flex-1 sm:flex-none">
              提交任务
              <Kbd>Ctrl ↵</Kbd>
            </Button>
          </div>
        </div>
      </Card>
    </form>
  )
}

function GpuPicker({ gpus, value, onChange }: { gpus: GPU[]; value: number[]; onChange: (v: number[]) => void }) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {gpus.map((g) => {
        const on = value.includes(g.index)
        const state = g.job ? `#${g.job.id} 在用` : g.external.length ? '外部进程' : '空闲'
        const tone = g.job ? 'text-run' : g.external.length ? 'text-warn' : 'text-ok'
        return (
          <button
            type="button"
            key={g.index}
            aria-pressed={on}
            onClick={() => onChange(on ? value.filter((x) => x !== g.index) : [...value, g.index].sort((a, b) => a - b))}
            className={cx(
              'relative rounded-md border px-3 py-2 text-left transition-colors duration-150',
              on ? 'border-fg bg-subtle ring-1 ring-fg' : 'border-line-strong hover:bg-subtle',
            )}
          >
            <div className="flex items-center justify-between">
              <span className="text-ui font-semibold">GPU {g.index}</span>
              {on && <Check className="size-3.5" strokeWidth={3} />}
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

/** 重跑时比较工作目录当前代码和原任务的快照，有变化就提醒 */
function CodeChangeNotice({ source, workdir }: { source: Job; workdir: string }) {
  const head = useQuery({ queryKey: ['githead', workdir], queryFn: () => api.gitHead(workdir), enabled: !!workdir })
  if (!head.data) return null
  const msgs: string[] = []
  if (head.data.is_git && source.git_commit) {
    if (head.data.commit !== source.git_commit) msgs.push(`当前 commit 是 ${head.data.commit?.slice(0, 8)}，原任务是 ${source.git_commit.slice(0, 8)}`)
    if (head.data.dirty) msgs.push('工作目录有未提交的改动')
    else if (source.git_dirty) msgs.push('原任务运行时有未提交的改动')
  }
  if (!msgs.length) return null
  return (
    <Alert tone="warn">
      重跑会使用工作目录<b>现在</b>的代码，可能和原任务不同：
      <ul className="mt-1 list-disc pl-4">
        {msgs.map((m) => (
          <li key={m}>{m}</li>
        ))}
      </ul>
      原任务的代码可以在它的「代码」标签页里查看和下载。
    </Alert>
  )
}

function WorkdirInput({ value, onChange, recent }: { value: string; onChange: (v: string) => void; recent: string[] }) {
  const [focus, setFocus] = useState(false)
  const [query, setQuery] = useState(value)
  const timer = useRef<number>(undefined)
  useEffect(() => {
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setQuery(value), 200)
  }, [value])
  const dirs = useQuery({ queryKey: ['dirs', query], queryFn: () => api.dirs(query), enabled: focus })
  const options = useMemo(() => (dirs.data?.dirs ?? []).filter((d) => d !== value), [dirs.data, value])

  return (
    <div className="space-y-2">
      <div className="relative">
        <FolderOpen className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-fg-3" />
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onFocus={() => setFocus(true)}
          onBlur={() => setTimeout(() => setFocus(false), 150)}
          placeholder="/home/you/projects/vit"
          className="pl-8 font-mono text-[12.5px]"
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          required
        />
        {focus && options.length > 0 && (
          <div className="absolute inset-x-0 top-full z-20 mt-1 max-h-64 overflow-auto rounded-md border border-line bg-surface py-1 shadow-[0_8px_24px_rgb(0_0_0/0.12)]">
            {options.map((d) => (
              <button
                type="button"
                key={d}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => onChange(d + '/')}
                className="block w-full truncate px-3 py-1.5 text-left font-mono text-[12.5px] text-fg-2 hover:bg-hover hover:text-fg"
              >
                {d}/
              </button>
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
              className="h-6 max-w-full truncate rounded border border-line bg-subtle px-1.5 font-mono text-[11.5px] text-fg-2 hover:border-line-strong hover:text-fg"
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
