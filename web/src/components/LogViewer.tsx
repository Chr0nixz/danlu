import { LogRenderer, useJobLog } from '@danlu/core'
import { ArrowDown, ArrowUp, Download, WrapText } from 'lucide-react'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { api } from '../lib/api'
import { cx, ErrorBox, Spinner } from './ui'

/** 先读日志末尾 256KB，再用 SSE 接收增量（见 @danlu/core 的 useJobLog） */
export function LogViewer({ jobId, offset }: { jobId: number; offset?: number }) {
  const { raw, start, loading, error, ended, loadingOlder, loadOlder, exactStart } = useJobLog(api, jobId, { offset })
  const [follow, setFollow] = useState(offset === undefined)
  const [wrap, setWrap] = useState(true)
  const box = useRef<HTMLDivElement>(null)
  const pre = useRef<HTMLPreElement>(null)
  useEffect(() => setFollow(offset === undefined), [offset])

  // 从文件中间开始读时，第一行是半截的，丢掉
  const text = useMemo(() => (start !== 0 && !exactStart ? raw.slice(raw.indexOf('\n') + 1) : raw), [raw, start, exactStart])

  // 增量渲染，不经过 React：每批新换行的内容是 pre 里单独的一块，最后一行单独一块原地重画。
  // 浏览器只排版新的这一块，前面几百 KB 不用重排（整段替换 256KB 要一百多毫秒，每秒两次）
  const view = useRef<{ el: HTMLPreElement; renderer: LogRenderer; tail: HTMLDivElement } | null>(null)
  useLayoutEffect(() => {
    const el = pre.current
    if (!el) return
    let v = view.current
    if (v?.el !== el) {
      v = view.current = { el, renderer: new LogRenderer(), tail: document.createElement('div') }
      el.replaceChildren(v.tail)
    }
    const { reset, lines, tail } = v.renderer.update(text)
    if (reset) el.replaceChildren(v.tail)
    // 块末尾的换行不会多出一个空行，复制出来的文字也和原文一样
    if (lines) v.tail.insertAdjacentHTML('beforebegin', `<div>${lines}</div>`)
    v.tail.innerHTML = tail
  }, [text])

  // 往前加载了一段时，保持正在看的那一行不动
  const prev = useRef({ start, height: 0 })
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    if (follow) el.scrollTop = el.scrollHeight
    else if (start >= 0 && start < prev.current.start) el.scrollTop += el.scrollHeight - prev.current.height
    prev.current = { start, height: el.scrollHeight }
  }, [text, follow, start])

  // 日志框从自己的位置铺到视口底部（见 index.css 的 .log-fit），上面的内容变高变矮时重新量
  const [top, setTop] = useState(0)
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const measure = () => setTop(Math.round(el.getBoundingClientRect().top + window.scrollY))
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(document.body)
    window.addEventListener('resize', measure)
    return () => {
      ro.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [])

  const onScroll = () => {
    const el = box.current
    if (!el) return
    setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
  }

  // 按钮 28px 高，点击区上下撑满 40px 高的工具栏（手机上只剩图标，又窄又矮）
  const tb =
    'relative inline-flex h-7 items-center gap-1.5 rounded px-2 text-xs text-[#a1a1a1] transition-colors duration-150 before:absolute before:inset-x-0 before:-inset-y-1.5 hover:bg-white/10 hover:text-white disabled:opacity-50 [&_svg]:size-3.5'

  return (
    <div className="overflow-hidden rounded-lg border border-line bg-[#0b0b0b]">
      <div className="flex h-10 items-center gap-1 border-b border-white/10 px-2 text-xs text-[#8f8f8f]">
        <span className="flex items-center gap-1.5 px-1.5">
          {!ended ? (
            <>
              <span className="size-1.5 animate-pulse rounded-full bg-[#3dd68c]" aria-hidden />
              实时
            </>
          ) : (
            <>
              <span className="size-1.5 rounded-full bg-[#5a5a5a]" />
              已结束
            </>
          )}
        </span>
        {start === -1 && <span className="truncate px-1.5">只保留了最近的部分，完整日志请下载</span>}
        <span className="ml-auto" />
        {start > 0 && (
          <button type="button" className={tb} disabled={loadingOlder} onClick={loadOlder}>
            {loadingOlder ? <Spinner className="size-3.5" /> : <ArrowUp />}
            加载更早
          </button>
        )}
        <button type="button" className={cx(tb, wrap && 'bg-white/10 text-white')} aria-pressed={wrap} title="自动换行" onClick={() => setWrap((w) => !w)}>
          <WrapText />
          <span className="hidden sm:inline">换行</span>
        </button>
        <a href={`/api/jobs/${jobId}/log/download`} className={tb} title="下载完整日志">
          <Download />
          <span className="hidden sm:inline">下载</span>
        </a>
      </div>
      <div className="relative">
        <div
          ref={box}
          onScroll={onScroll}
          className="term log-fit overflow-auto px-3 py-2.5"
          style={top ? ({ '--log-top': `${top}px` } as CSSProperties) : undefined}
        >
          {loading ? (
            <Spinner />
          ) : error ? (
            <ErrorBox error={error} />
          ) : raw === '' ? (
            <span className="text-[#8f8f8f]">{ended ? '没有输出' : '等待输出…'}</span>
          ) : (
            <pre ref={pre} className={wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'whitespace-pre'} />
          )}
        </div>
        {!follow && (
          <button
            type="button"
            onClick={() => setFollow(true)}
            className="absolute bottom-3 right-4 inline-flex h-8 items-center gap-1 rounded-md bg-white px-3 text-xs font-medium text-black shadow-[0_4px_12px_rgb(0_0_0/0.4)] hover:bg-[#e6e6e6]"
          >
            <ArrowDown className="size-3.5" />
            回到底部
          </button>
        )}
      </div>
    </div>
  )
}
