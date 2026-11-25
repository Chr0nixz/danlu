import { useQueryClient } from '@tanstack/react-query'
import { ShieldCheck } from 'lucide-react'
import type { ReactNode } from 'react'
import type { SettingItem, Settings } from '@danlu/core'
import { Button, Card, cx, FieldError, FormRow, hitArea, Input, Select } from '../../components/ui'
import { t } from '../../lib/i18n'

/** t 的键位类型：静态映射表里存键，不存文案，避免模块加载时冻结语言 */
type TKey = Parameters<typeof t>[0]

export function Section({ title, desc, children, footer }: { title: string; desc?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  return (
    <Card>
      <div className="px-4 pt-4 sm:px-5">
        <h2 className="text-sm font-semibold">{title}</h2>
        {desc && <p className="mt-1 text-ui leading-relaxed text-fg-3">{desc}</p>}
      </div>
      <div className="px-4 py-4 sm:px-5">{children}</div>
      {footer && <div className="flex items-center gap-3 rounded-b-lg border-t border-line bg-subtle px-4 py-3 sm:px-5">{footer}</div>}
    </Card>
  )
}

export function KV({ k, v }: { k: string; v: ReactNode }) {
  return (
    <div className="grid grid-cols-[96px_1fr] gap-3 py-2 text-ui sm:grid-cols-[128px_1fr]">
      <dt className="text-fg-3">{k}</dt>
      <dd className="min-w-0 break-all text-fg">{v}</dd>
    </div>
  )
}

/** 表单行之间的分隔：行自带左右内边距，所以容器抵消 Section 的内边距 */
export function Rows({ children }: { children: ReactNode }) {
  return <div className="-mx-4 -my-4 divide-y divide-line sm:-mx-5">{children}</div>
}

/** 保存按钮所在的底栏：左边是状态，右边是按钮 */
export function SaveFooter({ saved, dirty, pending, savedText = t('settings.saved_now'), children }: { saved: boolean; dirty: boolean; pending: boolean; savedText?: string; children?: ReactNode }) {
  return (
    <>
      <span role="status" className="flex min-w-0 flex-1 items-center gap-1.5 text-ui text-ok">
        {saved && (
          <>
            <ShieldCheck className="size-4 shrink-0" />
            {savedText}
          </>
        )}
      </span>
      {children}
      <Button type="submit" size="sm" variant="primary" loading={pending} disabled={!dirty}>
        {t('common.save')}
      </Button>
    </>
  )
}

/** 保存后把服务器返回的那一部分写进设置缓存，别的页面（新建任务、资源页）马上用上 */
export function useSettingsCache() {
  const qc = useQueryClient()
  return {
    merge: (part: Partial<Settings>) => qc.setQueryData<Settings>(['settings'], (old) => (old ? { ...old, ...part } : old)),
    refresh: () => {
      qc.invalidateQueries({ queryKey: ['settings'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
    },
  }
}

// ---------- 运行设置的一项：数值、单位、0 表示关闭、恢复成配置文件里的值 ----------

export type DurUnit = 's' | 'm' | 'h'
export const durUnits: { value: DurUnit; seconds: number }[] = [
  { value: 's', seconds: 1 },
  { value: 'm', seconds: 60 },
  { value: 'h', seconds: 3600 },
]
/** 单位词按当前语言取值，不在模块级常量里冻结 */
export function durUnitLabel(u: DurUnit): string {
  switch (u) {
    case 's':
      return t('settings.unit_s')
    case 'm':
      return t('settings.unit_m')
    case 'h':
      return t('settings.unit_h')
  }
}
const unitSeconds = (u: DurUnit) => durUnits.find((x) => x.value === u)!.seconds

/** 能整除的最大单位，1800 秒显示成 30 分钟 */
export function pickUnit(sec: number): DurUnit {
  if (sec > 0 && sec % 3600 === 0) return 'h'
  if (sec > 0 && sec % 60 === 0) return 'm'
  return 's'
}

/** 和服务器的操作记录一样：7 天、12 小时、90 分钟、30 秒 */
export function fmtSeconds(sec: number) {
  if (sec >= 86400 && sec % 86400 === 0) return t('settings.duration_days', { n: sec / 86400 })
  if (sec >= 3600 && sec % 3600 === 0) return t('settings.duration_hours', { n: sec / 3600 })
  if (sec >= 60 && sec % 60 === 0) return t('settings.duration_minutes', { n: sec / 60 })
  return t('settings.duration_seconds', { n: sec })
}

export function fmtItem(item: SettingItem, v: number) {
  if (item.zero_off && v === 0) return t('settings.value_off')
  switch (item.unit) {
    case 's':
      return fmtSeconds(v)
    case 'day':
      return t('settings.duration_days', { n: v })
    default:
      return `${v} ${item.unit}`
  }
}

/** 表单里的一项。reset 表示保存时恢复成配置文件里的值（提交 null） */
export interface ItemField {
  on: boolean
  amount: string
  unit: DurUnit
  reset: boolean
}

export function fieldFrom(item: SettingItem, value = item.value): ItemField {
  // 关闭时输入框里放默认值，重新打开时有个合理的起点
  const v = value || item.default || item.min
  const unit = item.unit === 's' ? pickUnit(v) : 's'
  return { on: !(item.zero_off && value === 0), amount: String(item.unit === 's' ? v / unitSeconds(unit) : v), unit, reset: false }
}

/** 要提交的值：null 表示恢复成配置文件里的值 */
export function fieldValue(item: SettingItem, f: ItemField): number | null {
  if (f.reset) return null
  if (item.zero_off && !f.on) return 0
  return Number(f.amount) * (item.unit === 's' ? unitSeconds(f.unit) : 1)
}

export function validateField(item: SettingItem, f: ItemField): string | undefined {
  if (f.reset || (item.zero_off && !f.on)) return undefined
  if (!/^\d+$/.test(f.amount.trim())) return t('settings.err_integer')
  const v = fieldValue(item, f)!
  if (v < item.min || v > item.max) return t(item.zero_off ? 'settings.err_range_or_off' : 'settings.err_range', { min: fmtItem(item, item.min), max: fmtItem(item, item.max) })
  return undefined
}

/** 一项运行设置：开关（0 表示关闭的项才有）、数值和单位，以及"面板上改过 · 恢复" */
export function SettingRow({
  label,
  hint,
  item,
  value,
  onChange,
  id,
  error,
  children,
}: {
  label: string
  hint: ReactNode
  item: SettingItem
  value: ItemField
  onChange: (f: ItemField) => void
  id: string
  error?: string
  /** 行下方的补充说明，例如当前磁盘剩余 */
  children?: ReactNode
}) {
  const errId = `${id}-err`
  const off = item.zero_off && !value.on
  const set = (patch: Partial<ItemField>) => onChange({ ...value, ...patch, reset: false })
  return (
    <FormRow label={label} hint={hint} describedBy={error ? errId : undefined}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-ui text-fg-2">
        {item.zero_off && (
          // 和旁边的输入框一样高（手机 44px）：点"开启"的整块都能切换，不用对准 16px 的勾选框
          <label className="flex min-h-9 items-center gap-2 text-fg max-sm:min-h-11">
            <input type="checkbox" className="size-4 accent-inv" checked={value.on} onChange={(e) => set({ on: e.target.checked })} />
            {t('settings.row_on')}
          </label>
        )}
        <span className="flex items-center gap-2">
          <Input
            id={id}
            aria-label={t('settings.row_amount_aria', { label })}
            aria-invalid={!!error || undefined}
            aria-describedby={error ? errId : undefined}
            className="w-24!"
            inputMode="numeric"
            disabled={off}
            value={value.amount}
            onChange={(e) => set({ amount: e.target.value.trim() })}
          />
          {item.unit === 's' ? (
            <Select aria-label={t('settings.row_unit_aria', { label })} className="w-24!" disabled={off} value={value.unit} onChange={(e) => set({ unit: e.target.value as DurUnit })}>
              {durUnits.map((u) => (
                <option key={u.value} value={u.value}>
                  {durUnitLabel(u.value)}
                </option>
              ))}
            </Select>
          ) : (
            <span className={cx(off && 'opacity-50')}>{item.unit === 'day' ? t('settings.unit_d') : item.unit}</span>
          )}
        </span>
      </div>
      {value.reset ? (
        <p className="mt-1.5 text-xs leading-relaxed text-fg-3">{t('settings.row_reset_hint', { value: fmtItem(item, item.default) })}</p>
      ) : (
        item.custom && (
          <p className="mt-1.5 text-xs leading-relaxed text-fg-3">
            {t('settings.row_custom', { value: fmtItem(item, item.default) })} ·{' '}
            <button
              type="button"
              className={cx(hitArea, 'text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg hover:decoration-fg')}
              onClick={() => onChange({ ...fieldFrom(item, item.default), reset: true })}
            >
              {t('settings.restore')}
            </button>
          </p>
        )
      )}
      {children}
      <FieldError id={errId}>{error}</FieldError>
    </FormRow>
  )
}

// 动作码 → 目录键的静态映射；文案在调用时按当前语言取
const actionKeys: Record<string, TKey> = {
  login_ok: 'settings.action.login_ok',
  login_fail: 'settings.action.login_fail',
  login_locked: 'settings.action.login_locked',
  logout: 'settings.action.logout',
  submit: 'settings.action.submit',
  cancel: 'settings.action.cancel',
  password_change: 'settings.action.password_change',
  security_change: 'settings.action.security_change',
  settings_change: 'settings.action.settings_change',
  retention_change: 'settings.action.retention_change',
  prefs_change: 'settings.action.prefs_change',
  session_revoke: 'settings.action.session_revoke',
  totp_enable: 'settings.action.totp_enable',
  totp_disable: 'settings.action.totp_disable',
  token_create: 'settings.action.token_create',
  token_revoke: 'settings.action.token_revoke',
  notify_test: 'settings.action.notify_test',
  job_memo: 'settings.action.job_memo',
  job_notify: 'settings.action.job_notify',
  job_star: 'settings.action.job_star',
  job_rename: 'settings.action.job_rename',
  job_edit: 'settings.action.job_edit',
  job_move: 'settings.action.job_move',
  job_hold: 'settings.action.job_hold',
  job_batch: 'settings.action.job_batch',
  queue_pause: 'settings.action.queue_pause',
  queue_resume: 'settings.action.queue_resume',
  terminal_open: 'settings.action.terminal_open',
  terminal_close: 'settings.action.terminal_close',
  reservation_create: 'settings.action.reservation_create',
  reservation_release: 'settings.action.reservation_release',
  reservation_extend: 'settings.action.reservation_extend',
  strict_enable: 'settings.action.strict_enable',
  strict_disable: 'settings.action.strict_disable',
}

/** 操作记录的动作名：按 action 码翻译，未知码照原样显示（和服务器一致） */
export function actionLabel(action: string): string {
  const key = actionKeys[action]
  return key ? t(key) : action
}
