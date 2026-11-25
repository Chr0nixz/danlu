import { translate } from './i18n.ts'
import type {
  HostMetricsResponse,
  AuditEntry,
  BatchReq,
  BatchResult,
  DeviceToken,
  DoctorReport,
  GPU,
  Job,
  JobComparison,
  JobsQuery,
  JobStatus,
  LogChunk,
  LogSearchResult,
  LoginSession,
  Prefs,
  PrefsPatch,
  Reservation,
  ReservationReq,
  Me,
  QueueMove,
  QueueState,
  ResourceHistory,
  ResourceRange,
  ResourceSnapshot,
  RetentionKey,
  RetentionPreview,
  RetentionSettings,
  RuntimeKey,
  RuntimeSettings,
  SecurityPatch,
  SecuritySettings,
  SettingsPatch,
  Settings,
  SnapshotMeta,
  SubmitReq,
  TerminalSession,
  TraceResponse,
  StrictStatus,
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
   * 是函数不是字符串：抛错时才取文案，切换语言后不必重建 Api。
   */
  emptyNotFound: () => string
}

/** 这一版前端认识的接口版本，和服务器 internal/api 的 APIVersion 保持一致 */
export const API_VERSION = 1

/** status 为 0 表示没连上服务器（网络错误、超时、证书不对）。code/params 是服务器给的机器码，供本地化 */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public needTotp = false,
    public code?: string,
    public params?: Record<string, string | number>,
  ) {
    super(message)
  }
}

/**
 * 错误的显示文案：带机器码且目录里有对应键就本地化，
 * 否则回退服务器原文（旧服务器、未编码的错误天然兼容）。
 */
