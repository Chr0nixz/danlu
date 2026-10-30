export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface Job {
  id: number
  name: string
  workdir: string
  conda_env: string
  cmd: string
  env: Record<string, string>
  gpus_req: number
  gpus_pinned: number[]
  gpus_assigned: number[]
  status: JobStatus
  master_port: number
  pid: number
  exit_code: number | null
  fail_reason: string
  cancel_requested: boolean
  tracker_url: string
  git_commit: string
  git_branch: string
  git_dirty: boolean
  snapshot_ok: boolean
  snapshot_note: string
  rerun_of: number | null
  created_at: number
  started_at: number | null
  ended_at: number | null
  last_line?: string
  /** 失败任务日志里最后一条像报错的行（Python 异常、Killed 等） */
  error_line?: string
  queue_pos?: number
}

export interface Proc {
  pid: number
  used_mem_mb: number
  user: string
  job_id: number
}

export interface GPU {
  index: number
  uuid: string
  name: string
  util: number
  mem_used_mb: number
  mem_total_mb: number
  temp_c: number
  power_w: number
  job: { id: number; name: string } | null
  external: Proc[]
}

export interface SnapshotMeta {
  workdir: string
  created_at: number
  git: { commit: string; branch: string; dirty: boolean; status: string } | null
  files: { path: string; size: number }[]
  skipped: { path: string; reason: string }[]
  skip_count: number
  truncated: boolean
  total_size: number
}

export interface Settings {
  username: string
  version: string
  tls_mode: string
  public_ip: string
  port: number
  data_dir: string
  conda_base: string
  ca_available: boolean
  ca_fingerprint?: string
}

export interface SubmitReq {
  name: string
  workdir: string
  conda_env: string
  cmd: string
  env: Record<string, string>
  gpus: number
  pinned_gpus: number[]
  rerun_of: number | null
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function req<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  if (res.status === 401 && !location.pathname.startsWith('/login')) {
    // 登录后回到原来的页面
    location.href = `/login?next=${encodeURIComponent(location.pathname + location.search)}`
    throw new ApiError(401, '未登录')
  }
  if (res.status === 404 && !text) {
    // 安全入口 cookie 过期后，所有请求都会是空的 404
    throw new ApiError(404, '访问凭证已失效，请重新打开带安全入口的地址')
  }
  let data: unknown = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    throw new ApiError(res.status, text.slice(0, 200) || res.statusText)
  }
  if (!res.ok) {
    throw new ApiError(res.status, (data as { error?: string })?.error ?? res.statusText)
  }
  return data as T
}

const get = <T,>(url: string) => req<T>('GET', url)
const post = <T,>(url: string, body: unknown = {}) => req<T>('POST', url, body)

function qs(params: Record<string, string | number | undefined>) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') p.set(k, String(v))
  const s = p.toString()
  return s ? `?${s}` : ''
}

export const api = {
  login: (username: string, password: string) => post('/api/login', { username, password }),
  logout: () => post('/api/logout'),
  me: () => get<{ username: string; version: string; hostname: string }>('/api/me'),

  gpus: () => get<{ gpus: GPU[]; updated_at: number; error?: string }>('/api/gpus'),
  condaEnvs: () =>
    get<{ available: boolean; base?: string; envs: { name: string; path: string }[]; error?: string }>('/api/conda-envs'),
  dirs: (path: string) => get<{ dirs: string[] }>(`/api/fs/dirs${qs({ path })}`),
  gitHead: (path: string) =>
    get<{ is_git: boolean; commit?: string; branch?: string; dirty?: boolean }>(`/api/fs/git${qs({ path })}`),
  recentWorkdirs: () => get<{ dirs: string[] }>('/api/workdirs/recent'),

  jobs: (p: { status?: string; q?: string; page?: number; size?: number; ended_after?: number }) =>
    get<{ jobs: Job[]; total: number; page: number; size: number }>(`/api/jobs${qs(p)}`),
  job: (id: number) => get<Job>(`/api/jobs/${id}`),
  submit: (r: SubmitReq) => post<Job>('/api/jobs', r),
  cancel: (id: number) => post<Job>(`/api/jobs/${id}/cancel`),
  log: (id: number, p: { tail?: number; offset?: number; limit?: number }) =>
    get<{ data: string; start: number; next: number; size: number }>(`/api/jobs/${id}/log${qs(p)}`),
  snapshot: (id: number) => get<{ meta: SnapshotMeta; has_diff: boolean; note: string }>(`/api/jobs/${id}/snapshot`),
  snapshotFile: (id: number, path: string) =>
    get<{ path: string; size: number; binary: boolean; content: string }>(`/api/jobs/${id}/snapshot/file${qs({ path })}`),
  snapshotDiff: (id: number) => get<{ diff: string }>(`/api/jobs/${id}/snapshot/diff`),

  settings: () => get<Settings>('/api/settings'),
  changePassword: (old: string, next: string) => post('/api/settings/password', { old, new: next }),
  audit: () => get<{ entries: { id: number; ts: number; ip: string; action: string; detail: string }[] }>('/api/audit'),
}
