import { projectOf, type GPU, type Job, type SubmitReq } from '@danlu/core'
import { t } from '../lib/i18n'
import { useMutation, useQueries, useQuery } from '@tanstack/react-query'
import { GitBranch, History, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { apiFor, refreshFleet, type ServerSnap } from '../lib/backend'
import { isOffline, jobKey } from '../lib/fleet'
import { placementFor, rememberPlacement } from '../lib/local'
import { MOD, modKey } from '../lib/platform'
import { useWorkspace, type Draft } from '../lib/workspace'
import { externalBusy, freeCount, heatStyle } from './GpuStrip'
import { Button, cx, ErrorText, Field, inputClass, Kbd, Segmented, useToast } from './ui'

function blank(server: string): Draft {
  return { server, name: '', workdir: '', conda_env: '', cmd: '', env: {}, inherit_bashrc: false, gpus: 1, pinned: [], rerun_of: null }
}

const envToText = (env: Record<string, string>) =>
  Object.entries(env)
    .map(([k, v]) => `${k}=${v}`)
    .join('\n')

function parseEnv(text: string): Record<string, string> | string {
  const env: Record<string, string> = {}
  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const i = line.indexOf('=')
    if (i <= 0) return t('submit.env_invalid', { line })
    env[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return env
}

/**
 * 提交任务：先选服务器，再选这台上的工作目录、命令、环境、卡。
 * 复制来的任务所有字段预先填好；复制到另一台时，目录和环境按"项目 × 服务器"的记录填。
 */
export function SubmitPanel({ initial, servers }: { initial: Draft | null; servers: ServerSnap[] }) {
  const { closePane, openJob } = useWorkspace()
  const toast = useToast()
  const usable = servers.filter((s) => !isOffline(s))
  const [d, setD] = useState<Draft>(() => initial ?? blank(usable[0]?.name ?? servers[0]?.name ?? ''))
  const [envText, setEnvText] = useState(() => envToText(d.env))
  const [mode, setMode] = useState<'count' | 'pinned'>(d.pinned.length ? 'pinned' : 'count')
  const project = d.workdir ? projectOf(d.workdir) : (d.project ?? '')
  const snap = servers.find((s) => s.name === d.server)
  const gpus = snap?.gpus ?? []
  const api = apiFor(d.server)

  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }))

  // 换服务器：同一个项目按记录填目录和环境，没有记录就清空让人选；指定的卡号不跨服务器
  const switchServer = (name: string) => {
    if (name === d.server) return
    const place = project ? placementFor(project, name) : undefined
    set({ server: name, project, workdir: place?.workdir ?? '', conda_env: place?.conda_env ?? '', pinned: [], rerun_of: null })
    setMode('count')
  }

  const envs = useQuery({ queryKey: ['conda-envs', d.server], queryFn: () => api.condaEnvs(), staleTime: 5 * 60_000 })
  const git = useQuery({
    queryKey: ['git', d.server, d.workdir],
    queryFn: () => api.gitHead(d.workdir),
    enabled: d.workdir.startsWith('/'),
    staleTime: 10_000,
  })

  const submit = useMutation({
    mutationFn: (req: SubmitReq) => api.submit(req),
    onSuccess: (job) => {
      rememberPlacement(projectOf(job.workdir), d.server, { workdir: job.workdir, conda_env: job.conda_env })
      toast(job.status === 'queued' ? t('submit.submitted_queued', { key: jobKey(d.server, job.id) }) : t('submit.submitted', { key: jobKey(d.server, job.id) }))
      refreshFleet()
      openJob(d.server, job.id)
    },
  })
  const [formError, setFormError] = useState<string | null>(null)

  const onSubmit = (e?: FormEvent) => {
    e?.preventDefault()
    setFormError(null)
    const env = parseEnv(envText)
    if (typeof env === 'string') return setFormError(env)
    if (!d.workdir.trim()) return setFormError(t('submit.workdir_required'))
    if (!d.cmd.trim()) return setFormError(t('submit.cmd_required'))
    submit.mutate({
      name: d.name.trim(),
      workdir: d.workdir.trim(),
      conda_env: d.conda_env,
      cmd: d.cmd.trim(),
      env,
      inherit_bashrc: d.inherit_bashrc,
      gpus: mode === 'pinned' ? d.pinned.length : d.gpus,
      pinned_gpus: mode === 'pinned' ? d.pinned : [],
      rerun_of: d.rerun_of,
    })
  }

  return (
    <form
      onSubmit={onSubmit}
      onKeyDown={(e) => {
        if (modKey(e) && e.key === 'Enter') onSubmit()
      }}
      className="rise flex h-full flex-col"
    >
      <div className="flex items-start gap-3 px-4 pt-4 pb-3">
        <div className="min-w-0 flex-1">
          <h2 className="text-[15px] font-semibold">{d.source ? t('submit.copy_title') : t('submit.new_title')}</h2>
          <p className="mt-0.5 text-fg-3">
            {d.source ? (
              <>
                {t('submit.copy_before')}<span className="num text-fg-2">{d.source}</span>{t('submit.copy_after')}
                {d.rerun_of && t('submit.rerun_mark')}
              </>
            ) : (
              t('submit.sync_hint')
            )}
          </p>
        </div>
        <Button variant="ghost" className="-mt-0.5 -mr-1.5 px-1.5" aria-label={t('common.close')} onClick={closePane}>
          <X />
        </Button>
      </div>

      <div className="min-h-0 flex-1 space-y-3.5 overflow-y-auto px-4 pb-4">
        <Field label={t('submit.server')}>
          <Segmented
            value={d.server}
            onChange={switchServer}
            options={servers.map((s) => ({ value: s.name, label: s.name, disabled: isOffline(s) }))}
          />
        </Field>

        <WorkdirField server={d.server} value={d.workdir} onChange={(workdir) => set({ workdir })} sourceProject={d.source && !d.workdir ? project : ''} />
        {d.workdir.startsWith('/') && (
          <p className="-mt-2 flex items-center gap-1.5 text-[11.5px] text-fg-3">
            <GitBranch className="size-3.5" />
            {git.isLoading ? (
              t('submit.git_checking')
            ) : git.data?.is_git ? (
              <span className="num">
                {git.data.commit?.slice(0, 8)} · {git.data.branch}
                {git.data.dirty && <span className="text-warn">{t('submit.git_dirty')}</span>}
              </span>
            ) : (
              t('submit.not_git')
            )}
          </p>
        )}

        <CommandField project={project} value={d.cmd} onChange={(cmd) => set({ cmd })} servers={servers} />

        <div className="grid grid-cols-2 gap-3">
          <Field label={t('submit.conda')} hint={envs.data && !envs.data.available ? t('submit.conda_missing') : undefined}>
            <select className={inputClass} value={d.conda_env} onChange={(e) => set({ conda_env: e.target.value })}>
              <option value="">{t('submit.conda_none')}</option>
              {d.conda_env && !envs.data?.envs.some((x) => x.name === d.conda_env) && <option value={d.conda_env}>{d.conda_env}</option>}
              {envs.data?.envs.map((x) => (
                <option key={x.path} value={x.name}>
                  {x.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t('submit.name')} hint={t('submit.name_hint')}>
            <input className={inputClass} value={d.name} onChange={(e) => set({ name: e.target.value })} placeholder="vit lr3e-4" />
          </Field>
        </div>

        <Field label="GPU">
          <div className="space-y-2">
            <Segmented
              value={mode}
              onChange={setMode}
              options={[
                { value: 'count', label: t('submit.mode_count') },
                { value: 'pinned', label: t('submit.mode_pinned') },
              ]}
            />
            {mode === 'count' ? (
              <div className="flex items-center gap-2">
                <input
                  type="number"
                  min={0}
                  max={Math.max(gpus.length, 8)}
                  className={cx(inputClass, 'num !w-20')}
                  value={d.gpus}
                  onChange={(e) => set({ gpus: Math.max(0, Number(e.target.value) || 0) })}
                />
                <span className="text-fg-3">
                  {t('submit.cards_suffix', { free: freeCount(gpus), total: gpus.length })}
                </span>
              </div>
            ) : (
              <PinnedPicker gpus={gpus} value={d.pinned} onChange={(pinned) => set({ pinned })} />
            )}
          </div>
        </Field>

        <Field label={t('submit.env')} hint={t('submit.env_hint')}>
          <textarea
            className={cx(inputClass, 'num h-auto min-h-[52px] resize-y py-1.5 text-[11.5px]')}
            rows={2}
            value={envText}
            onChange={(e) => setEnvText(e.target.value)}
            placeholder="WANDB_PROJECT=vit"
            spellCheck={false}
          />
        </Field>

        <Field label={t('submit.bashrc')} hint={t('submit.bashrc_hint')}>
          <span className="flex min-h-7 items-center gap-2 text-[11.5px] text-fg-2">
            <input
              type="checkbox"
              checked={d.inherit_bashrc}
              onChange={(e) => set({ inherit_bashrc: e.target.checked })}
              className="size-4 accent-fg"
            />
            {t('submit.bashrc_load')}
          </span>
        </Field>
      </div>

      <div className="flex items-center gap-3 border-t border-line px-4 py-3">
        <ErrorText error={formError ?? submit.error} className="min-w-0 flex-1 truncate" />
        <span className="ml-auto" />
        <Button variant="primary" type="submit" loading={submit.isPending} disabled={!d.server}>
          {t('submit.submit_to', { server: d.server })}
          <Kbd className="border-on-primary/30 text-on-primary/70">{MOD}+Enter</Kbd>
        </Button>
      </div>
    </form>
  )
}

/** 工作目录：最近用过的目录 + 边输边补全（服务器上的目录） */
function WorkdirField({ server, value, onChange, sourceProject }: { server: string; value: string; onChange: (v: string) => void; sourceProject: string }) {
  const api = apiFor(server)
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState(value)
  useEffect(() => setTyped(value), [value])
  const recent = useQuery({ queryKey: ['recent-dirs', server], queryFn: () => api.recentWorkdirs(), staleTime: 60_000 })
  // 输入以 / 开头时补全目录，停顿 200ms 再请求
  const [probe, setProbe] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setProbe(typed.startsWith('/') ? typed : ''), 200)
    return () => clearTimeout(t)
  }, [typed])
  const dirs = useQuery({ queryKey: ['dirs', server, probe], queryFn: () => api.dirs(probe), enabled: !!probe, staleTime: 10_000 })
  const options = useMemo(() => {
    const list = probe ? (dirs.data?.dirs ?? []) : (recent.data?.dirs ?? [])
    // 复制到另一台还没有记录时，把同名项目的目录排到前面
    return sourceProject ? [...list].sort((a, b) => Number(projectOf(b) === sourceProject) - Number(projectOf(a) === sourceProject)) : list
  }, [probe, dirs.data, recent.data, sourceProject])
  const box = useRef<HTMLDivElement>(null)

  return (
    <div ref={box} className="relative" onBlur={(e) => !box.current?.contains(e.relatedTarget) && setOpen(false)}>
      <Field label={t('submit.workdir')} hint={sourceProject ? t('submit.workdir_first_copy', { server, project: sourceProject }) : value ? t('submit.workdir_project', { project: projectOf(value) }) : undefined}>
        <input
          className={cx(inputClass, 'num text-[11.5px]')}
          value={typed}
          onFocus={() => setOpen(true)}
          onChange={(e) => {
            setTyped(e.target.value)
            onChange(e.target.value)
            setOpen(true)
          }}
          onKeyDown={(e) => e.key === 'Escape' && open && (e.stopPropagation(), setOpen(false))}
          placeholder="/home/you/code/vit"
          spellCheck={false}
        />
      </Field>
      {open && options.length > 0 && (
        <div className="absolute inset-x-0 top-full z-20 mt-1 max-h-56 overflow-y-auto rounded-[6px] border border-line-strong bg-panel py-1 shadow-float">
          {!probe && <div className="px-2.5 pt-0.5 pb-1 text-[11px] text-fg-3">{t('submit.recent')}</div>}
          {options.map((p) => (
            <button
              key={p}
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => {
                onChange(p)
                setTyped(p)
                setOpen(false)
              }}
              className="num flex w-full items-center gap-2 px-2.5 py-1 text-left text-[11.5px] hover:bg-hover"
            >
              <span className="min-w-0 flex-1 truncate">{p}</span>
              <span className="shrink-0 text-fg-3">{projectOf(p)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** 命令和命令历史：按项目列出最近用过的命令，从各台服务器的任务记录里取 */
function CommandField({ project, value, onChange, servers }: { project: string; value: string; onChange: (v: string) => void; servers: ServerSnap[] }) {
  const names = servers.filter((s) => !isOffline(s)).map((s) => s.name)
  const results = useQueries({
    queries: names.map((n) => ({
      queryKey: ['cmd-history', n, project],
      queryFn: () => apiFor(n).jobs({ q: project, size: 50 }),
      enabled: !!project,
      staleTime: 60_000,
    })),
  })
  const history = useMemo(() => {
    const seen = new Map<string, Job>()
    for (const r of results) {
      for (const j of r.data?.jobs ?? []) {
        if (projectOf(j.workdir) !== project) continue
        const prev = seen.get(j.cmd)
        if (!prev || prev.created_at < j.created_at) seen.set(j.cmd, j)
      }
    }
    return [...seen.values()].sort((a, b) => b.created_at - a.created_at).slice(0, 8)
  }, [results, project])

  return (
    <Field label={t('submit.cmd')}>
      <textarea
        className={cx(inputClass, 'num h-auto min-h-[68px] resize-y py-1.5 text-[11.5px]')}
        rows={3}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="torchrun --nproc_per_node=4 train.py --cfg configs/vit.yaml"
        spellCheck={false}
      />
      {history.length > 0 && (
        <div className="mt-1.5 rounded-[6px] border border-line bg-card py-1">
          <div className="flex items-center gap-1.5 px-2.5 pt-0.5 pb-1 text-[11px] text-fg-3">
            <History className="size-3" />
            {t('submit.cmd_recent', { project })}
          </div>
          {history.map((j) => (
            <button
              key={j.cmd}
              type="button"
              onClick={() => onChange(j.cmd)}
              title={j.cmd}
              className={cx('num flex w-full items-center gap-2 px-2.5 py-[3px] text-left text-[11.5px] hover:bg-hover', j.cmd === value && 'text-fg-3')}
            >
              <span className="min-w-0 flex-1 truncate">{j.cmd}</span>
              <span className="shrink-0 text-fg-3">{j.name}</span>
            </button>
          ))}
        </div>
      )}
    </Field>
  )
}

function PinnedPicker({ gpus, value, onChange }: { gpus: GPU[]; value: number[]; onChange: (v: number[]) => void }) {
  if (!gpus.length) return <p className="text-fg-3">{t('submit.no_gpu')}</p>
  return (
    <div className="flex flex-wrap gap-1.5">
      {gpus.map((g) => {
        const on = value.includes(g.index)
        const unknown = (g.unknown_mem_mb ?? 0) > 0
        const note = g.job ? t('submit.gpu_job', { id: g.job.id }) : g.reservation ? t('submit.gpu_reservation', { id: g.reservation.id }) : g.external.length ? t('submit.gpu_external') : unknown ? t('submit.gpu_unknown') : t('submit.gpu_idle')
        return (
          <button
            key={g.index}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(on ? value.filter((i) => i !== g.index) : [...value, g.index].sort((a, b) => a - b))}
            style={heatStyle(g)}
            className={cx(
              'flex w-[86px] flex-col items-start rounded-[6px] px-2 py-1 text-left transition-shadow duration-[120ms]',
              on ? 'shadow-[inset_0_0_0_2px_var(--fg)]' : 'shadow-[inset_0_0_0_1px_var(--line-strong)] hover:shadow-[inset_0_0_0_1px_var(--fg-3)]',
            )}
          >
            <span className="num text-[11.5px] font-medium">GPU {g.index}</span>
            <span className={cx('text-[11px]', externalBusy(g) ? 'text-warn' : 'text-fg-3')}>{note}</span>
          </button>
        )
      })}
    </div>
  )
}
