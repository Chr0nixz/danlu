import type { Catalog } from '@danlu/core'

// 命名空间 gpus 的面板文案（源语言）。键以 "gpus." 开头。
// 覆盖 GPU 卡片（GpuGrid）与选卡器（GpuPicker）：卡状态、进程标签与 tooltip、
// 预约占卡行、指标名、读取失败提示；选卡交互文案由新建任务和预约对话框共用。
export const gpus = {
  // 卡状态与指标
  'gpus.idle': '空闲',
  'gpus.unknown_mem': '未知显存占用',
  'gpus.unknown_mem_title': 'nvidia-smi 列出的进程解释不了这部分显存（比如别的容器里的进程），这张卡不会被分配',
  'gpus.util': '利用率',
  'gpus.mem': '显存',

  // 占卡进程标签（单个进程 / 按任务或用户合并后的多个进程）
  'gpus.proc_job': '#{id} 的进程',
  'gpus.proc_job_graphics': '#{id} 的图形进程',
  'gpus.proc_external': '外部进程',
  'gpus.proc_owner_unknown': '归属不明的进程（可能正在退出）',
  'gpus.proc_external_graphics': '外部图形进程',
  'gpus.job_proc_count': '#{job} 的 {n} 个进程',
  'gpus.external_proc_count': '{n} 个外部进程',
  'gpus.no_procs': '卡上暂无该任务的进程',
  'gpus.no_procs_short': '未见进程',
  'gpus.no_procs_title': '这张卡分给了这个任务，但卡上还没有它的进程：可能还在启动，也可能程序没有用这张卡',

  // 进程 tooltip：head 是前半段（pid 列表、用户、图形进程数），整句和预约卡版本复用它
  'gpus.stray_user': '，用户 {user}',
  'gpus.stray_graphics': '，其中 {n} 个是图形进程（EGL/OpenGL 渲染，按自己的设备编号选卡，不受 CUDA_VISIBLE_DEVICES 限制）',
  'gpus.stray_cvd': '，进程的 CUDA_VISIBLE_DEVICES 指向 GPU {cvd}',
  'gpus.stray_cvd_unset': '，进程没有 CUDA_VISIBLE_DEVICES',
  'gpus.stray_job_head': '任务 #{job} 的进程（pid {pids}{kinds}）',
  'gpus.stray_external_head': '外部进程（pid {pids}{user}{kinds}）',
  'gpus.stray_job_title': '{head}，但这张卡没有分给它：程序自己选了卡，或是已结束任务的残留。这张卡不会被分配',
  'gpus.stray_external_title': '{head}：不是 danlu 启动的，这张卡不会被分配',
  'gpus.stray_reserved_title': '{head}：在预约的卡上运行',

  // 预约占卡行
  'gpus.reservation_label': '预约 #{id}',
  'gpus.res_claiming': '收卡中',
  'gpus.res_until': '到 {time}',
  'gpus.res_manual': '手动释放',
  'gpus.res_line_title': '预约 #{id}：丹炉不会把这张卡分给任务',
  'gpus.res_line_title_note': '预约 #{id}（{note}）：丹炉不会把这张卡分给任务',

  // 选卡交互（新建任务与预约对话框共用）
  'gpus.mode_count': '按张数',
  'gpus.mode_pinned': '指定卡号',
  'gpus.count_less': '少一张',
  'gpus.count_more': '多一张',
  'gpus.pick_required': '请至少选择一张卡',
  'gpus.picker_job': '#{id} 在用',

  // 读取失败与空状态
  'gpus.load_failed': '读取 GPU 失败：{message}',
  'gpus.sample_failed': 'GPU 信息读取失败，暂停分配新任务：{error}',
  'gpus.error_since': '（已持续 {duration}）',
  'gpus.error_duration_s': '{n} 秒',
  'gpus.error_duration_m': '{n} 分钟',
  'gpus.error_duration_hm': '{h} 小时 {m} 分钟',
  'gpus.none_detected': '没有检测到 GPU。只能提交不用 GPU 的任务。',
} satisfies Catalog
