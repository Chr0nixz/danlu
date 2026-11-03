import type {
  HostMetricsResponse,
  AuditEntry,
  DeviceToken,
  GPU,
  Job,
  JobComparison,
  JobsQuery,
  JobStatus,
  LogChunk,
  Me,
  QueueState,
  Settings,
  SnapshotMeta,
  SubmitReq,
  TerminalSession,
  TraceResponse,
} from './types'

/**
 * 发请求的方式由使用方提供：面板直接用 fetch 和 EventSource，
 * 桌面客户端经 Rust 层转发（只信任这台服务器自己的 CA，带设备令牌）。
 */
export interface Transport {
  /** path 以 /api/ 开头，带查询串；body 是已经序列化好的 JSON */
  request(method: 'GET' | 'POST', path: string, body?: string): Promise<{ status: number; body: string }>
  /** 打开一个 SSE 流，返回关闭函数。普通消息的 event 是 'message' */
  stream(path: string, h: StreamHandlers): () => void
}

export interface StreamHandlers {
  onEvent(event: string, data: string): void
  /** 连接断开或出错。不会自动重连，由调用方决定 */
  onError(): void
}

export interface ApiOptions {
  /**
   * 收到空的 404 时的报错文字。服务器对没有通过检查的请求一律返回空 404：
   * 面板里是安全入口的 cookie 过期了，客户端里是设备令牌被吊销了。
   */
  emptyNotFound: string
}

/** 这一版前端认识的接口版本，和服务器 internal/api 的 APIVersion 保持一致 */
export const API_VERSION = 1

/** status 为 0 表示没连上服务器（网络错误、超时、证书不对） */
export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

export function qs(params: object) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') p.set(k, String(v))
  const s = p.toString()
  return s ? `?${s}` : ''
}

export interface LogStreamHandlers {
  /** o：这段内容在日志文件里的起始字节，n：下一段的起始字节 */
  onChunk(m: { o: number; n: number; t: string }): void
  onEnd(status: JobStatus): void
  onError(): void
}

export function createApi(t: Transport, opt: ApiOptions) {
  async function req<T>(method: 'GET' | 'POST', url: string, body?: unknown): Promise<T> {
    const res = await t.request(method, url, body !== undefined ? JSON.stringify(body) : undefined)
    if (res.status === 404 && !res.body) throw new ApiError(404, opt.emptyNotFound)
    let data: unknown = null
    try {
      data = res.body ? JSON.parse(res.body) : null
    } catch {
      throw new ApiError(res.status, res.body.slice(0, 200) || `HTTP ${res.status}`)
    }
    if (res.status < 200 || res.status >= 300) {
      throw new ApiError(res.status, (data as { error?: string })?.error ?? `HTTP ${res.status}`)
    }
    return data as T
  }

  async function getText(url: string): Promise<string> {
    const res = await t.request('GET', url)
    if (res.status === 404 && !res.body) throw new ApiError(404, opt.emptyNotFound)
    if (res.status < 200 || res.status >= 300) {
      let message = `HTTP ${res.status}`
      try {
        message = (JSON.parse(res.body) as { error?: string })?.error ?? message
      } catch {
        message = res.body.slice(0, 200) || message
      }
      throw new ApiError(res.status, message)
    }
    return res.body
  }
  const get = <T,>(url: string) => req<T>('GET', url)
  const post = <T,>(url: string, body: unknown = {}) => req<T>('POST', url, body)

  return {
    login: (username: string, password: string) => post('/api/login', { username, password }),
    logout: () => post('/api/logout'),
    me: () => get<Me>('/api/me'),

    gpus: () => get<{ gpus: GPU[]; updated_at: number; error?: string; error_since?: number }>('/api/gpus'),
    queue: () => get<QueueState>('/api/queue'),
    pauseQueue: () => post<QueueState>('/api/queue/pause'),
    resumeQueue: () => post<QueueState>('/api/queue/resume'),
    hostMetrics: () => get<HostMetricsResponse>('/api/system'),
    condaEnvs: () =>
      get<{ available: boolean; base?: string; envs: { name: string; path: string }[]; error?: string }>('/api/conda-envs'),
    dirs: (path: string) => get<{ dirs: string[] }>(`/api/fs/dirs${qs({ path })}`),
    gitHead: (path: string) =>
      get<{ is_git: boolean; commit?: string; branch?: string; dirty?: boolean }>(`/api/fs/git${qs({ path })}`),
    recentWorkdirs: () => get<{ dirs: string[] }>('/api/workdirs/recent'),

    jobs: (p: JobsQuery) => get<{ jobs: Job[]; total: number; page: number; size: number }>(`/api/jobs${qs(p)}`),
    trace: (path: string) => get<TraceResponse>(`/api/trace${qs({ path })}`),
    compare: (a: number, b: number) => get<JobComparison>(`/api/compare${qs({ a, b })}`),
    job: (id: number) => get<Job>(`/api/jobs/${id}`),
    envPackages: (id: number) => getText(`/api/jobs/${id}/env`),
    submit: (r: SubmitReq) => post<Job>('/api/jobs', r),
    cancel: (id: number) => post<Job>(`/api/jobs/${id}/cancel`),
    star: (id: number, starred: boolean) => post<Job>(`/api/jobs/${id}/star`, { starred }),
    log: (id: number, p: { tail?: number; offset?: number; limit?: number }) =>
      get<LogChunk>(`/api/jobs/${id}/log${qs(p)}`),
    /** 从 offset 开始接收日志增量，任务结束且读完后收到 onEnd。返回关闭函数 */
    logStream: (id: number, offset: number, h: LogStreamHandlers) =>
      t.stream(`/api/jobs/${id}/log/stream${qs({ offset })}`, {
        onEvent: (event, data) => {
          if (event === 'message') h.onChunk(JSON.parse(data))
          else if (event === 'end') h.onEnd((JSON.parse(data) as { status: JobStatus }).status)
        },
        onError: h.onError,
      }),
    snapshot: (id: number) => get<{ meta: SnapshotMeta; has_diff: boolean; note: string }>(`/api/jobs/${id}/snapshot`),
    snapshotFile: (id: number, path: string) =>
      get<{ path: string; size: number; binary: boolean; content: string }>(`/api/jobs/${id}/snapshot/file${qs({ path })}`),
    snapshotDiff: (id: number) => get<{ diff: string }>(`/api/jobs/${id}/snapshot/diff`),

    settings: () => get<Settings>('/api/settings'),
    testNotify: () => post<{ ok: boolean }>('/api/notify/test'),
    changePassword: (old: string, next: string) => post('/api/settings/password', { old, new: next }),
    audit: () => get<{ entries: AuditEntry[] }>('/api/audit'),
    tokens: () => get<{ tokens: DeviceToken[] }>('/api/tokens'),
    revokeToken: (id: number) => post(`/api/tokens/${id}/revoke`),

    terminalSessions: () => get<{ sessions: TerminalSession[] }>('/api/terminal'),
    openTerminal: (jobId?: number) => post<TerminalSession>('/api/terminal', jobId === undefined ? {} : { job_id: jobId }),
    closeTerminal: (id: string) => post<{ ok: boolean }>(`/api/terminal/${encodeURIComponent(id)}/close`),
  }
}

export type Api = ReturnType<typeof createApi>
