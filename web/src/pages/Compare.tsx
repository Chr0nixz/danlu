import { useQuery } from '@tanstack/react-query'
import { ProfileSummary } from '../components/ResourceProfile'
import { diffArrays, structuredPatch } from 'diff'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import { Button, Card, Empty, ErrorBox, SkeletonRows, StatusLabel, buttonClass, cx } from '../components/ui'
import { fmtTime, type FileChange, type Job, type JobComparison, type ValueChange } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'

// 静态键映射（不是运行时拼键）：渲染时取词，模块级字符串表会冻结语言
const fileLabelKeys = { added: 'compare.added', removed: 'compare.removed', changed: 'compare.changed' } as const
const pane = 'term min-w-0'
const cell = 'min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]'

export function Compare() {
  const [params] = useSearchParams()
  const a = Number(params.get('a'))
  const b = Number(params.get('b'))
  const validA = Number.isSafeInteger(a) && a > 0
  const choosing = !params.has('b')
  const validPair = validA && Number.isSafeInteger(b) && b > 0 && a !== b
  const query = useQuery({
    queryKey: ['compare', a, b],
    queryFn: () => api.compare(a, b),
    enabled: validPair,
    refetchInterval: ({ state }) => state.data && [state.data.a, state.data.b].some((j) => j.status === 'queued' || j.status === 'running' || j.recipe?.pending) ? 5000 : false,
  })
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Link to="/jobs" className="relative inline-flex items-center gap-0.5 text-ui text-fg-3 before:absolute before:-inset-x-2 before:-inset-y-3 hover:text-fg"><ChevronLeft className="size-4" aria-hidden />{t('job.back')}</Link>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h1 className="text-lg font-semibold">{t('compare.title')}</h1>
          {validPair && <Link to={`/compare?a=${a}`} className={buttonClass('secondary', 'sm')}>{t('compare.change_job')}</Link>}
        </div>
      </div>
      {!validA || (!choosing && !validPair) ? (
        <Card><Empty action={<Link to="/jobs" className={buttonClass('secondary', 'sm')}>{t('job.back_to_list')}</Link>}>{t('compare.pick_two')}</Empty></Card>
      ) : choosing ? (
        <ChooseJob key={a} id={a} />
      ) : (
        <>
          <ErrorBox error={query.error} />
          {query.isLoading && <Card><SkeletonRows rows={6} /></Card>}
          {query.data && <Comparison key={`${a}:${b}`} data={query.data} />}
        </>
      )}
    </div>
  )
}

function ChooseJob({ id }: { id: number }) {
  const job = useQuery({ queryKey: ['job', id], queryFn: () => api.job(id) })
  const workdir = job.data?.workdir
  const recent = useQuery({
    queryKey: ['jobs', 'compare-candidates', workdir],
    queryFn: () => api.jobs({ workdir, size: 12 }),
    enabled: !!workdir,
  })
  const choices = recent.data?.jobs.filter((j) => j.id !== id)
  return (
    <>
      <ErrorBox error={job.error || recent.error} />
      {job.data && <Card><JobHeading job={job.data} label={t('compare.base_job')} /></Card>}
      <Section title={t('compare.recent_title')}>
        {job.isLoading || recent.isLoading ? <SkeletonRows rows={4} /> : choices?.length ? (
          <ul className="divide-y divide-line">
            {choices.map((j) => (
              <li key={j.id}>
                <Link to={`/compare?a=${id}&b=${j.id}`} className="flex min-h-11 items-center gap-3 px-3 py-2 text-sm hover:bg-subtle sm:px-4">
                  <StatusLabel job={j} className="shrink-0" />
                  <div className="min-w-0 flex-1 sm:flex sm:items-baseline sm:gap-3">
                    <span className="block min-w-0 flex-1 truncate" title={j.name}><span className="mr-2 text-fg-3">#{j.id}</span>{j.name}</span>
                    <span className="block shrink-0 text-xs text-fg-3">{fmtTime(j.created_at)}</span>
                  </div>
                  <ChevronRight className="size-4 shrink-0 text-fg-3" aria-hidden />
                </Link>
              </li>
            ))}
          </ul>
        ) : !job.error && !recent.error && <Empty>{t('compare.no_candidates')}</Empty>}
      </Section>
    </>
  )
}

