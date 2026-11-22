import type { Catalog } from '@danlu/core'

// 命名空间 jobs 的面板文案（源语言）。键以 "jobs." 开头。
// 覆盖任务页（Jobs）、任务行（JobRow，含取消确认框）和队列控制（QueueControls）。
// 状态词与 core 的 status.* 完全一致时直接复用 core 键，这里只收页面自己的文案。
export const jobs = {
  // 任务页：标题、筛选、搜索、对比选择
  'jobs.title': '任务',
  'jobs.filter_all': '全部',
  'jobs.filter_queued': '排队',
  'jobs.filter_by_status': '按状态筛选',
  'jobs.starred_only': '只看标星',
  'jobs.selection_and': ' 与 ',
  'jobs.select_two_hint': '请选择两个任务',
  'jobs.view_compare': '查看对比',
  'jobs.exit_selection': '退出选择',
  'jobs.compare': '对比',
  'jobs.search_label': '搜索任务',
  'jobs.search_placeholder': '搜索任务，或粘贴产物路径',
  'jobs.search_clear': '清空搜索',
  'jobs.recent_24h': '最近 24 小时内结束',
  'jobs.recent_remove_label': '去掉条件：最近 24 小时内结束',
  'jobs.batch_finished': '本批已结束任务',
  'jobs.batch_show_all_label': '查看全部批次',

  // 任务页：路径反查来源、空状态、分页
  'jobs.trace_source': '来源：{source}',
  'jobs.trace_source_output_dir': '输出目录',
  'jobs.trace_source_run_code': '快照重跑目录',
  'jobs.trace_source_guess': '可能是',
  'jobs.trace_empty': '没有找到匹配的任务',
  'jobs.filter_empty': '没有符合条件的任务',
  'jobs.empty': '还没有任务，炉子还没点火',
  'jobs.new_job': '新建任务',
  'jobs.pagination_range': '第 {from}–{to} 条，共 {total} 条',
  'jobs.prev_page': '上一页',
  'jobs.next_page': '下一页',

  // 任务行：取消确认框
  'jobs.cancel_dialog_title': '取消任务 #{id}？',
  'jobs.keep_running': '继续运行',
  'jobs.keep_queued': '保留排队',
  'jobs.cancel_job': '取消任务',
  'jobs.cancel_job_label': '取消任务 #{id}',
  'jobs.cancel_running_body': '已经运行 {duration}。确定后进程会被结束，不能恢复；日志和代码快照会保留。',
  'jobs.cancel_queue_pos': '排在第 {pos} 位，',
  'jobs.cancel_queued_body': '取消后从队列里移除。',

  // 任务行：状态播报与说明文字
  'jobs.status_announce': '任务 #{id} {name}：{status}',
  'jobs.queue_pos': '第 {pos} 位',
  'jobs.wait_pinned': '等 GPU {list}',
  'jobs.wait_gpus': '等 {n} 张卡',
  'jobs.cancelling_detail': '正在结束进程…',
  'jobs.pruned_detail': '日志和代码快照已清理',
  'jobs.not_started': '未开始运行',
  'jobs.waiting_output': '等待输出…',
  'jobs.no_output': '没有输出',

  // 任务行：时间列、标记与操作
  'jobs.waited': '等 {duration}',
  'jobs.time_waited': '已等待',
  'jobs.time_running': '已运行',
  'jobs.duration': '耗时',
  'jobs.select_job_label': '选择任务 #{id} {name}',
  'jobs.starred_label': '已标星',
  'jobs.silent_for': '无输出 {duration}',
  'jobs.remaining_title': 'tqdm 对当前进度条的剩余时间估计；如果按 epoch 统计，这里是当前 epoch 的剩余时间',
  'jobs.remaining': '剩 {remaining}',
  'jobs.ended_at': '结束于',
  'jobs.copy_job': '复制任务',
  'jobs.copy_job_label': '复制任务 #{id}',

  // 任务行：电脑端列头
  'jobs.col_job': '任务',
  'jobs.col_output': '卡 · 最新输出',
  'jobs.col_code': '代码',

  // 队列控制：暂停横幅、暂停/继续按钮。reason 是服务器文本，原样插值，合成在后续阶段
  'jobs.queue_paused': '队列已暂停：{reason}',
  'jobs.queue_paused_fallback': '暂不启动排队任务',
  'jobs.resume_queue': '继续队列',
  'jobs.pause_queue': '暂停队列',
  'jobs.pause_queue_title': '暂停队列，运行中的任务继续',
  'jobs.pause_failed': '暂停队列失败：{message}。请点上方暂停按钮重试。',
} satisfies Catalog