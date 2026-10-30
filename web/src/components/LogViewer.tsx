import { ArrowDown, ArrowUp, Download, WrapText } from 'lucide-react'
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { api } from '../lib/api'
import { logToHtml } from '../lib/terminal'
import { cx, ErrorBox, Spinner } from './ui'

const TAIL = 256 * 1024
const MAX_CHARS = 3_000_000

/**
 * 先读日志末尾 256KB，再用 SSE 接收增量。
 * 偏移量都以字节计，由服务端给出（o = 本段起点，n = 下一段起点），前端只负责拼接和去重。
 */
export function LogViewer({ jobId }: { jobId: number }) {
  const [raw, setRaw] = useState('')
  const [start, setStart] = useState(0) // 已加载内容在文件中的起始字节；-1 表示前面被丢弃过
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [ended, setEnded] = useState(false)
  const [follow, setFollow] = useState(true)
  const [wrap, setWrap] = useState(true)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const next = useRef(0)
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let es: EventSource | null = null
    let retry: number | undefined
    let closed = false
    setRaw('')
    setEnded(false)
    setLoading(true)

    const append = (t: string) =>
      setRaw((r) => {
        const s = r + t
        if (s.length <= MAX_CHARS) return s
        setStart(-1)
        const cut = s.slice(s.length - MAX_CHARS * 0.7)
        return cut.slice(cut.indexOf('\n') + 1)
      })

    const connect = () => {
      if (closed) return
      es = new EventSource(`/api/jobs/${jobId}/log/stream?offset=${next.current}`)
      es.onmessage = (ev) => {
        const m = JSON.parse(ev.data) as { o: number; n: number; t: string }
        if (m.n <= next.current) return // 重连后重复的内容
        append(m.t)
        next.current = m.n
      }
      es.addEventListener('end', () => {
        setEnded(true)
        es?.close()
      })
      es.onerror = () => {
        // 浏览器的自动重连会带着旧的 offset，这里自己控制重连
        es?.close()
        retry = window.setTimeout(connect, 2000)
      }
    }

    api
      .log(jobId, { tail: TAIL })
      .then((r) => {
        if (closed) return
        setRaw(r.data)
        setStart(r.start)
        next.current = r.next
        setLoading(false)
        connect()
      })
      .catch((e) => {
        setError(e)
        setLoading(false)
      })

    return () => {
      closed = true
      es?.close()
      window.clearTimeout(retry)
    }
  }, [jobId])

  const html = useMemo(() => {
    // 从文件中间开始读时，第一行是半截的，丢掉
    const text = start !== 0 ? raw.slice(raw.indexOf('\n') + 1) : raw
    return logToHtml(text)
  }, [raw, start])

  useLayoutEffect(() => {
    const el = box.current
    if (el && follow) el.scrollTop = el.scrollHeight
  }, [html, follow])

  const onScroll = () => {
    const el = box.current
    if (!el) return
    setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
  }

  const loadOlder = async () => {
    if (start <= 0) return
    setLoadingOlder(true)
    try {
      const from = Math.max(0, start - TAIL)
      const r = await api.log(jobId, { offset: from, limit: start - from })
      const el = box.current
      const prevHeight = el?.scrollHeight ?? 0
      setRaw((cur) => r.data + cur)
      setStart(r.start)
      requestAnimationFrame(() => {
        if (el) el.scrollTop += el.scrollHeight - prevHeight
      })
    } catch (e) {
      setError(e)
    } finally {
      setLoadingOlder(false)
    }
  }

  const tb = 'inline-flex h-7 items-center gap-1.5 rounded px-2 text-xs text-[#a1a1a1] transition-colors duration-150 hover:bg-white/10 hover:text-white disabled:opacity-50 [&_svg]:size-3.5'

  return (
    <div className="overflow-hidden rounded-lg border border-line bg-[#0b0b0b]">
      <div className="flex h-10 items-center gap-1 border-b border-white/10 px-2 text-xs text-[#8f8f8f]">
        <span className="flex items-center gap-1.5 px-1.5">
          {!ended ? (
            <>
              <span className="size-1.5 animate-pulse rounded-full bg-[#3dd68c]" />
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
        <div ref={box} onScroll={onScroll} className="term h-[60dvh] overflow-auto px-3 py-2.5 sm:h-[calc(100dvh-19.5rem)] sm:min-h-[420px]">
          {loading ? (
            <Spinner />
          ) : error ? (
            <ErrorBox error={error} />
          ) : raw === '' ? (
            <span className="text-[#8f8f8f]">{ended ? '没有输出' : '等待输出…'}</span>
          ) : (
            <pre className={wrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'} dangerouslySetInnerHTML={{ __html: html }} />
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
