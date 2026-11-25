import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Lock, Minus, Plus, TimerReset } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { duration, useNow, type Reservation, type ReservationReq } from '@danlu/core'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { useReservations } from '../lib/queries'
import { useCopy } from '../lib/useCopy'
import { clock, gpuBusy, useGpus } from './GpuGrid'
import { GpuPicker } from './GpuPicker'
import { Button, Card, cx, Dialog, ErrorBox, FieldError, GpuChips, Input, Segmented } from './ui'

/** 外部任务用这几张卡要设的环境变量。CUDA 默认按算力排序，和 nvidia-smi 的编号不一定一致，所以带上 PCI_BUS_ID */
export const cudaEnv = (cards: number[]) => `CUDA_DEVICE_ORDER=PCI_BUS_ID CUDA_VISIBLE_DEVICES=${cards.join(',')}`

const want = (r: Reservation) => (r.pinned_gpus.length ? r.pinned_gpus.length : r.gpus)

function stateText(r: Reservation, now: number) {
  if (r.status === 'waiting') {
    const missing = r.pinned_gpus.filter((g) => !r.held_gpus.includes(g))
    const what = missing.length ? t('reservations.waiting_cards', { gpus: missing.join(', ') }) : t('reservations.claiming_progress', { held: r.held_gpus.length, want: want(r) })
    return t('reservations.waiting_state', { what, duration: duration(now - r.created_at) })
  }
  if (!r.ends_at) return t('reservations.ready_manual')
  return t('reservations.ready_until', { time: clock(r.ends_at), remaining: duration(r.ends_at - now) })
}

function useInvalidate() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: ['reservations'] })
    void qc.invalidateQueries({ queryKey: ['gpus'] })
    // 排队任务的等待原因跟着变
    void qc.invalidateQueries({ queryKey: ['jobs'] })
  }
}

function CopyEnv({ cards }: { cards: number[] }) {
  const text = cudaEnv(cards)
  const c = useCopy(text)
  return (
    <Button
      variant="ghost"
      size="sm"
      className={cx('text-xs', c.state === 'fail' ? 'text-fail' : 'text-fg-3')}
      onClick={c.copy}
      title={c.title ?? t('reservations.copy_env_title', { text })}
    >
      {c.icon}
      {t('reservations.copy_env')}
      {c.status}
    </Button>
  )
}

function ReservationRow({ r }: { r: Reservation }) {
  const invalidate = useInvalidate()
  const [confirming, setConfirming] = useState(false)
  const extend = useMutation({ mutationFn: () => api.extendReservation(r.id), onSuccess: invalidate })
  const release = useMutation({
    mutationFn: () => api.releaseReservation(r.id),
    onSuccess: () => {
      setConfirming(false)
      invalidate()
    },
  })
  const ready = r.status === 'active'
  // 等了多久、还剩多久每秒走一格
  const state = stateText(r, useNow())
  return (
    <div className="px-3 py-2.5 sm:px-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <div className="min-w-0 flex-1 basis-64">
          <div className="flex min-w-0 items-center gap-2 text-ui">
            <Lock className="size-3.5 shrink-0 text-fg-3" aria-hidden />
            <span className="shrink-0 font-medium">{t('reservations.label', { id: r.id })}</span>
            {r.held_gpus.length > 0 && <GpuChips ids={r.held_gpus} className="shrink-0" />}
            <span className={cx('min-w-0 truncate', ready ? 'text-fg-2' : 'text-fg-3')} title={state}>{state}</span>
          </div>
          {r.note && <div className="mt-0.5 truncate pl-[22px] text-xs text-fg-3">{r.note}</div>}
        </div>
        <div className="-my-1 flex shrink-0 items-center gap-1 max-sm:-my-1 max-sm:pl-3">
          {ready && <CopyEnv cards={r.held_gpus} />}
          {ready && r.ends_at && (
            <Button variant="ghost" size="sm" className="text-xs text-fg-3" loading={extend.isPending} onClick={() => extend.mutate()}>
              <TimerReset aria-hidden />
              {t('reservations.extend')}
            </Button>
          )}
          <Button variant="ghost" size="sm" className="text-xs text-fg-3" onClick={() => setConfirming(true)}>
            {t('reservations.release')}
          </Button>
        </div>
      </div>
      <ErrorBox error={extend.error} className="mt-2" />
      {confirming && (
        <Dialog
          title={t('reservations.release_title', { id: r.id })}
          onClose={() => setConfirming(false)}
          footer={
            <>
              <Button onClick={() => setConfirming(false)}>{t('reservations.release_cancel')}</Button>
              <Button variant="danger" loading={release.isPending} onClick={() => release.mutate()}>
                {t('reservations.release')}
              </Button>
            </>
          }
        >
          <p>
            {r.held_gpus.length ? t('reservations.release_body_cards', { gpus: r.held_gpus.join(', ') }) : t('reservations.release_body_empty')}
            {t('reservations.release_body_note')}
          </p>
          <ErrorBox error={release.error} />
        </Dialog>
      )}
    </div>
  )
}

