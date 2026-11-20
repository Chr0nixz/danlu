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

export interface ResourceProfile {
  gpu?: {
    avg_util: number
    samples: number
    covered_seconds: number
    cards: { index: number; peak_mem_mb: number; total_mem_mb: number }[]
  }
  host?: { peak_rss_bytes: number; samples: number; covered_seconds: number }
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
  memo?: string
  notify_done?: boolean
  profile?: ResourceProfile
  silent_since?: number
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

export type TraceSource = 'output_dir' | 'run_code' | 'danlu_json' | 'guess'

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
  /** 本实例 danlu 任务的编号；0 表示不是 danlu 任务的进程 */
  job_id: number
  /** 图形进程（EGL/OpenGL 渲染），nvidia-smi 的 compute-apps 列不出来，由服务端另外补查 */
  graphics?: boolean
}

export interface GPU {
  index: number
  uuid: string
  name: string
  util: number
  mem_used_mb: number
  mem_total_mb: number
  /** 进程列表无法解释的显存，超过服务端阈值时该卡不可分配 */
  unknown_mem_mb?: number
  temp_c: number
  power_w: number
  /** 资源详情页的扩展指标；老驱动可能没有这些字段 */
  fan_percent?: number
  clock_graphics_mhz?: number
  clock_memory_mhz?: number
  power_limit_w?: number
  pstate?: string
  /** 卡上能认出的全部进程，包括分到这张卡的任务自己的进程 */
  procs: Proc[]
  job: { id: number; name: string } | null
  /** 不属于这张卡上任务的进程：外部进程，或者 job_id 不为 0 时是别的任务跑到了这张卡上（也可能是已结束任务的残留） */
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

export type ResourceRange = '1m' | '5m' | '15m' | '1h'

export interface ResourceHost {
  hostname: string
  os?: string
  kernel?: string
  cpu_model?: string
  cpu_cores: number
  memory_total_bytes: number
  swap_total_bytes: number
  uptime_seconds: number
  boot_time?: number
}

export interface ResourceCPU {
  percent: number | null
  /** 每核百分比，-1 表示还没有第二次采样 */
  cores: number[]
  load_1: number
  load_5: number
  load_15: number
  freq_mhz: number | null
  freq_max_mhz: number | null
  temp_c: number | null
  power_w: number | null
  /** CPU 功耗读不到的原因（多半是内核只允许 root 读能量计数器）；还在等第二次读数时没有 */
  power_error?: string
}

export interface ResourceMemory {
  total_bytes: number
  used_bytes: number
  available_bytes: number
  free_bytes: number
  cached_bytes: number
  buffers_bytes: number
  percent: number
  swap_total_bytes: number
  swap_used_bytes: number
  swap_percent: number
}

export interface ResourceNetIface {
  name: string
  virtual: boolean
  speed_mbps?: number
  rx_bytes_per_sec: number
  tx_bytes_per_sec: number
  rx_total_bytes: number
  tx_total_bytes: number
  rx_errors: number
  tx_errors: number
  rx_drops: number
  tx_drops: number
}

export interface ResourceDiskIO {
  name: string
  read_bytes_per_sec: number
  write_bytes_per_sec: number
  read_total_bytes: number
  write_total_bytes: number
  busy_percent: number
}

export interface ResourceProcess {
  pid: number
  user: string
  command: string
  cpu_percent: number
  rss_bytes: number
  job_id: number
  job_name?: string
}

export interface ResourceSnapshot {
  /** 最近一次成功采样的时间；0 表示还没有成功过，这时其它字段都是零值 */
  updated_at: number
  samples: number
  process_samples: number
  host: ResourceHost
  cpu: ResourceCPU
  memory: ResourceMemory
  gpu_power_w: number | null
  gpu_temp_max_c: number | null
  power_w: number | null
  energy_wh: number | null
  net_rx_bps: number
  net_tx_bps: number
  disk_read_bps: number
  disk_write_bps: number
  network: ResourceNetIface[]
  disk_io: ResourceDiskIO[]
  processes: ResourceProcess[]
  processes_at: number
  root_disk?: Disk
  root_disk_at?: number
  disks: Disk[]
  disks_updated_at?: number
  disks_error?: string
  error?: string
  network_error?: string
  disk_io_error?: string
  processes_error?: string
}

export interface ResourcePoint {
  at: number
  cpu_percent: number | null
  memory_percent: number
  swap_percent: number
  net_rx_bps: number
  net_tx_bps: number
  disk_read_bps: number
  disk_write_bps: number
  cpu_power_w: number | null
  gpu_power_w: number | null
}

export interface ResourcePeaks {
  cpu_percent: number | null
  memory_percent: number
  swap_percent: number
  net_rx_bps: number
  net_tx_bps: number
  disk_read_bps: number
  disk_write_bps: number
  cpu_power_w: number | null
  gpu_power_w: number | null
}

export interface ResourceHistory {
  range: ResourceRange
  from: number
  to: number
  updated_at: number
  points: ResourcePoint[]
  peaks: ResourcePeaks
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
  queue_drained?: boolean
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
  memo?: string
  notify_done?: boolean
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
  finished_from_event?: number
  finished_to_event?: number
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

export interface LogSearchResult {
  matches: { offset: number; text: string }[]
  scanned: number
  size: number
  truncated: boolean
}
