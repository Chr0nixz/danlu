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
  /** 失败细分码（host_oom 三态、信号名）；start 类是不带前缀的原始启动错误。旧版服务器没有这个字段 */
  fail_detail?: string
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
  /** 严格限卡任务所在的 systemd 服务名，空串表示普通任务；旧版服务器没有这个字段 */
  unit?: string
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
  /** 提交幂等键（命令行 danlu submit --key）；没有时不出现 */
  submit_key?: string
  /** 暂缓：排队任务留在原位，调度时跳过，也不挡后面的任务；没有暂缓时不出现。旧版服务器没有这个字段 */
  held?: boolean
  /** 只在单个任务详情里有：排队期间修改过 */
  edited?: boolean
  created_at: number
  started_at: number | null
  ended_at: number | null
  last_line?: string
  /** 失败任务日志里最后一条像报错的行（Python 异常、Killed 等） */
  error_line?: string
  /** 日志中第一个 Python traceback 的字节偏移 */
  traceback_offset?: number
  queue_pos?: number
  /** 中文原文，旧服务器只有它；新服务器同时给结构化的 wait，显示时用 waitText 合成 */
  wait_reason?: string
  wait?: QueueWait
  /** 只在单个任务详情里有：重跑自的任务和它的状态 */
  rerun_parent?: JobRef
  /** 只在单个任务详情里有：从这个任务重跑出来的任务（最多 5 个） */
  reruns?: JobRef[]
}

export interface JobRef {
  id: number
  status: JobStatus
}

/** 排队任务在等什么（internal/scheduler/wait.go） */
export interface QueueWait {
  kind: 'ahead' | 'held' | 'scheduling' | 'queue_state' | 'paused' | 'disk_check' | 'gpu_data' | 'strict' | 'res_loading' | 'gpus' | 'pinned'
  need?: number
  free?: number
  pinned?: number[]
  blocker?: QueueWaitBlocker
}

export interface QueueWaitBlocker {
  kind: 'reserved' | 'unknown_mem' | 'external' | 'job_proc' | 'owner_unknown'
  gpu: number
  reservation_id?: number
  mem_mb?: number
  pid?: number
  job_id?: number
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
  /** 快速失败任务从启动到失败的秒数，供本地化合成；旧版服务器没有这个字段 */
  fail_seconds?: number
  /** 磁盘暂停时的余量与暂停线（GiB，一位小数）；旧版服务器没有这些字段 */
  disk_available_gb?: number
  disk_min_gb?: number
}

export interface Proc {
  pid: number
  used_mem_mb: number
  user: string
  /** 本实例 danlu 任务的编号；0 表示不是 danlu 任务的进程 */
  job_id: number
  /** 图形进程（EGL/OpenGL 渲染），nvidia-smi 的 compute-apps 列不出来，由服务端另外补查 */
  graphics?: boolean
  /** 任务进程启动时的 CUDA_VISIBLE_DEVICES（空串表示没设），GPU UUID 已换成卡号；只有本实例任务的进程有，旧版服务器没有这个字段 */
  cuda_visible?: string
  /** 认不出归属：和 danlu 同一用户的进程正在退出、是僵尸或读超时（job_id 为 0）。卡照样算占用，但不当外部进程告警 */
  owner_unknown?: boolean
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
  /** 收下这张卡的 GPU 预约，丹炉不会把它分给任务。卡上用户自己的进程仍列在 external 里。旧版服务器没有这个字段 */
  reservation?: GPUReservationRef | null
}

export type ReservationStatus = 'waiting' | 'active' | 'ended'

export interface GPUReservationRef {
  id: number
  note: string
  status: ReservationStatus
  ends_at: number | null
}

/** GPU 预约：给丹炉以外的任务占卡。waiting 还在收卡（可能已收了一部分），active 卡已全部到位 */
export interface Reservation {
  id: number
  note: string
  /** 一共要几张卡 */
  gpus: number
  pinned_gpus: number[]
  /** 已经收下的卡 */
  held_gpus: number[]
  /** 卡全部到位后保留多少秒；null 表示手动释放前一直保留 */
  duration: number | null
  status: ReservationStatus
  created_at: number
  ready_at: number | null
  ends_at: number | null
  ended_at: number | null
  end_reason: string
}

