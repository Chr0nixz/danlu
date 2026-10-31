import { logToHtml, useJobLog, type Api } from '@danlu/core'
import { ArrowDown, ArrowUp, ChevronDown, ChevronUp, Download, Search, WrapText, X } from 'lucide-react'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { MOD, modKey } from '../lib/platform'
import { cx, ErrorText } from './ui'

/**
 * 实时日志：先读末尾，再接收增量；tqdm 进度条原地刷新。两种主题下都是深色终端。
 * Ctrl+F 搜索用 CSS Custom Highlight API 标出命中，不改动日志的 DOM。
 */
export function LogPane({ api, jobId, onDownload }: { api: Api; jobId: number; onDownload?: () => void }) {
  const { raw, start, loading, error, ended, loadingOlder, loadOlder } = useJobLog(api, jobId)
  const [follow, setFollow] = useState(true)
  const [wrap, setWrap] = useState(true)
  const box = useRef<HTMLDivElement>(null)
  const pre = useRef<HTMLPreElement>(null)

  const html = useMemo(() => logToHtml(start !== 0 ? raw.slice(raw.indexOf('\n') + 1) : raw), [raw, start])

  // 跟随时贴底；往前加载了一段时，保持正在看的那一行不动
  const prev = useRef({ start, height: 0 })
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    if (follow) el.scrollTop = el.scrollHeight
    else if (start >= 0 && start < prev.current.start) el.scrollTop += el.scrollHeight - prev.current.height
    prev.current = { start, height: el.scrollHeight }
  }, [html, follow, start])

  // ---------- 搜索 ----------
  const [searching, setSearching] = useState(false)
  const [query, setQuery] = useState('')
  const [hits, setHits] = useState<Range[]>([])
  const [current, setCurrent] = useState(0)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (modKey(e) && e.key.toLowerCase() === 'f') {
        e.preventDefault()
        setSearching(true)
        requestAnimationFrame(() => input.current?.select())
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    const root = pre.current
    if (!searching || !query || !root) {
      setHits([])
      return
    }
    const q = query.toLowerCase()
    const found: Range[] = []
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
    for (let n = walker.nextNode(); n && found.length < 2000; n = walker.nextNode()) {
      const text = (n.textContent ?? '').toLowerCase()
      for (let i = text.indexOf(q); i >= 0; i = text.indexOf(q, i + q.length)) {
        const r = new Range()
        r.setStart(n, i)
        r.setEnd(n, i + q.length)
        found.push(r)
      }
    }
    setHits(found)
    setCurrent((c) => (found.length ? Math.min(c, found.length - 1) : 0))
  }, [searching, query, html])

  useEffect(() => {
    const reg = typeof CSS !== 'undefined' && 'highlights' in CSS ? CSS.highlights : null
    if (!reg) return
    reg.delete('log-match')
    reg.delete('log-current')
    if (hits.length) {
      reg.set('log-match', new Highlight(...hits))
      reg.set('log-current', new Highlight(hits[current]))
    }
    return () => {
      reg.delete('log-match')
      reg.delete('log-current')
    }
  }, [hits, current])

  const jump = useCallback(
    (to: number) => {
      if (!hits.length) return
      const i = (to + hits.length) % hits.length
      setCurrent(i)
      setFollow(false)
      const el = box.current
      const r = hits[i].getBoundingClientRect()
      if (el) {
        const b = el.getBoundingClientRect()
        el.scrollTop += r.top - b.top - b.height / 2
      }
    },
    [hits],
  )

  const closeSearch = () => {
    setSearching(false)
    setQuery('')
  }

  const tb =
    'inline-flex h-6 items-center gap-1 rounded-[5px] px-1.5 text-[11.5px] text-[#a3a5ad] transition-colors duration-[120ms] hover:bg-white/10 hover:text-white disabled:opacity-50 [&_svg]:size-3.5'

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] bg-term text-[#d6d7dc] shadow-[inset_0_0_0_1px_rgb(255_255_255/0.06)]">
      <div className="flex h-8 shrink-0 items-center gap-1 border-b border-white/[0.06] pr-1.5 pl-2.5 text-[11.5px] text-[#8b8e97]">
        <span className="flex items-center gap-1.5">
          <span className={cx('size-1.5 rounded-full', ended ? 'bg-[#5a5d66]' : 'breathe bg-[#4fd18b]')} aria-hidden />
          {ended ? '已结束' : '实时'}
        </span>
        {start === -1 && <span className="truncate pl-2">只保留了最近的部分</span>}
        <span className="ml-auto" />
        {searching ? (
          <div className="flex items-center gap-0.5">
            <input
              ref={input}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') jump(e.shiftKey ? current - 1 : current + 1)
                if (e.key === 'Escape') {
                  e.stopPropagation()
                  closeSearch()
                }
              }}
              placeholder="搜索日志"
              aria-label="搜索日志"
              className="h-6 w-40 rounded-[5px] bg-white/[0.07] px-2 text-[11.5px] text-white caret-white outline-none placeholder:text-[#6b6e77]"
            />
            <span className="num w-14 text-center text-[11px]">{query ? (hits.length ? `${current + 1}/${hits.length}` : '无') : ''}</span>
            <button type="button" className={tb} aria-label="上一个" onClick={() => jump(current - 1)}>
              <ChevronUp />
            </button>
            <button type="button" className={tb} aria-label="下一个" onClick={() => jump(current + 1)}>
              <ChevronDown />
            </button>
            <button type="button" className={tb} aria-label="关闭搜索" onClick={closeSearch}>
              <X />
            </button>
          </div>
        ) : (
          <button type="button" className={tb} title={`搜索（${MOD}+F）`} onClick={() => setSearching(true)}>
            <Search />
          </button>
        )}
        {start > 0 && (
          <button type="button" className={tb} disabled={loadingOlder} onClick={loadOlder}>
            <ArrowUp />
            更早
          </button>
        )}
        <button type="button" className={cx(tb, wrap && 'bg-white/10 text-white')} aria-pressed={wrap} title="自动换行" onClick={() => setWrap((w) => !w)}>
          <WrapText />
        </button>
        {onDownload && (
          <button type="button" className={tb} title="下载完整日志" onClick={onDownload}>
            <Download />
          </button>
        )}
      </div>
      <div className="relative min-h-0 flex-1">
        <div
          ref={box}
          onScroll={(e) => {
            const el = e.currentTarget
            setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
          }}
          className="selectable absolute inset-0 overflow-auto px-3 py-2 font-mono text-[11.5px] leading-[1.6]"
        >
          {loading ? (
            <span className="text-[#8b8e97]">读取中…</span>
          ) : error ? (
            <ErrorText error={error} />
          ) : raw === '' ? (
            <span className="text-[#8b8e97]">{ended ? '没有输出' : '等待输出…'}</span>
          ) : (
            <pre ref={pre} className={wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'whitespace-pre'} dangerouslySetInnerHTML={{ __html: html }} />
          )}
        </div>
        {!follow && (
          <button
            type="button"
            onClick={() => setFollow(true)}
            className="absolute right-3 bottom-3 inline-flex h-7 items-center gap-1 rounded-[6px] bg-white px-2.5 text-[11.5px] font-medium text-black shadow-[0_4px_12px_rgb(0_0_0/0.4)]"
          >
            <ArrowDown className="size-3.5" />
            回到底部
          </button>
        )}
      </div>
    </div>
  )
}
