import { useMutation, useMutationState, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowDownToLine, ArrowUp, ArrowUpToLine, Pause, Play, SquarePen, X } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { apiErrorText, type BatchItem, type Job, type QueueMove } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { announce, Button, cx, Dialog, ErrorBox, Input, Menu, menuItemClass, MenuSeparator } from './ui'

// 排队任务的调整：置顶、上移、下移、移到末尾、暂缓和恢复（FEATURES 3.2），以及批量取消和批量替换（FEATURES 3.1）

const opKey = ['job', 'queue-op']

type Op = { id: number; move?: QueueMove; held?: boolean }

/** 挪动和暂缓：成功后刷新列表，失败的原因由 QueueOpError 显示在页面上方（任务行里放不下） */
export function useQueueOp() {
  const qc = useQueryClient()
  return useMutation({
    mutationKey: opKey,
    mutationFn: (op: Op) => (op.move ? api.moveJob(op.id, op.move) : api.holdJob(op.id, op.held!)),
    onSuccess: (job) => {
      qc.setQueryData(['job', job.id], (old: Job | undefined) => (old ? { ...old, ...job } : job))
      void qc.invalidateQueries({ queryKey: ['jobs'] })
    },
  })
}

export function QueueOpError() {
  const results = useMutationState({
    filters: { mutationKey: opKey },
    select: (m) => ({ error: m.state.error, id: (m.state.variables as Op | undefined)?.id }),
  })
  const latest = results.at(-1)
  return latest?.error ? <ErrorBox error={t('jobs.queue_op_error', { id: String(latest.id), error: apiErrorText(latest.error) })} /> : null
}

/**
 * 排队任务行尾的 ⋯ 菜单：置顶、上移、下移、移到末尾，暂缓或恢复，修改，取消。
 * 占的是在跑任务的取消按钮那一格，行宽不变；手机上就是那一个 44px 的操作。
 */
export function QueueJobMenu({ job: j, onCancel, className, buttonClassName }: { job: Job; onCancel(): void; className?: string; buttonClassName?: string }) {
  const op = useQueueOp()
  const pos = j.queue_pos ?? 0
  const move = (to: QueueMove) => op.mutate({ id: j.id, move: to })
  const item = (to: QueueMove, Icon: typeof ArrowUp, label: string, disabled: boolean) => (
    <button type="button" role="menuitem" className={menuItemClass} disabled={op.isPending || disabled} onClick={() => move(to)}>
      <Icon aria-hidden />
      {label}
    </button>
  )
  return (
    <Menu label={t('jobs.queue_menu', { id: j.id })} variant="ghost" size="icon-sm" floating className={className}
      buttonClassName={cx('text-fg-3 max-lg:size-11', buttonClassName)}>
      {/* 第一位的不能再往上；最后一位读不到总数，交给服务器判断（已经在末尾时什么都不做） */}
      {item('top', ArrowUpToLine, t('jobs.move_top'), pos === 1)}
      {item('up', ArrowUp, t('jobs.move_up'), pos === 1)}
      {item('down', ArrowDown, t('jobs.move_down'), false)}
      {item('bottom', ArrowDownToLine, t('jobs.move_bottom'), false)}
      <MenuSeparator />
      <button type="button" role="menuitem" className={menuItemClass} disabled={op.isPending} onClick={() => op.mutate({ id: j.id, held: !j.held })}>
        {j.held ? <Play aria-hidden /> : <Pause aria-hidden />}
        {j.held ? t('jobs.release') : <>{t('jobs.hold')}<span className="text-xs text-fg-3">{t('jobs.hold_hint')}</span></>}
      </button>
      <Link role="menuitem" to={`/new?edit=${j.id}`} className={menuItemClass}>
        <SquarePen aria-hidden />
        {t('jobs.edit_queued')}
      </Link>
      <button type="button" role="menuitem" className={menuItemClass} onClick={onCancel}>
        <X aria-hidden />
        {t('jobs.cancel_job')}
      </button>
    </Menu>
  )
}

/** 任务列表：#1、#2、#3（最多列 12 个，其余写"等 N 个"） */
export function idList(ids: number[]) {
  const shown = ids.slice(0, 12).map((id) => `#${id}`).join(' ')
  return ids.length > 12 ? t('jobs.id_list_more', { list: shown, n: ids.length - 12 }) : shown
}

export function BatchCancelDialog({ ids, onDone, onClose }: { ids: number[]; onDone(): void; onClose(): void }) {
  const qc = useQueryClient()
  const cancel = useMutation({
    mutationFn: () => api.batchJobs({ ids, action: 'cancel' }),
    onSuccess: () => {
      void qc.invalidateQueries()
      announce(t('jobs.batch_cancel_done', { n: ids.length }))
      onDone()
    },
  })
  return (
    <Dialog
      title={t('jobs.batch_cancel_title', { n: ids.length })}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{t('jobs.keep_queued')}</Button>
          <Button variant="danger" loading={cancel.isPending} onClick={() => cancel.mutate()}>
            {t('jobs.batch_cancel')}
          </Button>
        </>
      }
    >
      <p className="font-mono text-xs break-words text-fg">{idList(ids)}</p>
      <p>{t('jobs.batch_cancel_body')}</p>
      <ErrorBox error={cancel.error} />
    </Dialog>
  )
}

