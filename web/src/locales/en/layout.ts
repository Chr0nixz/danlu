import type { Catalog } from '@danlu/core'

// 命名空间 layout 的英文文案。键集必须与 zh-CN/layout.ts 完全一致。
export const layout = {
  'layout.title': 'Danlu',
  'layout.title_host': 'Danlu · {hostname}',
  'layout.title_running': '{n} running',
  'layout.title_failed': '{n} failed',
  'layout.brand': 'Danlu',
  'layout.skip_to_content': 'Skip to main content',
  'layout.current_server': 'Current server',
  'layout.nav_overview': 'Overview',
  'layout.nav_resources': 'Resources',
  'layout.nav_jobs': 'Jobs',
  'layout.nav_terminal': 'Terminal',
  'layout.nav_settings': 'Settings',
  'layout.nav_new': 'New',
  'layout.new_job': 'New job',
  'layout.new_job_title': 'New job (N)',
  'layout.terminal_aria': 'Web terminal',
} satisfies Catalog
