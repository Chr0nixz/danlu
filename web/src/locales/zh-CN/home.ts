import type { Catalog } from '@danlu/core'

// 命名空间 home 的面板文案（源语言）。键以 "home." 开头。
// 覆盖总览页：摘要行（含 GPU 空闲数与数据新鲜度）、运行/排队/最近完成分组。
// "运行中""失败"与 core 的 status.* 完全一致，直接复用 core 键。
export const home = {
  'home.title': '总览',

  // 摘要行
  'home.queued': '排队',
  'home.failed_title': '最近 24 小时内失败的任务',
  'home.within_24h': '24 小时内',
  'home.free_short': '空闲',
  'home.free_cards': '张卡空闲',
  'home.gpu_updated_title': 'GPU 数据的更新时间',
  'home.gpu_stale': 'GPU 数据 {n} 秒没有更新',
  'home.live': '实时',

  // 分组标题与空状态
  'home.silent_count': '{n} 个无新输出',
  'home.running_empty': '炉子空着，没有正在运行的任务',
  'home.queued_empty': '队列是空的',
  'home.recent_title': '最近完成',
  'home.all_jobs': '全部任务',
  'home.recent_empty': '还没有出炉的任务',
} satisfies Catalog