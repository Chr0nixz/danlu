import { useMutation, useMutationState, useQueryClient } from '@tanstack/react-query'
import { Pause, Play } from 'lucide-react'
import { apiErrorText, queuePauseText } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { useQueueState } from '../lib/queries'
import { Alert, Button, ErrorBox } from './ui'

const pauseMutationKey = ['queue', 'pause'] as const

export function QueueBanner() {
  const queue = useQueueState()
  const queryClient = useQueryClient()
  const resume = useMutation({
    mutationFn: api.resumeQueue,
    onSuccess: (state) => {
      queryClient.setQueryData(['queue'], state)
      void queryClient.invalidateQueries({ queryKey: ['jobs'] })
    },
  })

  if (!queue.data?.paused) return <ErrorBox error={queue.error} />
  const reason = queuePauseText(queue.data)

  return (
    <div className="space-y-2">
      <Alert tone={queue.data.kind === 'manual' ? 'info' : 'warn'} role="status">
        <div className="flex flex-wrap items-center gap-3">
          <span className="min-w-0 flex-1">{t('jobs.queue_paused', { reason: reason || t('jobs.queue_paused_fallback') })}</span>
          <Button size="sm" loading={resume.isPending} onClick={() => resume.mutate()}>
            <Play aria-hidden />
            {t('jobs.resume_queue')}
          </Button>
        </div>
      </Alert>
      <ErrorBox error={resume.error} />
    </div>
  )
}

export function QueuePauseButton() {
  const queue = useQueueState()
  const queryClient = useQueryClient()
  const pause = useMutation({
    mutationKey: pauseMutationKey,
    mutationFn: api.pauseQueue,
    onSuccess: (state) => queryClient.setQueryData(['queue'], state),
  })

  if (!queue.data || queue.data.paused) return null
  // 写出文字：手机上看不到悬停提示，只有图标分不清是暂停队列还是暂停任务
  return (
    <Button
      variant="ghost"
      size="sm"
      title={t('jobs.pause_queue_title')}
      // 手机上按钮比 36px 的分组标题高，上下各伸出 4px；z-10 让伸出去的部分也点得到，不被相邻的任务行盖住
      className="relative z-10 -mr-2 text-xs text-fg-3 max-sm:-my-1"
      loading={pause.isPending}
      onClick={() => pause.mutate()}
    >
      <Pause aria-hidden />
      {t('jobs.pause_queue')}
    </Button>
  )
}

/** 失败时在标题下方完整展示，避免错误信息挤进固定高度的标题栏。 */
export function QueuePauseError() {
  const queue = useQueueState()
  const errors = useMutationState({
    filters: { mutationKey: pauseMutationKey, exact: true },
    select: (mutation) => mutation.state.error,
  })
  const error = errors[errors.length - 1]
  if (!error || queue.data?.paused) return null
  return <ErrorBox error={t('jobs.pause_failed', { message: apiErrorText(error) })} className="mx-3 my-2 sm:mx-4" />
}