function JobHeading({ job, label }: { job: Job; label: string }) {
  return (
    <div className="min-w-0 px-3 py-2.5 sm:px-4">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-fg-3">
        {label} <StatusLabel job={job} className="font-normal" />
      </div>
      <Link to={`/jobs/${job.id}`} className="relative mt-0.5 block text-sm font-medium before:absolute before:inset-x-0 before:-inset-y-3 hover:underline [overflow-wrap:anywhere]">
        <span className="mr-2 text-fg-3">#{job.id}</span>{job.name}
      </Link>
      <div className="mt-0.5 break-all font-mono text-xs text-fg-3">{job.workdir}</div>
      {job.memo && <div className="mt-1 whitespace-pre-wrap text-ui text-fg-2 [overflow-wrap:anywhere]">{job.memo}</div>}
    </div>
  )
}

function Section({ title, count, children }: { title: string; count?: number; children: ReactNode }) {
  return (
    <section>
      <Card className="overflow-hidden">
        <h2 className="flex items-center gap-2 border-b border-line bg-subtle px-3 py-2 text-ui font-semibold sm:px-4">
          {title}{count !== undefined && <span className="text-xs font-normal text-fg-3">{t('compare.diff_count', { n: count })}</span>}
        </h2>
        {children}
      </Card>
    </section>
  )
}

function Note({ children }: { children: ReactNode }) {
  return <p className="px-3 py-2.5 text-ui text-fg-3 sm:px-4">{children}</p>
}

function Comparison({ data }: { data: JobComparison }) {
  const { a, b, code } = data
  const gitRows: ValueChange[] = [
    { name: 'Commit', a: a.git_commit || t('compare.no_record'), b: b.git_commit || t('compare.no_record') },
    { name: t('compare.branch'), a: a.git_branch || t('compare.no_record'), b: b.git_branch || t('compare.no_record') },
    { name: t('compare.dirty'), a: dirty(a), b: dirty(b) },
  ]
  // 代码的差异数把 commit、分支、未提交改动也算进去：commit 不同却写"0 项差异"，正好在证明"跑的是哪版代码"的地方自相矛盾。
  // 文件读不出来时不给数字，免得只数了 git 信息看起来像全部
  const codeDiffs = code.files ? code.files.length + gitRows.filter((r) => r.a !== r.b).length : undefined
  return (
    <>
      <Card className="grid divide-y divide-line sm:grid-cols-2 sm:divide-x sm:divide-y-0">
        <JobHeading job={a} label={t('compare.base_job')} />
        <JobHeading job={b} label={t('compare.other_job')} />
      </Card>
      <Section title={t('job.row_cmd')}>
        <CommandDiff data={data} />
      </Section>
      <Section title={t('job.row_env')} count={data.env.length}>
        <ValueRows rows={data.env} a={a.id} b={b.id} />
      </Section>
      <Section title={t('job.meta_code_short')} count={codeDiffs}>
        <PairTable a={a.id} b={b.id} rows={gitRows} />
        {code.error ? <Note>{code.error}</Note> : (
          <>
            {code.partial && <Note>{t('compare.partial_note')}</Note>}
            {code.files?.length ? (
              <div className="divide-y divide-line border-t border-line">
                {code.files.map((file) => <FileRow key={file.path} file={file} a={a.id} b={b.id} />)}
              </div>
            ) : <Note>{t('compare.files_same')}</Note>}
          </>
        )}
      </Section>
      <Section title={t('job.row_env_package')} count={data.packages?.length}>
        {data.packages_error ? <Note>{data.packages_error}</Note> : <ValueRows rows={data.packages ?? []} a={a.id} b={b.id} />}
      </Section>
      <Section title={t('compare.machine_title')}>
        <PairTable a={a.id} b={b.id} rows={[
          { name: t('job.meta_conda'), a: a.conda_env || t('compare.unused'), b: b.conda_env || t('compare.unused') },
          { name: t('compare.bashrc'), a: a.inherit_bashrc ? t('compare.yes') : t('compare.no'), b: b.inherit_bashrc ? t('compare.yes') : t('compare.no') },
          { name: t('compare.gpu_request'), a: gpuRequest(a), b: gpuRequest(b) },
          { name: t('compare.gpu_assigned'), a: gpuAssigned(a), b: gpuAssigned(b) },
          { name: t('compare.hostname'), a: a.recipe?.machine?.hostname || t('compare.no_record'), b: b.recipe?.machine?.hostname || t('compare.no_record') },
          { name: t('compare.driver'), a: a.recipe?.machine?.driver_version || t('compare.no_record'), b: b.recipe?.machine?.driver_version || t('compare.no_record') },
          { name: 'CUDA', a: a.recipe?.machine?.cuda_version || t('compare.no_record'), b: b.recipe?.machine?.cuda_version || t('compare.no_record') },
        ]} />
      </Section>
      <Section title={t('job.row_resources')}>
        <div className="grid divide-y divide-line sm:grid-cols-2 sm:divide-x sm:divide-y-0">
          {[a, b].map((job) => (
            <div key={job.id} className="min-w-0 space-y-1 px-3 py-2.5 sm:px-4">
              <div className="text-xs text-fg-3">#{job.id}</div>
              <ProfileSummary job={job} />
            </div>
          ))}
        </div>
      </Section>
    </>
  )
}

