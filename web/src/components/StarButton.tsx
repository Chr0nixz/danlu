import { useMutation, useMutationState, useQueryClient } from '@tanstack/react-query'
import { Star } from 'lucide-react'
import type { Job } from '@danlu/core'
import { api } from '../lib/api'
import { Button, cx, ErrorBox, menuItemClass } from './ui'

const mutationKey = ['job', 'star']

function useStar(job: Job) {
  const qc = useQueryClient()
  const star = useMutation({
    mutationKey,
    mutationFn: ({ id, starred }: { id: number; starred: boolean }) => api.star(id, starred),
    onSuccess: (updated) => {
      qc.setQueryData(['job', job.id], updated)
      void qc.invalidateQueries({ queryKey: ['jobs'] })
    },
  })
  return {
    pending: star.isPending,
    toggle: () => star.mutate({ id: job.id, starred: !job.starred }),
    title: job.starred ? '取消标星' : job.pruned_at ? '标星任务（日志和快照已清理）' : '标星保留日志和代码快照',
  }
}

export function StarButton({ job, label = false }: { job: Job; label?: boolean }) {
  const star = useStar(job)
  const action = job.starred ? '取消标星' : '标星'
  return (
    <Button
      size={label ? 'sm' : 'icon-sm'} variant={label ? 'secondary' : 'ghost'}
      className={cx(!label && 'max-md:size-11', job.starred ? 'text-fg' : 'text-fg-3')}
      loading={star.pending} aria-pressed={!!job.starred} aria-label={`${action}任务 #${job.id}`}
      title={star.title}
      onClick={star.toggle}
    >
      <Star aria-hidden className={job.starred ? 'fill-current' : ''} />
      {label && (job.starred ? '已标星' : '标星')}
    </Button>
  )
}

/** 更多操作菜单里的标星项：手机上标星收进菜单，说明它的用处（保留日志和快照），触屏上看不到悬停提示 */
export function StarMenuItem({ job }: { job: Job }) {
  const star = useStar(job)
  return (
    <button type="button" role="menuitem" className={menuItemClass} disabled={star.pending} onClick={star.toggle}>
      <Star aria-hidden className={job.starred ? 'fill-current' : ''} />
      {job.starred ? '取消标星' : <>标星<span className="text-xs text-fg-3">保留日志和代码快照</span></>}
    </button>
  )
}

export function StarError() {
  const results = useMutationState({
    filters: { mutationKey },
    select: (mutation) => ({ error: mutation.state.error, id: (mutation.state.variables as { id?: number } | undefined)?.id }),
  })
  const latest = results.at(-1)
  return latest?.error ? <ErrorBox error={`任务 #${latest.id} 标星修改失败：${latest.error.message}。请重试标星按钮。`} /> : null
}