/** 首页的预约列表：只在有预约时出现，排在 GPU 和任务之间 */
export function ReservationList() {
  const q = useReservations()
  const list = q.data?.reservations ?? []
  if (!list.length) return <ErrorBox error={q.error} />
  return (
    <section aria-labelledby="reservation-heading">
      <Card className="overflow-hidden">
        <div className="flex h-9 items-center gap-2 border-b border-line bg-subtle px-3 text-ui sm:px-4">
          <Lock className="size-4 text-fg-3" aria-hidden />
          <h2 id="reservation-heading" className="font-medium">{t('reservations.heading')}</h2>
          <span className="text-fg-3">{list.length}</span>
          <span className="ml-auto truncate text-xs text-fg-3 max-sm:hidden">{t('reservations.heading_hint')}</span>
        </div>
        <div className="divide-y divide-line">
          {list.map((r) => (
            <ReservationRow key={r.id} r={r} />
          ))}
        </div>
      </Card>
    </section>
  )
}

type Length = '1' | '3' | '8' | 'custom' | 'none'

/** 首页摘要行里的"预约 GPU"：打开预约对话框 */
export function ReserveButton({ className }: { className?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button variant="ghost" size="sm" className={cx('text-xs text-fg-3', className)} onClick={() => setOpen(true)}>
        <Lock aria-hidden />
        {t('reservations.reserve_gpu')}
      </Button>
      {open && <ReserveDialog onClose={() => setOpen(false)} />}
    </>
  )
}