export function apiErrorText(err: unknown): string {
  if (err instanceof ApiError && err.code) {
    const key = `error.${err.code}`
    const msg = translate(key, err.params)
    if (msg !== key) return msg
  }
  return err instanceof Error ? err.message : String(err)
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
    if (res.status === 404 && !res.body) throw new ApiError(404, opt.emptyNotFound())
    let data: unknown = null
    try {
      data = res.body ? JSON.parse(res.body) : null
    } catch {
      throw new ApiError(res.status, res.body.slice(0, 200) || `HTTP ${res.status}`)
    }
    if (res.status < 200 || res.status >= 300) {
      const error = data as { error?: string; need_totp?: boolean; code?: string; params?: Record<string, string | number> } | null
      throw new ApiError(res.status, error?.error ?? `HTTP ${res.status}`, error?.need_totp === true, error?.code, error?.params)
    }
    return data as T
  }

  async function getText(url: string): Promise<string> {
    const res = await t.request('GET', url)
    if (res.status === 404 && !res.body) throw new ApiError(404, opt.emptyNotFound())
    if (res.status < 200 || res.status >= 300) {
      let message = `HTTP ${res.status}`
      let code: string | undefined
      let params: Record<string, string | number> | undefined
      try {
        const e = JSON.parse(res.body) as { error?: string; code?: string; params?: Record<string, string | number> }
        message = e?.error ?? message
        code = e?.code
        params = e?.params
      } catch {
        message = res.body.slice(0, 200) || message
      }
      throw new ApiError(res.status, message, false, code, params)
    }
    return res.body
  }
  const get = <T,>(url: string) => req<T>('GET', url)
  const post = <T,>(url: string, body: unknown = {}) => req<T>('POST', url, body)

  return {
    login: (username: string, password: string, code?: string) => post('/api/login', { username, password, code }),
    logout: () => post('/api/logout'),
    me: () => get<Me>('/api/me'),

    gpus: () => get<{ gpus: GPU[]; updated_at: number; error?: string; error_since?: number }>('/api/gpus'),
    queue: () => get<QueueState>('/api/queue'),
    pauseQueue: () => post<QueueState>('/api/queue/pause'),
    resumeQueue: () => post<QueueState>('/api/queue/resume'),
    reservations: () => get<{ reservations: Reservation[] }>('/api/reservations'),
    createReservation: (r: ReservationReq) => post<Reservation>('/api/reservations', r),
    releaseReservation: (id: number) => post<Reservation>(`/api/reservations/${id}/release`),
    extendReservation: (id: number) => post<Reservation>(`/api/reservations/${id}/extend`),
    hostMetrics: () => get<HostMetricsResponse>('/api/system'),
    resources: () => get<ResourceSnapshot>('/api/resources'),
    resourceHistory: (range: ResourceRange) => get<ResourceHistory>(`/api/resources/history${qs({ range })}`),
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
    /** 修改排队中的任务，body 和提交相同；rerun_of、code_from 不变。已经开始时 409 */
    editJob: (id: number, r: SubmitReq) => post<Job>(`/api/jobs/${id}/edit`, r),
    /** 调整排队任务的位置 */
    moveJob: (id: number, to: QueueMove) => post<Job>(`/api/jobs/${id}/move`, { to }),
    /** 暂缓或恢复排队任务 */
    holdJob: (id: number, held: boolean) => post<Job>(`/api/jobs/${id}/hold`, { held }),
    /** 批量操作排队任务；有任务已经开始或结束时 409，整批不改 */
    batchJobs: (r: BatchReq) => post<BatchResult>('/api/jobs/batch', r),
    cancel: (id: number) => post<Job>(`/api/jobs/${id}/cancel`),
    star: (id: number, starred: boolean) => post<Job>(`/api/jobs/${id}/star`, { starred }),
    rename: (id: number, name: string) => post<Job>(`/api/jobs/${id}/rename`, { name }),
    setMemo: (id: number, memo: string) => post<Job>(`/api/jobs/${id}/memo`, { memo }),
    setNotifyDone: (id: number, on: boolean) => post<Job>(`/api/jobs/${id}/notify`, { on }),
    log: (id: number, p: { tail?: number; offset?: number; limit?: number }) =>
      get<LogChunk>(`/api/jobs/${id}/log${qs(p)}`),
    searchLog: (id: number, q: string, limit = 200) =>
      get<LogSearchResult>(`/api/jobs/${id}/log/search${qs({ q, limit })}`),
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
    doctor: () => get<DoctorReport>('/api/doctor'),
    testNotify: () => post<{ ok: boolean }>('/api/notify/test'),
    changePassword: (old: string, next: string) => post('/api/settings/password', { old, new: next }),
    /** 只传改了的项。换了入口时，响应会给这个浏览器带上新的入口 cookie */
    updateSecurity: (patch: SecurityPatch) => post<{ security: SecuritySettings }>('/api/settings/security', patch),
    /** 只传改了的项，null 恢复成 config.toml 里的值。保存后立即生效 */
    updateRuntime: (patch: SettingsPatch<RuntimeKey>) => post<{ runtime: RuntimeSettings }>('/api/settings/runtime', patch),
    /** 保存后服务器马上按新策略清理一次 */
    updateRetention: (patch: SettingsPatch<RetentionKey>) => post<{ retention: RetentionSettings }>('/api/settings/retention', patch),
    /** 按要改的保留策略预览会清理什么，不删任何东西；正在自动清理时返回 409 */
    previewRetention: (patch: SettingsPatch<RetentionKey>) => post<RetentionPreview>('/api/settings/retention/preview', patch),
    updatePrefs: (patch: PrefsPatch) => post<{ prefs: Partial<Prefs> }>('/api/settings/prefs', patch),
    /** 打开前服务器经 sudo 查一次 root 助手，查不过返回 409 strict_unavailable。只影响之后启动的任务 */
    updateStrict: (enabled: boolean) => post<StrictStatus>('/api/settings/strict', { enabled }),
    sessions: () => get<{ sessions: LoginSession[] }>('/api/sessions'),
    revokeSession: (id: string) => post<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/revoke`),
    revokeOtherSessions: () => post<{ revoked: number }>('/api/sessions/revoke-others'),
    /** 每页 50 条，page 从 1 开始 */
    audit: (page = 1) => get<{ entries: AuditEntry[] }>(page > 1 ? `/api/audit?page=${page}` : '/api/audit'),
    tokens: () => get<{ tokens: DeviceToken[] }>('/api/tokens'),
    revokeToken: (id: number) => post(`/api/tokens/${id}/revoke`),

    terminalSessions: () => get<{ sessions: TerminalSession[] }>('/api/terminal'),
    openTerminal: (jobId?: number) => post<TerminalSession>('/api/terminal', jobId === undefined ? {} : { job_id: jobId }),
    closeTerminal: (id: string) => post<{ ok: boolean }>(`/api/terminal/${encodeURIComponent(id)}/close`),
  }
}

export type Api = ReturnType<typeof createApi>
