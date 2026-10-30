import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Download, FileText, Folder, GitBranch, GitCommitHorizontal } from 'lucide-react'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import cpp from 'highlight.js/lib/languages/cpp'
import diff from 'highlight.js/lib/languages/diff'
import ini from 'highlight.js/lib/languages/ini'
import json from 'highlight.js/lib/languages/json'
import markdown from 'highlight.js/lib/languages/markdown'
import python from 'highlight.js/lib/languages/python'
import yaml from 'highlight.js/lib/languages/yaml'
import 'highlight.js/styles/github-dark.css'
import { useMemo, useState } from 'react'
import { api, ApiError, type SnapshotMeta } from '../lib/api'
import { bytes } from '../lib/format'
import { buttonClass, Card, cx, ErrorBox, Segmented, Spinner } from './ui'

hljs.registerLanguage('python', python)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('json', json)
hljs.registerLanguage('ini', ini)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('cpp', cpp)
hljs.registerLanguage('diff', diff)

const langByExt: Record<string, string> = {
  py: 'python', pyi: 'python', sh: 'bash', bash: 'bash', yaml: 'yaml', yml: 'yaml', json: 'json',
  toml: 'ini', cfg: 'ini', ini: 'ini', md: 'markdown', c: 'cpp', cc: 'cpp', cpp: 'cpp', h: 'cpp', hpp: 'cpp', cu: 'cpp', cuh: 'cpp',
}

function highlight(code: string, lang?: string) {
  if (lang && hljs.getLanguage(lang)) return hljs.highlight(code, { language: lang }).value
  return code.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c]!)
}

const chip = 'inline-flex h-7 items-center gap-1 rounded-md border border-line bg-surface px-2 text-fg-2'

export function CodeBrowser({ jobId }: { jobId: number }) {
  const q = useQuery({ queryKey: ['snapshot', jobId], queryFn: () => api.snapshot(jobId), retry: false })
  const [tab, setTab] = useState<'files' | 'diff' | 'skipped'>('files')

  if (q.isLoading) return <Spinner />
  if (q.error) {
    const e = q.error as ApiError
    return <div className="rounded-lg border border-dashed border-line-strong px-4 py-10 text-center text-ui text-fg-3">{e.message}</div>
  }
  const { meta, has_diff, note } = q.data!
  const g = meta.git

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        {g ? (
          <>
            {g.branch && (
              <span className={chip}>
                <GitBranch className="size-3.5" />
                {g.branch}
              </span>
            )}
            {g.commit && (
              <span className={cx(chip, 'font-mono')} title={g.commit}>
                <GitCommitHorizontal className="size-3.5" />
                {g.commit.slice(0, 10)}
              </span>
            )}
            {g.dirty && (
              <span className={cx(chip, 'border-warn/30 bg-warn/8 font-medium text-warn')}>
                有未提交的改动
              </span>
            )}
          </>
        ) : (
          <span className={chip}>不是 git 仓库，按扩展名保存了代码和配置文件</span>
        )}
        <span className="text-fg-3">
          {meta.files.length} 个文件 · {bytes(meta.total_size)}
        </span>
        <a
          href={`/api/jobs/${jobId}/snapshot/download`}
          className={cx(buttonClass('ghost', 'sm'), 'ml-auto')}
        >
          <Download />
          下载 tar.gz
        </a>
      </div>
      {note && <div className="text-xs text-warn">{note}</div>}

      <Segmented
        label="快照内容"
        value={tab}
        onChange={setTab}
        options={[
          { value: 'files', label: '文件' },
          ...(has_diff ? [{ value: 'diff' as const, label: '未提交的改动' }] : []),
          ...(meta.skip_count ? [{ value: 'skipped' as const, label: `未保存 (${meta.skip_count})` }] : []),
        ]}
      />
      {tab === 'files' && <Files jobId={jobId} meta={meta} />}
      {tab === 'diff' && <DiffView jobId={jobId} />}
      {tab === 'skipped' && (
        <Card className="divide-y divide-line text-ui">
          {meta.skipped.map((s) => (
            <div key={s.path} className="flex justify-between gap-3 px-3 py-2">
              <span className="truncate font-mono text-xs">{s.path}</span>
              <span className="shrink-0 text-xs text-fg-3">{s.reason}</span>
            </div>
          ))}
          {meta.skip_count > meta.skipped.length && <div className="px-3 py-2 text-xs text-fg-3">……共 {meta.skip_count} 个</div>}
        </Card>
      )}
    </div>
  )
}

type Node = { name: string; path: string; children: Map<string, Node>; size?: number; isFile: boolean }

function buildTree(files: { path: string; size: number }[]): Node {
  const root: Node = { name: '', path: '', children: new Map(), isFile: false }
  for (const f of files) {
    let cur = root
    const parts = f.path.split('/')
    parts.forEach((p, i) => {
      const isFile = i === parts.length - 1
      if (!cur.children.has(p)) {
        cur.children.set(p, { name: p, path: parts.slice(0, i + 1).join('/'), children: new Map(), isFile, size: isFile ? f.size : undefined })
      }
      cur = cur.children.get(p)!
    })
  }
  return root
}

