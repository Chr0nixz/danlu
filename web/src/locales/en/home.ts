import type { Catalog } from '@danlu/core'

// 命名空间 home 的英文文案。键集必须与 zh-CN/home.ts 完全一致。
export const home = {
  'home.title': 'Overview',

  // Summary line
  'home.queued': 'Queued',
  'home.failed_title': 'Jobs failed in the last 24 hours',
  'home.within_24h': "in the last 24h",
  'home.free_short': 'free',
  'home.free_cards': 'GPUs free',
  'home.gpu_updated_title': 'GPU data update time',
  'home.gpu_stale': 'GPU data not updated for {n}s',
  'home.live': 'Live',

  // Group titles and empty states
  'home.silent_count': '{n} silent',
  'home.running_empty': 'The furnace is idle — no running jobs',
  'home.queued_empty': 'The queue is empty',
  'home.recent_title': 'Recently finished',
  'home.all_jobs': 'All jobs',
  'home.recent_empty': 'Nothing out of the furnace yet',
} satisfies Catalog