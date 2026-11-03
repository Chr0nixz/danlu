import { useQuery } from '@tanstack/react-query'
import { diffArrays, structuredPatch } from 'diff'
import { ArrowLeft, ChevronRight } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import { Button, Card, Empty, ErrorBox, SkeletonRows, StatusLabel, buttonClass, cx } from '../components/ui'
import { fmtTime, type FileChange, type Job, type JobComparison, type ValueChange } from '@danlu/core'
import { api } from '../lib/api'

const fileLabels = { added: '新增', removed: '删除', changed: '改动' } as const
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
        <Link to="/jobs" className="inline-flex items-center gap-1 text-ui text-fg-3 hover:text-fg"><ArrowLeft className="size-4" aria-hidden />任务</Link>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h1 className="text-lg font-semibold">任务对比</h1>
          {validPair && <Link to={`/compare?a=${a}`} className={buttonClass('secondary', 'sm')}>更换对比任务</Link>}
        </div>
      </div>
      {!validA || (!choosing && !validPair) ? (
        <Card><Empty action={<Link to="/jobs" className={buttonClass('secondary', 'sm')}>回到任务列表</Link>}>请在任务列表中选择两个不同的任务</Empty></Card>
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
      {job.data && <Card><JobHeading job={job.data} label="基准任务" /></Card>}
      <Section title="同一工作目录的最近任务">
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
        ) : !job.error && !recent.error && <Empty>这个工作目录还没有其他任务</Empty>}
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
      <Link to={`/jobs/${job.id}`} className="mt-0.5 block text-sm font-medium hover:underline [overflow-wrap:anywhere]">
        <span className="mr-2 text-fg-3">#{job.id}</span>{job.name}
      </Link>
      <div className="mt-0.5 break-all font-mono text-xs text-fg-3">{job.workdir}</div>
    </div>
  )
}