function sortedChildren(n: Node) {
  return [...n.children.values()].sort((a, b) => Number(a.isFile) - Number(b.isFile) || a.name.localeCompare(b.name))
}

function pickDefault(files: { path: string }[]) {
  for (const want of ['train.py', 'main.py', 'run.py', 'README.md']) {
    const f = files.find((x) => x.path === want)
    if (f) return f.path
  }
  return files.find((f) => f.path.endsWith('.py'))?.path ?? files[0]?.path ?? ''
}

function Files({ jobId, meta }: { jobId: number; meta: SnapshotMeta }) {
  const tree = useMemo(() => buildTree(meta.files), [meta.files])
  const [sel, setSel] = useState(() => pickDefault(meta.files))
  const expandAll = meta.files.length <= 60
  if (!meta.files.length) return <div className="text-ui text-fg-3">快照里没有文件</div>
  return (
    <div className="grid overflow-hidden rounded-lg border border-line md:grid-cols-[260px_1fr]">
      <div className="max-h-56 overflow-auto border-b border-line bg-surface py-1.5 text-ui md:max-h-[68vh] md:border-b-0 md:border-r">
        {sortedChildren(tree).map((n) => (
          <TreeNode key={n.path} node={n} depth={0} sel={sel} onSelect={setSel} defaultOpen={expandAll} />
        ))}
      </div>
      <FileView jobId={jobId} path={sel} />
    </div>
  )
}

function TreeNode({ node, depth, sel, onSelect, defaultOpen }: { node: Node; depth: number; sel: string; onSelect: (p: string) => void; defaultOpen: boolean }) {
  const [open, setOpen] = useState(defaultOpen || depth === 0 && node.children.size < 15)
  const pad = { paddingLeft: 8 + depth * 14 }
  if (node.isFile) {
    return (
      <button
        onClick={() => onSelect(node.path)}
        style={pad}
        className={cx(
          'flex w-full items-center gap-1.5 py-1 pr-2 text-left',
          sel === node.path ? 'bg-hover font-medium text-fg' : 'text-fg-2 hover:bg-subtle hover:text-fg',
        )}
      >
        <FileText className="size-3.5 shrink-0 text-fg-3" />
        <span className="truncate">{node.name}</span>
      </button>
    )
  }
  return (
    <div>
      <button onClick={() => setOpen((o) => !o)} style={pad} className="flex w-full items-center gap-1 py-1 pr-2 text-left text-fg-2 hover:bg-subtle hover:text-fg">
        {open ? <ChevronDown className="size-3.5 shrink-0 text-fg-3" /> : <ChevronRight className="size-3.5 shrink-0 text-fg-3" />}
        <Folder className="size-3.5 shrink-0 text-fg-3" />
        <span className="truncate">{node.name}</span>
      </button>
      {open && sortedChildren(node).map((c) => <TreeNode key={c.path} node={c} depth={depth + 1} sel={sel} onSelect={onSelect} defaultOpen={defaultOpen} />)}
    </div>
  )
}

function FileView({ jobId, path }: { jobId: number; path: string }) {
  const q = useQuery({ queryKey: ['snapfile', jobId, path], queryFn: () => api.snapshotFile(jobId, path), enabled: !!path })
  const html = useMemo(() => {
    if (!q.data || q.data.binary) return ''
    return highlight(q.data.content, langByExt[path.split('.').pop()?.toLowerCase() ?? ''])
  }, [q.data, path])
  // 行号：traceback 里引用的就是行号
  const lines = useMemo(() => {
    const c = q.data && !q.data.binary ? q.data.content : ''
    const n = c ? c.split('\n').length - (c.endsWith('\n') ? 1 : 0) : 0
    return Array.from({ length: n }, (_, i) => i + 1).join('\n')
  }, [q.data])
  return (
    <div className="term min-w-0">
      <div className="sticky top-0 border-b border-white/10 px-3 py-2 font-mono text-xs text-[#8f8f8f]">{path || '选择一个文件'}</div>
      <div className="max-h-[68vh] overflow-auto p-3">
        {q.isLoading ? (
          <Spinner />
        ) : q.error ? (
          <ErrorBox error={q.error} />
        ) : q.data?.binary ? (
          <span className="text-[#8f8f8f]">二进制文件，不显示内容（{bytes(q.data.size)}）</span>
        ) : (
          <pre className="flex">
            <span className="shrink-0 select-none pr-4 text-right text-[#5a5a5a]" aria-hidden>
              {lines}
            </span>
            <code className="hljs min-w-0 flex-1" dangerouslySetInnerHTML={{ __html: html }} />
          </pre>
        )}
      </div>
    </div>
  )
}

function DiffView({ jobId }: { jobId: number }) {
  const q = useQuery({ queryKey: ['snapdiff', jobId], queryFn: () => api.snapshotDiff(jobId) })
  const html = useMemo(() => (q.data ? highlight(q.data.diff, 'diff') : ''), [q.data])
  return (
    <div className="term max-h-[70vh] overflow-auto rounded-lg border border-line p-3">
      {q.isLoading ? <Spinner /> : q.error ? <ErrorBox error={q.error} /> : <pre><code className="hljs" dangerouslySetInnerHTML={{ __html: html }} /></pre>}
    </div>
  )
}
