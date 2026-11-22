import type { Message } from '@danlu/core'
import { compare } from './en/compare'
import { gpus } from './en/gpus'
import { home } from './en/home'
import { job } from './en/job'
import { jobs } from './en/jobs'
import { layout } from './en/layout'
import { login } from './en/login'
import { newjob } from './en/newjob'
import { notfound } from './en/notfound'
import { reservations } from './en/reservations'
import { resources } from './en/resources'
import { settings } from './en/settings'
import { terminal } from './en/terminal'
import { ui } from './en/ui'
import type { zhCN } from './zh-CN'

// 键集必须与 zh-CN 完全一致：缺键、多键都会在 typecheck 报错。
export const en: { [K in keyof typeof zhCN]: Message } = {
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
}