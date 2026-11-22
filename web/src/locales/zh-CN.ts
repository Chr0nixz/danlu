import type { Catalog } from '@danlu/core'
import { compare } from './zh-CN/compare'
import { gpus } from './zh-CN/gpus'
import { home } from './zh-CN/home'
import { job } from './zh-CN/job'
import { jobs } from './zh-CN/jobs'
import { layout } from './zh-CN/layout'
import { login } from './zh-CN/login'
import { newjob } from './zh-CN/newjob'
import { notfound } from './zh-CN/notfound'
import { reservations } from './zh-CN/reservations'
import { resources } from './zh-CN/resources'
import { settings } from './zh-CN/settings'
import { terminal } from './zh-CN/terminal'
import { ui } from './zh-CN/ui'

// 面板文案目录（源语言），按命名空间拆分文件，聚合导出。
// 键名扁平点号、以命名空间开头；禁止在调用点运行时拼键。
// en.ts 的键集必须与它完全一致（类型强制）。约定见 docs/I18N.md。
export const zhCN = {
  ...compare,
  ...gpus,
  ...home,
  ...job,
  ...jobs,
  ...layout,
  ...login,
  ...newjob,
  ...notfound,
  ...reservations,
  ...resources,
  ...settings,
  ...terminal,
  ...ui,
} satisfies Catalog