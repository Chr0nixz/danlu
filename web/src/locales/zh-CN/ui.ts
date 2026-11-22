import type { Catalog } from '@danlu/core'

// 命名空间 ui 的面板文案（源语言）。键以 "ui." 开头。
export const ui = {
  // Commit 短哈希
  'ui.commit_dirty': '{sha}（有未提交的改动）',
  'ui.commit_dirty_sr': '，有未提交的改动',
  'ui.commit_view_code': '{title}，查看这次运行的代码',
  // 读屏播报：同一轮里多条之间的分隔
  'ui.announce_separator': '；',
  // 页面出错兜底（ErrorBoundary）
  'ui.error_chunk_title': '页面没能加载',
  'ui.error_title': '这个页面出错了',
  'ui.error_chunk_body': '页面文件读取失败：丹炉可能刚升级、服务正在重启，或者网络断了。重新加载会取到最新的页面。',
  'ui.error_body': '有新数据时会自动重试，也可以手动重新加载，任务不受影响。反复出现时，把下面这行报错记下来。',
  'ui.error_reload': '重新加载',
  'ui.back_home': '回到总览',
  // 快捷键一览
  'ui.shortcuts_title': '快捷键',
  'ui.shortcut_search': '搜索任务，或粘贴产物路径反查任务',
  'ui.shortcut_move': '在总览和任务页移到下一个、上一个任务',
  'ui.shortcut_open': '打开选中的任务',
  'ui.shortcut_submit': '在新建页提交任务',
  'ui.shortcut_esc': '清空搜索，关闭菜单和确认框',
  'ui.shortcut_help': '打开这份快捷键一览',
  // 复制到剪贴板
  'ui.copy_https_only': '浏览器只在 HTTPS 下允许复制',
  'ui.copy_denied': '浏览器拒绝了复制',
  'ui.copy_failed_sr': '复制失败，{why}',
  'ui.copy_failed_title': '复制失败：{why}，请手动选中',
} satisfies Catalog