function Section({ title, count, children }: { title: string; count?: number; children: ReactNode }) {
  return (
    <section>
      <Card className="overflow-hidden">
        <h2 className="flex items-center gap-2 border-b border-line bg-subtle px-3 py-2 text-ui font-semibold sm:px-4">
          {title}{count !== undefined && <span className="text-xs font-normal text-fg-3">{count} 项差异</span>}
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
  return (
    <>
      <Card className="grid divide-y divide-line sm:grid-cols-2 sm:divide-x sm:divide-y-0">
        <JobHeading job={a} label="基准任务" />
        <JobHeading job={b} label="对比任务" />
      </Card>
      <Section title="命令">
        <CommandDiff data={data} />
      </Section>
      <Section title="环境变量" count={data.env.length}>
        <ValueRows rows={data.env} a={a.id} b={b.id} />
      </Section>
      <Section title="代码" count={code.files?.length}>
        <PairTable a={a.id} b={b.id} rows={[
          { name: 'Commit', a: a.git_commit || '无记录', b: b.git_commit || '无记录' },
          { name: '分支', a: a.git_branch || '无记录', b: b.git_branch || '无记录' },
          { name: '未提交改动', a: dirty(a), b: dirty(b) },
        ]} />
        {code.error ? <Note>{code.error}</Note> : (
          <>
            {code.partial && <Note>有文件未收进快照。以下差异只反映已保存的文件。</Note>}
            {code.files?.length ? (
              <div className="divide-y divide-line border-t border-line">
                {code.files.map((file) => <FileRow key={file.path} file={file} a={a.id} b={b.id} />)}
              </div>
            ) : <Note>已保存的文件内容相同</Note>}
          </>
        )}
      </Section>
      <Section title="环境包" count={data.packages?.length}>
        {data.packages_error ? <Note>{data.packages_error}</Note> : <ValueRows rows={data.packages ?? []} a={a.id} b={b.id} />}
      </Section>
      <Section title="机器和运行环境">
        <PairTable a={a.id} b={b.id} rows={[
          { name: 'conda 环境', a: a.conda_env || '未使用', b: b.conda_env || '未使用' },
          { name: '加载 .bashrc', a: a.inherit_bashrc ? '是' : '否', b: b.inherit_bashrc ? '是' : '否' },
          { name: '申请 GPU', a: gpuRequest(a), b: gpuRequest(b) },
          { name: '分配 GPU', a: gpuAssigned(a), b: gpuAssigned(b) },
          { name: '主机名', a: a.recipe?.machine?.hostname || '无记录', b: b.recipe?.machine?.hostname || '无记录' },
          { name: '驱动', a: a.recipe?.machine?.driver_version || '无记录', b: b.recipe?.machine?.driver_version || '无记录' },
          { name: 'CUDA', a: a.recipe?.machine?.cuda_version || '无记录', b: b.recipe?.machine?.cuda_version || '无记录' },
        ]} />
      </Section>
    </>
  )
}

function dirty(job: Job) {
  return job.git_dirty ? '有' : job.git_commit || job.git_branch ? '无' : '无记录'
}

function gpuRequest(job: Job) {
  return `${job.gpus_req} 张${job.gpus_pinned.length ? ` · 指定 ${job.gpus_pinned.join(', ')}` : ''}`
}

function gpuAssigned(job: Job) {
  if (!job.started_at) return '尚未分配'
  const gpus = job.gpus_assigned.map((id) => {
    const name = job.recipe?.machine?.gpus?.find((gpu) => gpu.index === id)?.name
    return name ? `GPU ${id} · ${name}` : `GPU ${id} · 型号无记录`
  })
  return gpus.length ? `${gpus.length} 张\n${gpus.join('\n')}` : '0 张'
}

function CommandDiff({ data }: { data: JobComparison }) {
  const parts = useMemo(() => diffArrays(data.command_a, data.command_b, { timeout: 80 }), [data.command_a, data.command_b])
  return (
    <>
      <div className={cx(pane, 'grid divide-y divide-white/15 sm:grid-cols-2 sm:divide-x sm:divide-y-0')}>
        {(['a', 'b'] as const).map((side) => (
          <div key={side} className="min-w-0">
            <div className="border-b border-white/15 px-3 py-1.5">#{data[side].id} · {side === 'a' ? '基准' : '对比'}</div>
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
      {!parts && <Note>命令差异较大，已并列显示完整命令。</Note>}
    </>
  )
}

function ValueRows({ rows, a, b }: { rows: ValueChange[]; a: number; b: number }) {
  if (!rows.length) return <Note>没有差异</Note>
  return <PairTable a={a} b={b} rows={rows} changesOnly />
}

function PairTable({ a, b, rows, changesOnly }: { a: number; b: number; rows: ValueChange[]; changesOnly?: boolean }) {
  return (
    <table className="w-full table-fixed text-left text-xs">
      <thead className="border-b border-line text-fg-3">
        <tr>
          <th scope="col" className="w-[34%] px-3 py-1.5 font-normal sm:w-[25%] sm:px-4">项目</th>
          <th scope="col" className="px-2 py-1.5 font-normal">#{a} 基准</th>
          <th scope="col" className="px-2 py-1.5 font-normal">#{b} 对比</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-line">
        {rows.map((row) => (
          <tr key={row.name} className={cx(!changesOnly && row.a !== row.b && 'bg-subtle')}>
            <th scope="row" className={cx(cell, 'px-3 py-2 align-top font-medium sm:px-4', changesOnly && 'font-mono')}>
              {row.name}
              {changesOnly && <span className="mt-0.5 block font-sans font-normal text-fg-3">{row.a === null ? '新增' : row.b === null ? '删除' : '改动'}</span>}
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
  if (value === null) return <span className="text-fg-3">无</span>
  if (!value) return <span className="text-fg-3">空值</span>
  return value
}

function FileRow({ file, a, b }: { file: FileChange; a: number; b: number }) {
  const [open, setOpen] = useState(false)
  return (
    <details className="group" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="flex min-h-11 cursor-pointer items-center gap-2 px-3 py-2 text-ui hover:bg-subtle sm:px-4">
        <ChevronRight className="size-4 shrink-0 text-fg-3 group-open:rotate-90" aria-hidden />
        <span className="min-w-0 flex-1 font-mono [overflow-wrap:anywhere]">{file.path}</span>
        <span className="shrink-0 text-xs text-fg-3">{fileLabels[file.kind]}</span>
      </summary>
      {open && <FileDiff file={file} a={a} b={b} />}
    </details>
  )
}

function FileDiff({ file, a, b }: { file: FileChange; a: number; b: number }) {
  const [limit, setLimit] = useState(300)
  const query = useQuery({
    queryKey: ['compare-file', a, b, file.path, file.kind],
    queryFn: async () => {
      const [before, after] = await Promise.all([
        file.kind === 'added' ? null : api.snapshotFile(a, file.path),
        file.kind === 'removed' ? null : api.snapshotFile(b, file.path),
      ])
      if (before?.binary || after?.binary) return { note: '二进制文件没有文本差异可显示。' }
      if ((before?.size ?? 0) > 2 * 1024 * 1024 || (after?.size ?? 0) > 2 * 1024 * 1024) {
        return { note: '文件超过 2 MiB。可在任务的代码页下载快照查看完整内容。' }
      }
      const patch = structuredPatch(file.path, file.path, before?.content ?? '', after?.content ?? '', undefined, undefined, { context: 3, timeout: 120 })
      if (!patch) return { note: '文件差异过大。可在任务的代码页下载快照查看完整内容。' }
      if (!patch.hunks.length) return { note: '没有文本行差异。' }
      return { lines: patch.hunks.flatMap((hunk) => [`@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`, ...hunk.lines]) }
    },
  })
  if (query.isLoading) return <Note>正在读取快照文件…</Note>
  if (query.error) return <div className="px-3 pb-2 sm:px-4"><ErrorBox error={query.error} /></div>
  const lines = query.data?.lines
  if (!lines) return <Note>{query.data?.note}</Note>
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
          再显示 {Math.min(300, lines.length - limit)} 行
        </Button>
      )}
    </div>
  )
}
