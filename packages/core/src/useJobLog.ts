import { useCallback, useEffect, useRef, useState } from 'react'
import type { Api } from './client'
import type { JobStatus } from './types'

export interface JobLogOptions {
  /** 先读末尾多少字节 */
  tail?: number
  /** 从指定字节开始读取，用于跳到 traceback */
  offset?: number
  /** 内存里最多保留多少字符。定位时暂停接收，跟随时保留最近的内容。 */
  maxChars?: number
}

export interface JobLog {
  raw: string
  /** 已加载内容在文件中的起始字节；-1 表示前面被丢弃过 */
  start: number
  loading: boolean
  error: unknown
  /** 任务结束且日志读完了 */
  ended: JobStatus | null
  loadingOlder: boolean
  /** 往前多读一段，接在开头 */
  loadOlder(): Promise<void>
  /** 初始内容从明确的字节偏移开始，而非从日志中段截取 */
  exactStart: boolean
  windowLimited: boolean
}

/**
 * 先读日志末尾，再用 SSE 接收增量，断线 2 秒后从已读到的位置续上。
 * 偏移量都以字节计，由服务端给出（o = 本段起点，n = 下一段起点），这里只负责拼接和去重。
 */
export function useJobLog(api: Api, jobId: number, { tail = 256 * 1024, maxChars = 3_000_000, offset }: JobLogOptions = {}): JobLog {
  const [raw, setRaw] = useState('')
  const [start, setStart] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [ended, setEnded] = useState<JobStatus | null>(null)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [exactStart, setExactStart] = useState(false)
  const [windowLimited, setWindowLimited] = useState(false)
  const next = useRef(0)
  const buffered = useRef(0)

  useEffect(() => {
    let close: (() => void) | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let closed = false
    setRaw('')
    setStart(0)
    setError(null)
    setEnded(null)
    setLoading(true)
    setExactStart(offset !== undefined)
    setWindowLimited(false)
    next.current = 0
    buffered.current = 0

    let paused = false
    const pause = () => {
      paused = true
      setWindowLimited(true)
      close?.()
    }

    const append = (t: string) =>
      setRaw((r) => {
        const s = r + t
        if (s.length <= maxChars) return s
        setStart(-1)
        const cut = s.slice(s.length - maxChars * 0.7)
        return cut.slice(cut.indexOf('\n') + 1)
      })

    const connect = () => {
      if (closed) return
      close = api.logStream(jobId, next.current, {
        onChunk: (m) => {
          if (closed || paused || m.n <= next.current) return // 重连后重复的内容
          if (offset === undefined) {
            append(m.t)
          } else {
            const remaining = maxChars - buffered.current
            let part = m.t.slice(0, remaining)
            if (/[\uD800-\uDBFF]$/.test(part)) part = part.slice(0, -1)
            buffered.current += part.length
            setRaw((current) => current + part)
            if (m.t.length >= remaining) pause()
          }
          next.current = m.n
        },
        onEnd: (status) => {
          setEnded(status)
          close?.()
        },
        onError: () => {
          close?.()
          if (!closed && !paused) retry = setTimeout(connect, 2000)
        },
      })
    }

    const initial = offset === undefined ? api.log(jobId, { tail }) : api.log(jobId, { offset, limit: Math.min(tail, maxChars) })
    initial
      .then((r) => {
        if (closed) return
        setRaw(r.data)
        buffered.current = r.data.length
        setStart(r.start)
        next.current = r.next
        setLoading(false)
        if (offset !== undefined && buffered.current >= maxChars) pause()
        else connect()
      })
      .catch((e) => {
        if (closed) return
        setError(e)
        setLoading(false)
      })

    return () => {
      closed = true
      close?.()
      clearTimeout(retry)
    }
  }, [api, jobId, tail, maxChars, offset])

  const loadOlder = useCallback(async () => {
    if (start <= 0) return
    const room = offset === undefined ? tail : Math.min(tail, maxChars - buffered.current)
    if (room <= 0) return
    setLoadingOlder(true)
    try {
      const from = Math.max(0, start - room)
      const r = await api.log(jobId, { offset: from, limit: start - from })
      if (offset !== undefined && r.data.length + buffered.current > maxChars) return
      buffered.current += r.data.length
      setRaw((cur) => r.data + cur)
      setStart(r.start)
      setExactStart(false)
    } catch (e) {
      setError(e)
    } finally {
      setLoadingOlder(false)
    }
  }, [api, jobId, start, tail, offset, maxChars])

  return { raw, start, loading, error, ended, loadingOlder, loadOlder, exactStart, windowLimited }
}