function dirty(job: Job) {
  return job.git_dirty ? t('compare.present') : job.git_commit || job.git_branch ? t('compare.absent') : t('compare.no_record')
}

function gpuRequest(job: Job) {
  return t('compare.gpu_count', { n: job.gpus_req }) + (job.gpus_pinned.length ? t('compare.gpu_pinned_suffix', { list: job.gpus_pinned.join(', ') }) : '')
}

function gpuAssigned(job: Job) {
  if (!job.started_at) return t('compare.gpu_unassigned')
  const gpus = job.gpus_assigned.map((id) => {
    const name = job.recipe?.machine?.gpus?.find((gpu) => gpu.index === id)?.name
    return name ? `GPU ${id} · ${name}` : t('compare.gpu_no_model', { id })
  })
  return gpus.length ? `${t('compare.gpu_count', { n: gpus.length })}\n${gpus.join('\n')}` : t('compare.gpu_count', { n: 0 })
}

function CommandDiff({ data }: { data: JobComparison }) {
  const parts = useMemo(() => diffArrays(data.command_a, data.command_b, { timeout: 80 }), [data.command_a, data.command_b])
  return (
    <>
      <div className={cx(pane, 'grid divide-y divide-white/15 sm:grid-cols-2 sm:divide-x sm:divide-y-0')}>
        {(['a', 'b'] as const).map((side) => (
          <div key={side} className="min-w-0">
            <div className="border-b border-white/15 px-3 py-1.5">#{data[side].id} · {t(side === 'a' ? 'compare.base' : 'compare.other')}</div>
            <pre className={cx(cell, 'px-3 py-2.5')}>{!parts ? data[side].cmd : parts.map((part, index) => {
              if (side === 'a' ? part.added : part.removed) return null
              const value = part.value.join('')
              if (part.removed) return <del key={index} className="bg-white/15 text-white decoration-white/70">{value}</del>
              if (part.added) return <ins key={index} className="bg-white/20 text-white decoration-white/70">{value}</ins>
              return value
            })}</pre>
          </div>
        ))}
      </div>
      {!parts && <Note>{t('compare.command_too_different')}</Note>}
    </>
  )
}

function ValueRows({ rows, a, b }: { rows: ValueChange[]; a: number; b: number }) {
  if (!rows.length) return <Note>{t('compare.no_diff')}</Note>
  return <PairTable a={a} b={b} rows={rows} changesOnly />
}

