import { useQueryClient } from '@tanstack/react-query'
import { ShieldCheck } from 'lucide-react'
import type { ReactNode } from 'react'
import type { SettingItem, Settings } from '@danlu/core'
import { Button, Card, cx, FieldError, FormRow, hitArea, Input, Select } from '../../components/ui'

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
export function SaveFooter({ saved, dirty, pending, savedText = '已保存，立即生效', children }: { saved: boolean; dirty: boolean; pending: boolean; savedText?: string; children?: ReactNode }) {
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
        保存
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
export const durUnits: { value: DurUnit; label: string; seconds: number }[] = [
  { value: 's', label: '秒', seconds: 1 },
  { value: 'm', label: '分钟', seconds: 60 },
  { value: 'h', label: '小时', seconds: 3600 },
]
const unitSeconds = (u: DurUnit) => durUnits.find((x) => x.value === u)!.seconds

/** 能整除的最大单位，1800 秒显示成 30 分钟 */
export function pickUnit(sec: number): DurUnit {
  if (sec > 0 && sec % 3600 === 0) return 'h'
  if (sec > 0 && sec % 60 === 0) return 'm'
  return 's'
}

/** 和服务器的操作记录一样：7 天、12 小时、90 分钟、30 秒 */
export function fmtSeconds(sec: number) {
  if (sec >= 86400 && sec % 86400 === 0) return `${sec / 86400} 天`
  if (sec >= 3600 && sec % 3600 === 0) return `${sec / 3600} 小时`
  if (sec >= 60 && sec % 60 === 0) return `${sec / 60} 分钟`
  return `${sec} 秒`
}

export function fmtItem(item: SettingItem, v: number) {
  if (item.zero_off && v === 0) return '关闭'
  switch (item.unit) {
    case 's':
      return fmtSeconds(v)
    case 'day':
      return `${v} 天`
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
  if (!/^\d+$/.test(f.amount.trim())) return '需要是整数'
  const v = fieldValue(item, f)!
  if (v < item.min || v > item.max) return `需要在 ${fmtItem(item, item.min)} 到 ${fmtItem(item, item.max)} 之间${item.zero_off ? '，或关闭' : ''}`
  return undefined
}

const unitText: Record<SettingItem['unit'], string> = { s: '', GB: 'GB', MB: 'MB', day: '天' }

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
          <label className="flex items-center gap-2 text-fg">
            <input type="checkbox" className="size-4 accent-inv" checked={value.on} onChange={(e) => set({ on: e.target.checked })} />
            开启
          </label>
        )}
        <span className="flex items-center gap-2">
          <Input
            id={id}
            aria-label={`${label}数值`}
            aria-invalid={!!error || undefined}
            aria-describedby={error ? errId : undefined}
            className="w-24!"
            inputMode="numeric"
            disabled={off}
            value={value.amount}
            onChange={(e) => set({ amount: e.target.value.trim() })}
          />
          {item.unit === 's' ? (
            <Select aria-label={`${label}单位`} className="w-24!" disabled={off} value={value.unit} onChange={(e) => set({ unit: e.target.value as DurUnit })}>
              {durUnits.map((u) => (
                <option key={u.value} value={u.value}>
                  {u.label}
                </option>
              ))}
            </Select>
          ) : (
            <span className={cx(off && 'opacity-50')}>{unitText[item.unit]}</span>
          )}
        </span>
      </div>
      {value.reset ? (
        <p className="mt-1.5 text-xs leading-relaxed text-fg-3">保存后恢复为配置文件里的 {fmtItem(item, item.default)}</p>
      ) : (
        item.custom && (
          <p className="mt-1.5 text-xs leading-relaxed text-fg-3">
            面板上改过，config.toml 里的值（{fmtItem(item, item.default)}）不生效 ·{' '}
            <button
              type="button"
              className={cx(hitArea, 'text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg hover:decoration-fg')}
              onClick={() => onChange({ ...fieldFrom(item, item.default), reset: true })}
            >
              恢复
            </button>
          </p>
        )
      )}
      {children}
      <FieldError id={errId}>{error}</FieldError>
    </FormRow>
  )
}

export const actionLabel: Record<string, string> = {
  login_ok: '登录成功',
  login_fail: '登录失败',
  login_locked: '登录被锁定',
  logout: '退出登录',
  submit: '提交任务',
  cancel: '取消任务',
  password_change: '修改密码',
  security_change: '修改安全设置',
  settings_change: '修改调度与告警',
  retention_change: '修改保留策略',
  prefs_change: '修改面板偏好',
  session_revoke: '注销登录会话',
  totp_enable: '开启两步验证',
  totp_disable: '关闭两步验证',
  token_create: '创建设备令牌',
  token_revoke: '吊销设备令牌',
  notify_test: '测试手机推送',
  job_memo: '修改任务备注',
  job_notify: '修改结束提醒',
}
