import { useQuery } from '@tanstack/react-query'
import type { Prefs } from '@danlu/core'
import { api } from './api'

/** 和服务器 internal/settings/prefs.go 的默认值一致；服务器还没返回或版本较旧时用 */
export const PREF_DEFAULTS: Prefs = {
  new_job_gpus: 1,
  new_job_bashrc: false,
  new_job_notify_done: false,
  new_job_conda: 'auto',
  jobs_page_size: 20,
  log_wrap: true,
  warn_cpu_percent: 90,
  warn_mem_percent: 90,
  warn_swap_percent: 50,
  warn_gpu_temp_c: 83,
  notify_lang: 'zh-CN',
}

/**
 * 手机和电脑共用的面板偏好，存在服务器上，随设置一起返回。
 * ready 之前 prefs 是默认值；拿它当初始值的表单（新建任务）要等 ready 再挂载。
 */
export function usePrefs() {
  const q = useQuery({ queryKey: ['settings'], queryFn: api.settings, staleTime: 60_000, select: (s) => s.prefs })
  return { prefs: { ...PREF_DEFAULTS, ...q.data } as Prefs, ready: !q.isPending }
}
