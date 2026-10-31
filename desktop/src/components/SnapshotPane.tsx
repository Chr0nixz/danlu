import { bytes, type Api } from '@danlu/core'
import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, Download, FileText } from 'lucide-react'
import { useState } from 'react'
import { apiFor } from '../lib/backend'
import { cx, ErrorText, Segmented } from './ui'

/** 代码快照：任务启动时保存的 git 改动和文件。和日志一样放在深色终端里 */
export function SnapshotPane({ server, jobId, onDownload }: { server: string; jobId: number; onDownload: () => void }) {
  const api = apiFor(server)
  const meta = useQuery({ queryKey: ['snapshot', server, jobId], queryFn: () => api.snapshot(jobId), staleTime: Infinity, retry: false })
  const [view, setView] = useState<'diff' | 'files'>('diff')
  const [file, setFile] = useState<string | null>(null)
  const hasDiff = meta.data?.has_diff ?? false

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] bg-term text-[#d6d7dc] shadow-[inset_0_0_0_1px_rgb(255_255_255/0.06)]">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-white/[0.06] pr-1.5 pl-2.5 text-[11.5px] text-[#8b8e97]">
        {file ? (
          <button type="button" onClick={() => setFile(null)} className="inline-flex items-center gap-1 rounded-[5px] px-1 hover:text-white">
            <ArrowLeft className="size-3.5" />
            <span className="num truncate">{file}</span>
          </button>
        ) : (
          <div className="[&_[role=radiogroup]]:bg-white/[0.06] [&_[aria-checked=false]]:text-[#8b8e97] [&_[aria-checked=true]]:bg-white/[0.12] [&_[aria-checked=true]]:text-white [&_[aria-checked=true]]:shadow-none">
            <Segmented
              value={hasDiff ? view : 'files'}
              onChange={setView}
              options={[
                { value: 'diff', label: '未提交的改动', disabled: !hasDiff },
                { value: 'files', label: `文件 ${meta.data?.meta.files.length ?? ''}` },
              ]}
            />
          </div>
        )}
        <span className="ml-auto" />
        <button
          type="button"
          onClick={onDownload}
          title="下载快照（tar.gz）"
          className="inline-flex h-6 items-center rounded-[5px] px-1.5 hover:bg-white/10 hover:text-white [&_svg]:size-3.5"
        >
          <Download />
        </button>
      </div>
      <div className="selectable min-h-0 flex-1 overflow-auto font-mono text-[11.5px] leading-[1.6]">
        {meta.isLoading ? (
          <p className="p-3 text-[#8b8e97]">读取中…</p>
        ) : meta.error ? (
          <ErrorText error={meta.error} className="p-3" />
        ) : file ? (
          <FileView api={api} server={server} jobId={jobId} path={file} />
        ) : hasDiff && view === 'diff' ? (
          <DiffView api={api} server={server} jobId={jobId} />
        ) : (
          <FileList files={meta.data!.meta.files} skipped={meta.data!.meta.skip_count} truncated={meta.data!.meta.truncated} onOpen={setFile} />
        )}
      </div>
    </div>
  )
}

function FileList({ files, skipped, truncated, onOpen }: { files: { path: string; size: number }[]; skipped: number; truncated: boolean; onOpen: (p: string) => void }) {
  return (
    <div className="py-1">
      {files.map((f) => (
        <button
          key={f.path}
          type="button"
          onClick={() => onOpen(f.path)}
          className="flex h-6 w-full items-center gap-2 px-3 text-left hover:bg-white/[0.05]"
        >
          <FileText className="size-3.5 shrink-0 text-[#6b6e77]" />
          <span className="min-w-0 flex-1 truncate">{f.path}</span>
          <span className="shrink-0 text-[#6b6e77]">{bytes(f.size)}</span>
        </button>
      ))}
      {(skipped > 0 || truncated) && (
        <p className="px-3 pt-2 pb-1 font-sans text-[#8b8e97]">
          {skipped > 0 && `有 ${skipped} 个文件太大或被忽略，没有保存。`}
          {truncated && '快照到了大小上限，后面的文件没有保存。'}
        </p>
      )}
    </div>
  )
}

function FileView({ api, server, jobId, path }: { api: Api; server: string; jobId: number; path: string }) {
  const q = useQuery({ queryKey: ['snapshot-file', server, jobId, path], queryFn: () => api.snapshotFile(jobId, path), staleTime: Infinity })
  if (q.isLoading) return <p className="p-3 text-[#8b8e97]">读取中…</p>
  if (q.error) return <ErrorText error={q.error} className="p-3" />
  if (q.data!.binary) return <p className="p-3 font-sans text-[#8b8e97]">二进制文件，{bytes(q.data!.size)}</p>
  const lines = q.data!.content.split('\n')
  return (
    <table className="w-full border-collapse">
      <tbody>
        {lines.map((l, i) => (
          <tr key={i}>
            <td className="w-10 pr-3 pl-2 text-right align-top text-[#4d5058] select-none">{i + 1}</td>
            <td className="pr-3 whitespace-pre-wrap [overflow-wrap:anywhere]">{l}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function DiffView({ api, server, jobId }: { api: Api; server: string; jobId: number }) {
  const q = useQuery({ queryKey: ['snapshot-diff', server, jobId], queryFn: () => api.snapshotDiff(jobId), staleTime: Infinity })
  if (q.isLoading) return <p className="p-3 text-[#8b8e97]">读取中…</p>
  if (q.error) return <ErrorText error={q.error} className="p-3" />
  return (
    <pre className="py-1">
      {q.data!.diff.split('\n').map((l, i) => (
        <div
          key={i}
          className={cx(
            'px-3 whitespace-pre-wrap [overflow-wrap:anywhere]',
            l.startsWith('diff --git') && 'mt-2 font-semibold text-white first:mt-0',
            l.startsWith('@@') && 'text-[#79c0ff]',
            l.startsWith('+') && !l.startsWith('+++') && 'bg-[#4fd18b]/[0.08] text-[#7ee787]',
            l.startsWith('-') && !l.startsWith('---') && 'bg-[#ff6b6b]/[0.08] text-[#ffa198]',
            (l.startsWith('+++') || l.startsWith('---') || l.startsWith('index ')) && 'text-[#8b8e97]',
          )}
        >
          {l || ' '}
        </div>
      ))}
    </pre>
  )
}