/** 按行比较改前改后：只列出变了的行，改前的一行在上、改后的在下 */
function LineDiff({ before, after }: { before: string; after: string }) {
  const a = before.split('\n')
  const b = after.split('\n')
  const rows: { sign: '-' | '+'; text: string }[] = []
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if (a[i] === b[i]) continue
    if (a[i] !== undefined) rows.push({ sign: '-', text: a[i] })
    if (b[i] !== undefined) rows.push({ sign: '+', text: b[i] })
  }
  return (
    <div className="overflow-x-auto rounded-md border border-line bg-subtle px-2.5 py-1.5 font-mono text-xs leading-relaxed">
      {rows.map((r, i) => (
        // 颜色只表示任务状态：改前的行用删除线和浅一级的灰，改后的用主文字色
        <div key={i} className={cx('whitespace-pre-wrap [overflow-wrap:anywhere]', r.sign === '-' ? 'text-fg-3 line-through decoration-fg-4' : 'text-fg')}>
          <span aria-hidden className="mr-1.5 inline-block w-2 text-fg-3 no-underline">{r.sign === '-' ? '−' : '+'}</span>
          <span className="sr-only">{r.sign === '-' ? t('jobs.diff_before') : t('jobs.diff_after')}</span>
          {r.text}
        </div>
      ))}
    </div>
  )
}

/**
 * 批量替换命令里的文字：填"查找"和"替换为"，先逐个预览改前改后（按行），确认后一次改完。
 * 预览之后又改了输入框，要重新预览才能替换。整批要么全改成，要么一个不改；有任务已经开始时服务器返回 409，写明是哪个。
 */
export function ReplaceDialog({ ids, onDone, onClose }: { ids: number[]; onDone(): void; onClose(): void }) {
  const qc = useQueryClient()
  const uid = useId()
  const [find, setFind] = useState('')
  const [replace, setReplace] = useState('')
  const [preview, setPreview] = useState<{ find: string; replace: string; items: BatchItem[] } | null>(null)
  const dry = useMutation({
    mutationFn: () => api.batchJobs({ ids, action: 'replace', find, replace, dry_run: true }),
    onSuccess: (r) => setPreview({ find, replace, items: r.items }),
  })
  const apply = useMutation({
    mutationFn: () => api.batchJobs({ ids, action: 'replace', find: preview!.find, replace: preview!.replace }),
    onSuccess: (r) => {
      void qc.invalidateQueries()
      announce(t('jobs.replace_done', { n: r.items.filter((i) => i.changed).length }))
      onDone()
    },
  })
  const stale = preview !== null && (preview.find !== find || preview.replace !== replace)
  const changed = preview?.items.filter((i) => i.changed) ?? []
  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    if (!find) return
    if (preview && !stale && changed.length) apply.mutate()
    else dry.mutate()
  }
  const ready = preview && !stale && changed.length > 0
  return (
    <Dialog
      wide
      className="flex max-h-[calc(100dvh-2rem)] flex-col"
      title={t('jobs.replace_title', { n: ids.length })}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{t('jobs.dialog_close')}</Button>
          {ready ? (
            <Button variant="primary" type="submit" form={`${uid}-form`} loading={apply.isPending}>
              {t('jobs.replace_apply', { n: changed.length })}
            </Button>
          ) : (
            <Button variant="primary" type="submit" form={`${uid}-form`} loading={dry.isPending} disabled={!find}>
              {t('jobs.replace_preview')}
            </Button>
          )}
        </>
      }
    >
      <form id={`${uid}-form`} onSubmit={onSubmit} className="space-y-3">
        <p>{t('jobs.replace_hint')}</p>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="space-y-1">
            <span className="text-xs font-medium text-fg-2">{t('jobs.replace_find')}</span>
            <Input autoFocus value={find} onChange={(e) => setFind(e.target.value)} spellCheck={false} autoCapitalize="off" autoCorrect="off"
              className="font-mono text-[12.5px]" placeholder="--lr 1e-3" />
          </label>
          <label className="space-y-1">
            <span className="text-xs font-medium text-fg-2">{t('jobs.replace_with')}</span>
            <Input value={replace} onChange={(e) => setReplace(e.target.value)} spellCheck={false} autoCapitalize="off" autoCorrect="off"
              className="font-mono text-[12.5px]" placeholder="--lr 3e-4" />
          </label>
        </div>
      </form>
      {stale && <p className="text-xs text-warn">{t('jobs.replace_stale')}</p>}
      {preview && !stale && (
        <div className="space-y-2.5" aria-live="polite">
          {changed.length === 0 && <p className="text-fg">{t('jobs.replace_none')}</p>}
          {preview.items.map((item) => (
            <div key={item.id} className="space-y-1">
              <p className="flex min-w-0 items-baseline gap-2 text-xs">
                <span className="shrink-0 text-fg-3">#{item.id}</span>
                <span className="truncate font-medium text-fg">{item.name}</span>
                {!item.changed && <span className="ml-auto shrink-0 text-fg-3">{t('jobs.replace_unchanged')}</span>}
              </p>
              {item.changed && <LineDiff before={item.before ?? ''} after={item.after ?? ''} />}
            </div>
          ))}
        </div>
      )}
      <ErrorBox error={dry.error ?? apply.error} />
    </Dialog>
  )
}
