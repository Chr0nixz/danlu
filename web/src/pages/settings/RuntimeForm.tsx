import type { FormEvent, ReactNode } from 'react'
import type { SettingItem, Settings, SettingsPatch } from '@danlu/core'
import { announce, ErrorBox } from '../../components/ui'
import { t } from '../../lib/i18n'
import { fieldFrom, fieldValue, Rows, SaveFooter, Section, SettingRow, useSettingsCache, validateField, type ItemField } from './shared'
import { useServerForm, type FormErrors } from './useServerForm'

export interface RowMeta {
  label: string
  hint: ReactNode
}

/** 保存后各项实际会是多少：恢复的项取配置文件里的值，关闭的项是 0 */
export function effectiveValues<K extends string>(items: Record<K, SettingItem>, form: Record<K, ItemField>, keys: K[]) {
  return Object.fromEntries(keys.map((k) => [k, fieldValue(items[k], form[k]) ?? items[k].default])) as Record<K, number>
}

/**
 * 一组运行设置（调度与告警、数据保留）的表单：每项一行，只提交改了的项，保存后立即生效。
 * intercept 在提交前接管（数据保留要先预览会清理什么），确认后调用 save。
 */
export function RuntimeForm<K extends string>({
  title,
  desc,
  items,
  keys,
  meta,
  cacheKey,
  update,
  extra,
  crossCheck,
  intercept,
  savedText,
  busy = false,
  children,
}: {
  title: string
  desc?: ReactNode
  items: Record<K, SettingItem>
  keys: K[]
  meta: Record<K, RowMeta>
  /** 保存结果写进设置缓存的哪个字段 */
  cacheKey: 'runtime' | 'retention'
  update: (patch: SettingsPatch<K>) => Promise<Record<K, SettingItem>>
  /** 某一行下方的补充说明，拿到这一行正在编辑的内容 */
  extra?: Partial<Record<K, (f: ItemField, values: Record<K, number>) => ReactNode>>
  /** 两项之间的约束，只在其中一项改了时检查 */
  crossCheck?: (values: Record<K, number>, changed: Record<K, boolean>) => FormErrors<Record<K, ItemField>>
  intercept?: (patch: SettingsPatch<K>, save: () => void) => void
  savedText?: string
  /** intercept 正在做事（例如预览、等确认）：保存按钮显示忙，输入框锁住，免得改了没预览过的值 */
  busy?: boolean
  /** 各行下方的补充内容，例如预览出错 */
  children?: ReactNode
}) {
  const cache = useSettingsCache()
  const toForm = (its: Record<K, SettingItem>) => Object.fromEntries(keys.map((k) => [k, fieldFrom(its[k])])) as Record<K, ItemField>
  const f = useServerForm({
    server: toForm(items),
    save: (form, changed) => {
      const patch: SettingsPatch<K> = {}
      for (const k of keys) if (changed[k]) patch[k] = fieldValue(items[k], form[k])
      return update(patch)
    },
    fromResult: toForm,
    onSaved: (r) => {
      cache.merge({ [cacheKey]: r } as Partial<Settings>)
      announce(t('settings.section_saved', { title }))
    },
    onSettled: cache.refresh,
  })
  const values = effectiveValues(items, f.form, keys)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const found: FormErrors<Record<K, ItemField>> = {}
    for (const k of keys) {
      if (!f.changed[k]) continue // config.toml 里超出面板范围的旧值照样生效，不拦着改别的项
      const err = validateField(items[k], f.form[k])
      if (err) found[k] = err
    }
    // 字段自己的错误优先，没有时才显示两项之间的约束
    const cross: FormErrors<Record<K, ItemField>> = crossCheck?.(values, f.changed) ?? {}
    for (const k of keys) if (!found[k] && cross[k]) found[k] = cross[k]
    if (!f.check(found, keys) || !f.dirty) return
    if (intercept) {
      // 确认后保存的是交给 intercept 的这一份，不是确认那一刻的表单
      const snapshot = f.form
      const patch: SettingsPatch<K> = {}
      for (const k of keys) if (f.changed[k]) patch[k] = fieldValue(items[k], snapshot[k])
      intercept(patch, () => f.m.mutate(snapshot))
    } else f.m.mutate()
  }

  return (
    <form onSubmit={submit} noValidate>
      <Section title={title} desc={desc} footer={<SaveFooter saved={f.saved} dirty={f.dirty} pending={f.m.isPending || busy} savedText={savedText} />}>
        {/* fieldset 的 disabled 一次锁住里面所有输入框 */}
        <fieldset disabled={busy} className="m-0 min-w-0 border-0 p-0">
          <Rows>
            {keys.map((k) => (
              <SettingRow key={k} label={meta[k].label} hint={meta[k].hint} item={items[k]} value={f.form[k]} id={f.id(k)} error={f.errors[k]} onChange={(v) => f.edit(k, v)}>
                {extra?.[k]?.(f.form[k], values)}
              </SettingRow>
            ))}
            {children}
            {f.m.error && (
              <div className="px-4 py-3 sm:px-5">
                <ErrorBox error={f.m.error} />
              </div>
            )}
          </Rows>
        </fieldset>
      </Section>
    </form>
  )
}
