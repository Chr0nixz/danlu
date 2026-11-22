import { Check } from 'lucide-react'
import type { GPU } from '@danlu/core'
import { strayLabel } from './GpuGrid'
import { t } from '../lib/i18n'
import { cx } from './ui'

/** 指定卡号：每张卡一个可按下的按钮，写出这张卡现在被谁占着。新建任务和预约 GPU 共用 */
export function GpuPicker({ id, describedBy, gpus, value, onChange }: { id: string; describedBy?: string; gpus: GPU[]; value: number[]; onChange: (v: number[]) => void }) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {gpus.map((g, n) => {
        const on = value.includes(g.index)
        const state = g.job
          ? t('gpus.picker_job', { id: g.job.id })
          : g.reservation
            ? t('gpus.reservation_label', { id: g.reservation.id })
            : g.external.length ? strayLabel(g.external[0]) : (g.unknown_mem_mb ?? 0) > 0 ? t('gpus.unknown_mem') : t('gpus.idle')
        const tone = g.job ? 'text-run' : g.reservation ? 'text-fg-2' : g.external.length || (g.unknown_mem_mb ?? 0) > 0 ? 'text-warn' : 'text-ok'
        return (
          <button
            type="button"
            key={g.index}
            // 第一张卡带上字段 id，校验失败时焦点落在这里，并读出错误说明
            id={n === 0 ? id : undefined}
            aria-describedby={n === 0 ? describedBy : undefined}
            aria-pressed={on}
            onClick={() => onChange(on ? value.filter((x) => x !== g.index) : [...value, g.index].sort((a, b) => a - b))}
            className={cx(
              'relative rounded-md border px-3 py-2 text-left transition-colors duration-150',
              on ? 'border-fg bg-subtle ring-1 ring-fg' : 'border-line-strong hover:bg-subtle',
            )}
          >
            <div className="flex items-center justify-between">
              <span className="text-ui font-semibold">GPU {g.index}</span>
              {on && <Check className="size-3.5" strokeWidth={3} aria-hidden />}
            </div>
            <div className={cx('mt-0.5 truncate text-xs', tone)}>
              {state}
              <span className="text-fg-3"> · {g.util}%</span>
            </div>
          </button>
        )
      })}
    </div>
  )
}