export interface ReservationReq {
  gpus: number
  pinned_gpus: number[]
  /** 秒，600–604800；null 表示手动释放前一直保留 */
  duration: number | null
  note: string
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

/** 丹炉自身占用的一类文件（db/logs/snapshots/run/envs/backup/other） */
export interface SelfUsagePart {
  name: string
  bytes: number
  files: number
  error?: string
}

/** 数据库大小：逻辑大小按页数×页大小（含空闲页），文件字节另计 WAL/SHM */
export interface SelfDB {
  file_bytes: number
  wal_bytes: number
  shm_bytes: number
  logical_bytes: number
  free_bytes: number
  page_size: number
  page_count: number
  free_pages: number
}

/** 数据目录自身占用，每 30 秒统计一次 */
export interface SelfUsage {
  path: string
  total_bytes: number
  files: number
  parts: SelfUsagePart[]
  db?: SelfDB
  error?: string
  db_error?: string
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
  /** 丹炉自身占用；还没有成功统计过时没有 */
  self?: SelfUsage
  self_updated_at?: number
  self_error?: string
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
  /** 丹炉数据目录总量和数据库逻辑大小；自身统计不可用时为 null。旧版服务器没有这两个字段 */
  data_bytes?: number | null
  db_bytes?: number | null
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
  data_bytes?: number | null
  db_bytes?: number | null
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
  /** 旧版服务器没有这个字段 */
  security?: SecuritySettings
  /** 面板上能改的调度与告警设置。旧版服务器没有这个字段 */
  runtime?: RuntimeSettings
  /** 面板上能改的数据保留设置。旧版服务器没有这个字段 */
  retention?: RetentionSettings
  /** 服务启动后最近一次自动清理；还没清理过（或保留策略关闭）时没有 */
  prune?: PruneStatus
  /** 手机和电脑共用的面板偏好。旧版服务器没有这个字段 */
  prefs?: Partial<Prefs>
  /** 严格限卡（cgroup 设备控制）。旧版服务器没有这个字段 */
  strict?: StrictStatus
}

/** 严格限卡助手不可用的原因代码 */
export type StrictReason = 'checking' | 'not_installed' | 'no_sudo' | 'protocol' | 'failed' | 'unsupported'

/**
 * 严格限卡：开启后每个任务在只放行分到的卡的 systemd 服务里运行，需要服务器上 sudo 安装一次 root 助手。
 * installed 只看助手文件在不在；ok/reason 是上次经 sudo 检查的结果，从没查过时 reason 是 checking
 */
export interface StrictStatus {
  enabled: boolean
  installed: boolean
  ok: boolean
  reason?: StrictReason
  detail?: string
  helper_version?: string
  checked_at?: number
  /** 在服务器上运行的安装命令，带 danlu 的完整路径 */
  install_cmd: string
}

/**
 * 面板上能改的一项运行设置。时长以秒为单位，大小以 GB 或 MB 为单位，见 unit。
 * custom 表示面板上改过（存在数据库里），这时 config.toml 里的同名项不生效；恢复后取 default。
 */
export interface SettingItem {
  value: number
  default: number
  custom: boolean
  min: number
  max: number
  /** 0 表示关闭 */
  zero_off: boolean
  unit: 's' | 'GB' | 'MB' | 'day'
}

export type RuntimeKey =
  | 'fail_fast'
  | 'kill_grace'
  | 'unknown_gpu_memory_mb'
  | 'disk_min_gb'
  | 'disk_warn_gb'
  | 'silence_alert'
  | 'snapshot_max_file_mb'
  | 'snapshot_max_total_mb'
  | 'terminal_session_lifetime'
export type RetentionKey = 'keep_days' | 'max_total_gb'
export type RuntimeSettings = Record<RuntimeKey, SettingItem>
export type RetentionSettings = Record<RetentionKey, SettingItem>

/** 要改的运行设置，只传改了的项；null 恢复成 config.toml 里的值 */
export type SettingsPatch<K extends string> = Partial<Record<K, number | null>>

export interface PruneStatus {
  at: number
  jobs: number
  envs: number
  bytes: number
  error?: string
}

export interface RetentionPreviewItem {
  job_id: number
  name: string
  bytes: number
  reason: string
  /** resume/age/size，供本地化；旧版服务器没有这个字段，回退 reason 原文 */
  reason_code?: string
}

/** 按新的保留策略会清理什么；不删任何东西 */
export interface RetentionPreview {
  jobs: number
  envs: number
  bytes: number
  /** 当前任务文件总量 */
  total_bytes: number
  /** 最多 50 条 */
  items: RetentionPreviewItem[]
  errors: string[]
}

/** 面板偏好：存在服务器上，手机和电脑共用 */
export interface Prefs {
  new_job_gpus: number
  new_job_bashrc: boolean
  new_job_notify_done: boolean
  /** auto：自动选第一个非 base 环境；none：不预选 */
  new_job_conda: 'auto' | 'none'
  jobs_page_size: 20 | 50 | 100
  log_wrap: boolean
  warn_cpu_percent: number
  warn_mem_percent: number
  warn_swap_percent: number
  warn_gpu_temp_c: number
  /** 服务端 ntfy 推送的语言；面板显示语言是各设备本地的，与它解耦 */
  notify_lang: 'zh-CN' | 'en'
}

/** 要改的面板偏好，只传改了的项；null 恢复默认 */
export type PrefsPatch = { [K in keyof Prefs]?: Prefs[K] | null }

/** 面板上能改的安全设置，保存后立即生效。时长都以秒为单位 */
export interface SecuritySettings {
  entrance: string
  /** 不带入口的地址，如 https://203.0.113.10:28457 */
  base_url: string
  /** 带安全入口的完整访问地址 */
  url: string
  session_ttl: number
  max_failures: number
  lockout: number
}

/** 要改的安全设置，只传改了的项 */
export type SecurityPatch = Partial<Pick<SecuritySettings, 'entrance' | 'session_ttl' | 'max_failures' | 'lockout'>>

/** 用密码登录的浏览器会话。设备令牌不在这里 */
export interface LoginSession {
  /** 令牌哈希的前 16 位，只用来注销 */
  id: string
  created_at: number
  expires_at: number
  ip: string
  user_agent: string
  /** 是不是发起这次请求的浏览器 */
  current: boolean
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
  /** 提交幂等键：同一个 key 只建一个任务，重复提交返回已有的那个（200，新建是 201） */
  submit_key?: string
}

/** 排队任务的位置调整：置顶、上移、下移、移到末尾 */
export type QueueMove = 'top' | 'up' | 'down' | 'bottom'

/** 对一批排队任务的同一个操作。整批全成或全不成 */
export type BatchAction = 'cancel' | 'replace' | 'hold' | 'release' | 'top'

export interface BatchReq {
  ids: number[]
  action: BatchAction
  /** replace 用：命令里的 find 全部换成 replace */
  find?: string
  replace?: string
  /** 只返回预览，不改任何东西 */
  dry_run?: boolean
}

/** 一个任务在批量操作里的结果。changed 为 false 表示不受影响（命令里没有要找的文字、已经是暂缓的等） */
export interface BatchItem {
  id: number
  name: string
  changed: boolean
  /** replace：改前、改后的命令 */
  before?: string
  after?: string
}

export interface BatchResult {
  action: BatchAction
  dry_run: boolean
  items: BatchItem[]
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
  /**
   * 'ended'：只要已结束的，按结束时间倒序，配合 before / before_id 翻页；
   * 'asc'：按 id 从旧到新；只筛排队任务时按队列顺序（置顶、上移后的顺序）。不传时按 id 从新到旧
   */
  order?: 'ended' | 'asc'
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
