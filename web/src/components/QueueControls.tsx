import { useMutation, useMutationState, useQueryClient } from '@tanstack/react-query'
import { Pause, Play } from 'lucide-react'
import { api } from '../lib/api'
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
  const reason = [queue.data.reason, queue.data.job_id && `#${queue.data.job_id}`].filter(Boolean).join(' · ')

  return (
    <div className="space-y-2">
      <Alert tone={queue.data.kind === 'manual' ? 'info' : 'warn'} role="status">
        <div className="flex flex-wrap items-center gap-3">
          <span className="min-w-0 flex-1">队列已暂停：{reason || '暂不启动排队任务'}</span>
          <Button size="sm" className="max-sm:h-10" loading={resume.isPending} onClick={() => resume.mutate()}>
            <Play aria-hidden />
            继续队列
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
      title="暂停队列，运行中的任务继续"
      className="-mr-2 text-xs text-fg-3 max-sm:-my-1 max-sm:h-11"
      loading={pause.isPending}
      onClick={() => pause.mutate()}
    >
      <Pause aria-hidden />
      暂停队列
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
  const message = error instanceof Error ? error.message : String(error)
  return <ErrorBox error={`暂停队列失败：${message}。请点上方暂停按钮重试。`} className="mx-3 my-2 sm:mx-4" />
}