function PairTable({ a, b, rows, changesOnly }: { a: number; b: number; rows: ValueChange[]; changesOnly?: boolean }) {
  return (
    <table className="w-full table-fixed text-left text-xs">
      <thead className="border-b border-line text-fg-3">
        <tr>
          <th scope="col" className="w-[34%] px-3 py-1.5 font-normal sm:w-[25%] sm:px-4">{t('compare.col_item')}</th>
          <th scope="col" className="px-2 py-1.5 font-normal">#{a} {t('compare.base')}</th>
          <th scope="col" className="px-2 py-1.5 font-normal">#{b} {t('compare.other')}</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-line">
        {rows.map((row) => (
          <tr key={row.name} className={cx(!changesOnly && row.a !== row.b && 'bg-subtle')}>
            <th scope="row" className={cx(cell, 'px-3 py-2 align-top font-medium sm:px-4', changesOnly && 'font-mono')}>
              {row.name}
              {changesOnly && <span className="mt-0.5 block font-sans font-normal text-fg-3">{t(row.a === null ? 'compare.added' : row.b === null ? 'compare.removed' : 'compare.changed')}</span>}
            </th>
            <td className={cx(cell, 'px-2 py-2 align-top font-mono text-fg-2')}>{showValue(row.a)}</td>
            <td className={cx(cell, 'px-2 py-2 align-top font-mono text-fg-2')}>{showValue(row.b)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function showValue(value: string | null) {
  if (value === null) return <span className="text-fg-3">{t('compare.value_none')}</span>
  if (!value) return <span className="text-fg-3">{t('compare.value_empty')}</span>
  return value
}

function FileRow({ file, a, b }: { file: FileChange; a: number; b: number }) {
  const [open, setOpen] = useState(false)
  return (
    <details className="group" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="flex min-h-11 cursor-pointer items-center gap-2 px-3 py-2 text-ui hover:bg-subtle sm:px-4">
        <ChevronRight className="size-4 shrink-0 text-fg-3 group-open:rotate-90" aria-hidden />
        <span className="min-w-0 flex-1 font-mono [overflow-wrap:anywhere]">{file.path}</span>
        <span className="shrink-0 text-xs text-fg-3">{t(fileLabelKeys[file.kind])}</span>
      </summary>
      {open && <FileDiff file={file} a={a} b={b} />}
    </details>
  )
}

/** 差异说明存目录键、渲染时再翻译：查询缓存跨语言切换存活，取数时合成文案会被冻结 */
type FileDiffData =
  | { lines: string[]; note?: undefined }
  | { note: 'compare.diff_binary' | 'compare.diff_too_large' | 'compare.diff_huge' | 'compare.diff_no_text'; lines?: undefined }

function FileDiff({ file, a, b }: { file: FileChange; a: number; b: number }) {
  const [limit, setLimit] = useState(300)
  const query = useQuery({
    queryKey: ['compare-file', a, b, file.path, file.kind],
    queryFn: async (): Promise<FileDiffData> => {
      const [before, after] = await Promise.all([
        file.kind === 'added' ? null : api.snapshotFile(a, file.path),
        file.kind === 'removed' ? null : api.snapshotFile(b, file.path),
      ])
      if (before?.binary || after?.binary) return { note: 'compare.diff_binary' }
      if ((before?.size ?? 0) > 2 * 1024 * 1024 || (after?.size ?? 0) > 2 * 1024 * 1024) {
        return { note: 'compare.diff_too_large' }
      }
      const patch = structuredPatch(file.path, file.path, before?.content ?? '', after?.content ?? '', undefined, undefined, { context: 3, timeout: 120 })
      if (!patch) return { note: 'compare.diff_huge' }
      if (!patch.hunks.length) return { note: 'compare.diff_no_text' }
      return { lines: patch.hunks.flatMap((hunk) => [`@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`, ...hunk.lines]) }
    },
  })
  if (query.isLoading) return <Note>{t('compare.diff_loading')}</Note>
  if (query.error) return <div className="px-3 pb-2 sm:px-4"><ErrorBox error={query.error} /></div>
  const lines = query.data?.lines
  if (!lines) return <Note>{query.data?.note ? t(query.data.note) : null}</Note>
  return (
    <div className={cx(pane, 'border-t border-white/15')}>
      <pre className="max-h-96 overflow-auto py-1">
        {lines.slice(0, limit).map((line, i) => (
          <span key={i} className={cx('block min-w-max px-3', line.startsWith('+') && 'bg-white/15 text-white', line.startsWith('-') && 'bg-white/8', line.startsWith('@@') && 'text-white/65')}>
            {line}
          </span>
        ))}
      </pre>
      {lines.length > limit && (
        <Button size="sm" variant="ghost" className="m-1 text-white/80 hover:bg-white/10 hover:text-white" onClick={() => setLimit(limit + 300)}>
          {t('compare.show_more', { n: Math.min(300, lines.length - limit) })}
        </Button>
      )}
    </div>
  )
}
