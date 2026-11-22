import type { Catalog } from '@danlu/core'

// 命名空间 layout 的面板文案（源语言）。键以 "layout." 开头。
export const layout = {
  'layout.title': '丹炉 Danlu',
  'layout.title_host': '丹炉 · {hostname}',
  'layout.title_running': '{n} 运行',
  'layout.title_failed': '{n} 失败',
  'layout.brand': '丹炉',
  'layout.skip_to_content': '跳到主要内容',
  'layout.current_server': '当前服务器',
  'layout.nav_overview': '总览',
  'layout.nav_resources': '资源',
  'layout.nav_jobs': '任务',
  'layout.nav_terminal': '终端',
  'layout.nav_settings': '设置',
  'layout.nav_new': '新建',
  'layout.new_job': '新建任务',
  'layout.new_job_title': '新建任务（N）',
  'layout.terminal_aria': '网页终端',
} satisfies Catalog
