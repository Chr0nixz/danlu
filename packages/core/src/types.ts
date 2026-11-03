// 服务器 /api 返回的数据结构。面板和桌面客户端共用。

export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface Recipe {
  env_hash?: string
  env_error?: string
  pending?: boolean
  machine?: RecipeMachine
  machine_error?: string
}

export interface RecipeMachine {
  hostname?: string
  driver_version?: string
  cuda_version?: string
  gpus?: RecipeGPU[]
}

export interface RecipeGPU {
  index: number
  uuid?: string
  name?: string
}

export interface Job {
  id: number
  name: string
  workdir: string
  conda_env: string
  cmd: string
  env: Record<string, string>
  inherit_bashrc: boolean
  gpus_req: number
  gpus_pinned: number[]
  gpus_assigned: number[]
  status: JobStatus
  master_port: number
  pid: number
  exit_code: number | null
  fail_reason: string
  /** 失败原因分类；旧版服务器没有这个字段 */
  fail_kind?: string
  /** 运行备注（如残留进程清理）；旧版服务器没有这个字段 */
  notes?: string
  /** 任务运行期间是否观察到卡上有其他进程 */
  ext_seen?: boolean
  recipe?: Recipe
  output_dir?: string
  starred?: boolean
  pruned_at?: number | null
  cancel_requested: boolean
  tracker_url: string
  git_commit: string
  git_branch: string
  git_dirty: boolean
  snapshot_ok: boolean
  snapshot_note: string
  rerun_of: number | null
  code_from?: number
  created_at: number
  started_at: number | null
  ended_at: number | null
  last_line?: string
  /** 失败任务日志里最后一条像报错的行（Python 异常、Killed 等） */
  error_line?: string
  /** 日志中第一个 Python traceback 的字节偏移 */
  traceback_offset?: number
  queue_pos?: number
  wait_reason?: string
}

export type TraceSource = 'output_dir' | 'danlu_json' | 'guess'

export interface TraceMatch {
  job: Job
  source: TraceSource
}

export interface TraceResponse {
  path: string
  matches: TraceMatch[]
}

export interface ValueChange {
  name: string
  a: string | null
  b: string | null
}

export interface FileChange {
  path: string
  kind: 'added' | 'removed' | 'changed'
}

export interface JobComparison {
  a: Job
  b: Job
  command_a: string[]
  command_b: string[]
  env: ValueChange[]
  code: { files: FileChange[] | null; error?: string; partial?: boolean }
  packages: ValueChange[] | null
  packages_error: string
}

export interface QueueState {
  paused: boolean
  kind?: 'manual' | 'fail_fast' | 'disk'
  reason?: string
  since?: number
  job_id?: number
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

export interface HostSample {
  at: number
  cpu_percent: number | null
  memory_used_bytes: number
  memory_total_bytes: number
  memory_percent: number
}

export interface Disk {
  id: string
  path: string
  paths: string[]
  total_bytes: number
  available_bytes: number
  data: boolean
  warn: boolean
  error?: string
}

export interface HostMetricsResponse {
  samples: HostSample[]
  updated_at: number
  error?: string
  disks?: Disk[]
  disks_updated_at?: number
  disks_error?: string
}

export interface Me {
  username: string
  version: string
  /** 旧版服务器没有这个字段 */
  api_version?: number
  hostname: string
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
  terminal_enabled?: boolean
  totp_enabled?: boolean
  ca_fingerprint?: string
  notify?: NotifyStatus
}

export interface DoctorCheck {
  id: string
  level: 'ok' | 'warn' | 'fail'
  title: string
  detail: string
  fix?: string
}

export interface DoctorReport {
  checks: DoctorCheck[]
}

export interface TerminalSession {
  id: string
  workdir: string
  opened_at: number
  attached: boolean
}

export interface NotifyStatus {
  enabled: boolean
  last_sent_at: number | null
  last_error: string
}

export interface DeviceToken {
  id: number
  name: string
  created_at: number
  last_used_at: number | null
  last_ip: string
}

export interface AuditEntry {
  id: number
  ts: number
  ip: string
  action: string
  detail: string
}

export interface SubmitReq {
  name: string
  workdir: string
  conda_env: string
  cmd: string
  env: Record<string, string>
  inherit_bashrc: boolean
  gpus: number
  pinned_gpus: number[]
  rerun_of: number | null
  code_from?: number
}

export interface JobsQuery {
  workdir?: string
  starred?: boolean
  status?: string
  q?: string
  page?: number
  size?: number
  /** unix 秒，只要这之后结束的 */
  ended_after?: number
  /** 'ended'：只要已结束的，按结束时间倒序，配合 before / before_id 翻页 */
  order?: 'ended'
  /** 上一页最后一条的结束时间和 id */
  before?: number
  before_id?: number
}

export interface LogChunk {
  data: string
  start: number
  next: number
  size: number
}