function ReserveDialog({ onClose }: { onClose: () => void }) {
  const ids = { gpu: useId(), hours: useId(), note: useId() }
  const gpus = useGpus()
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, staleTime: 60_000 })
  const invalidate = useInvalidate()
  const list = gpus.data?.gpus ?? []
  const total = list.length
  const free = list.filter((g) => !gpuBusy(g)).length
  const [mode, setMode] = useState<'count' | 'pinned'>('count')
  const [count, setCount] = useState(1)
  const [pinned, setPinned] = useState<number[]>([])
  const [length, setLength] = useState<Length>('3')
  const [hours, setHours] = useState('2')
  const [note, setNote] = useState('')
  const [error, setError] = useState<{ gpu?: string; hours?: string }>({})
  const create = useMutation({
    mutationFn: (r: ReservationReq) => api.createReservation(r),
    onSuccess: () => {
      invalidate()
      onClose()
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: typeof error = {}
    if (mode === 'pinned' && !pinned.length) errs.gpu = t('gpus.pick_required')
    let seconds: number | null = null
    if (length === 'custom') {
      const h = Number(hours)
      if (!Number.isFinite(h) || h * 3600 < 600 || h > 168) errs.hours = t('reservations.hours_range')
      else seconds = Math.round(h * 3600)
    } else if (length !== 'none') {
      seconds = Number(length) * 3600
    }
    setError(errs)
    if (errs.gpu || errs.hours) {
      document.getElementById(errs.gpu ? ids.gpu : ids.hours)?.focus()
      return
    }
    create.mutate({ gpus: mode === 'count' ? count : pinned.length, pinned_gpus: mode === 'pinned' ? pinned : [], duration: seconds, note: note.trim() })
  }

  const need = mode === 'count' ? count : pinned.length
  const short = mode === 'count' ? need > free : pinned.some((i) => {
    const g = list.find((x) => x.index === i)
    return !!g && (!!g.job || !!g.reservation)
  })
  const pushOn = settings.data?.notify?.enabled
  const formId = useId()

  return (
    <Dialog
      title={t('reservations.reserve_gpu')}
      wide
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="submit" form={formId} variant="primary" loading={create.isPending}>
            {t('reservations.submit')}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={submit} className="space-y-4 pt-1">
        <p>
          {t('reservations.intro')}
          {pushOn && t('reservations.intro_push')}
        </p>

        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="text-ui font-medium text-fg">GPU</span>
            <Segmented
              label={t('reservations.mode_label')}
              value={mode}
              onChange={(v) => {
                setMode(v)
                setError({})
              }}
              options={[
                { value: 'count', label: t('gpus.mode_count') },
                { value: 'pinned', label: t('gpus.mode_pinned') },
              ]}
            />
            <span className="text-xs text-fg-3">{t('reservations.cards_summary', { total, free, n: total })}</span>
          </div>
          {mode === 'count' ? (
            <div className="flex items-center gap-3">
              <div role="group" aria-label={t('reservations.count_label')} className="inline-flex h-9 items-stretch overflow-hidden rounded-md border border-field-line max-sm:h-[46px]">
                <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40 max-sm:w-11" onClick={() => setCount((c) => Math.max(1, c - 1))} disabled={count <= 1} aria-label={t('gpus.count_less')}>
                  <Minus className="size-4" aria-hidden />
                </button>
                <output aria-live="polite" className="grid w-10 place-items-center border-x border-field-line text-sm font-semibold text-fg">
                  {count}
                </output>
                <button type="button" className="grid w-9 place-items-center text-fg-2 hover:bg-hover disabled:opacity-40 max-sm:w-11" onClick={() => setCount((c) => Math.min(total || 64, c + 1))} disabled={total > 0 && count >= total} aria-label={t('gpus.count_more')}>
                  <Plus className="size-4" aria-hidden />
                </button>
              </div>
              <span className="text-ui">{t('reservations.count_suffix', { n: count })}</span>
            </div>
          ) : (
            <GpuPicker id={ids.gpu} describedBy={error.gpu && `${ids.gpu}-err`} gpus={list} value={pinned} onChange={(v) => {
              setPinned(v)
              setError({})
            }} />
          )}
          <FieldError id={`${ids.gpu}-err`}>{error.gpu}</FieldError>
          {short && need > 0 && <p className="text-xs text-warn">{t('reservations.short_warning')}</p>}
        </div>

        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="text-ui font-medium text-fg">{t('reservations.auto_release')}</span>
            <Segmented
              label={t('reservations.length_label')}
              value={length}
              onChange={(v) => {
                setLength(v)
                setError({})
              }}
              options={[
                { value: '1', label: t('reservations.length_1h') },
                { value: '3', label: t('reservations.length_3h') },
                { value: '8', label: t('reservations.length_8h') },
                { value: 'custom', label: t('reservations.length_custom') },
                { value: 'none', label: t('reservations.length_none') },
              ]}
            />
          </div>
          {length === 'custom' && (
            <div className="flex items-center gap-2">
              {/* Input 自带 w-full，宽度由外层定 */}
              <div className="w-24 shrink-0">
                <Input id={ids.hours} type="number" inputMode="decimal" min={0.5} max={168} step={0.5}
                  aria-invalid={!!error.hours || undefined} aria-describedby={error.hours && `${ids.hours}-err`}
                  value={hours} onChange={(e) => setHours(e.target.value)} />
              </div>
              <span className="text-ui">{t('reservations.custom_hours_suffix')}</span>
            </div>
          )}
          <FieldError id={`${ids.hours}-err`}>{error.hours}</FieldError>
          {length === 'none' && <p className="text-xs text-fg-3">{t('reservations.none_hint')}</p>}
        </div>

        <div className="space-y-1.5">
          <label htmlFor={ids.note} className="text-ui font-medium text-fg">
            {t('reservations.note_label')}
          </label>
          <Input id={ids.note} value={note} onChange={(e) => setNote(e.target.value)} maxLength={100} placeholder={t('reservations.note_placeholder')} />
        </div>
        <ErrorBox error={create.error} />
      </form>
    </Dialog>
  )
}
