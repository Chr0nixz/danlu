import { useCallback, useEffect, useRef, useState } from 'react'
import type { Api } from './client'
import type { JobStatus } from './types'

export interface JobLogOptions {
  /** 先读末尾多少字节 */
  tail?: number
  /** 内存里最多保留多少字符，超过后丢掉前面的 70% */
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
}

/**
 * 先读日志末尾，再用 SSE 接收增量，断线 2 秒后从已读到的位置续上。
 * 偏移量都以字节计，由服务端给出（o = 本段起点，n = 下一段起点），这里只负责拼接和去重。
 */
export function useJobLog(api: Api, jobId: number, { tail = 256 * 1024, maxChars = 3_000_000 }: JobLogOptions = {}): JobLog {
  const [raw, setRaw] = useState('')
  const [start, setStart] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [ended, setEnded] = useState<JobStatus | null>(null)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const next = useRef(0)

  useEffect(() => {
    let close: (() => void) | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let closed = false
    setRaw('')
    setStart(0)
    setError(null)
    setEnded(null)
    setLoading(true)

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
          if (m.n <= next.current) return // 重连后重复的内容
          append(m.t)
          next.current = m.n
        },
        onEnd: (status) => {
          setEnded(status)
          close?.()
        },
        onError: () => {
          close?.()
          if (!closed) retry = setTimeout(connect, 2000)
        },
      })
    }

    api
      .log(jobId, { tail })
      .then((r) => {
        if (closed) return
        setRaw(r.data)
        setStart(r.start)
        next.current = r.next
        setLoading(false)
        connect()
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
  }, [api, jobId, tail, maxChars])

  const loadOlder = useCallback(async () => {
    if (start <= 0) return
    setLoadingOlder(true)
    try {
      const from = Math.max(0, start - tail)
      const r = await api.log(jobId, { offset: from, limit: start - from })
      setRaw((cur) => r.data + cur)
      setStart(r.start)
    } catch (e) {
      setError(e)
    } finally {
      setLoadingOlder(false)
    }
  }, [api, jobId, start, tail])

  return { raw, start, loading, error, ended, loadingOlder, loadOlder }
}
