// 主窗口的右侧分栏：任务详情或提交表单。总览和历史页共用。

import { projectOf, type Job } from '@danlu/core'
import { createContext, useContext } from 'react'
import { placementFor } from './local'

/** 提交表单的初始内容。复制任务、重跑都从这里来 */
export interface Draft {
  server: string
  name: string
  workdir: string
  conda_env: string
  cmd: string
  env: Record<string, string>
  inherit_bashrc: boolean
  gpus: number
  pinned: number[]
  rerun_of: number | null
  /** 从哪个任务复制来的（gpu1-42），显示在表单顶部 */
  source?: string
  /** 复制来的任务所属的项目：换到另一台、目录还空着时，用它找目录和命令历史 */
  project?: string
}

export type Pane = { kind: 'job'; server: string; id: number } | { kind: 'submit'; draft: Draft | null } | null

export interface Workspace {
  pane: Pane
  openJob(server: string, id: number): void
  openSubmit(draft?: Draft): void
  closePane(): void
  /** 标题栏的搜索框内容，总览和历史页用它筛选任务 */
  search: string
}

export const WorkspaceCtx = createContext<Workspace | null>(null)

export function useWorkspace() {
  const w = useContext(WorkspaceCtx)
  if (!w) throw new Error('useWorkspace 必须在 WorkspaceCtx 里用')
  return w
}

/**
 * 把一个任务复制成提交表单的草稿，可以换一台服务器。
 * 换到另一台时，目录和环境按"项目 × 服务器"的记录填；第一次复制到这台时留空让人选。
 */
export function draftFrom(job: Job, from: string, to = from): Draft {
  const same = from === to
  const place = same ? { workdir: job.workdir, conda_env: job.conda_env } : placementFor(projectOf(job.workdir), to)
  return {
    server: to,
    name: job.name,
    workdir: place?.workdir ?? '',
    conda_env: place?.conda_env ?? '',
    cmd: job.cmd,
    env: { ...job.env },
    inherit_bashrc: job.inherit_bashrc ?? false,
    gpus: job.gpus_req,
    // 指定的卡号只在同一台上有意义
    pinned: same ? job.gpus_pinned : [],
    rerun_of: same ? job.id : null,
    source: `${from}-${job.id}`,
    project: projectOf(job.workdir),
  }
}
