import { Monitor, Moon, RotateCcw, Sun } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import type { Prefs, PrefsPatch, Settings } from '@danlu/core'
import { ShortcutList } from '../../components/Shortcuts'
import { announce, Button, ErrorBox, FieldError, FormRow, Input, Segmented, Select } from '../../components/ui'
import { api } from '../../lib/api'
import { LOG_FONT_DEFAULT, LOG_FONT_SIZES, setLogFontSize, storedLogFontSize, storedTerminalFontSize, saveTerminalFontSize, TERMINAL_FONT } from '../../lib/localPrefs'
import { PREF_DEFAULTS } from '../../lib/prefs'
import { getTheme, setTheme, type Theme } from '../../lib/theme'
import { Rows, SaveFooter, Section, useSettingsCache } from './shared'
import { useServerForm, type FormErrors } from './useServerForm'

export function Preferences({ st }: { st: Settings }) {
  const prefs: Prefs = { ...PREF_DEFAULTS, ...st.prefs }
  return (
    <>
      <AppearanceSection terminal={!!st.terminal_enabled} />
      {st.prefs && (
        <>
          <DefaultsSection prefs={prefs} />
          <ThresholdsSection prefs={prefs} />
        </>
      )}
      {/* 有鼠标或触控板的设备才显示（多半也有键盘） */}
      <div className="hidden pointer-fine:block">
        <Section title="快捷键" desc="不在输入框里时生效。随时按 ? 也能打开这份一览。">
          <ShortcutList />
        </Section>
      </div>
    </>
  )
}

/** 跟屏幕有关的显示偏好，只存在当前浏览器，改了立即生效 */
function AppearanceSection({ terminal }: { terminal: boolean }) {
  const [theme, setThemeState] = useState<Theme>(getTheme)
  const [logFont, setLogFont] = useState(storedLogFontSize)
  const [termFont, setTermFont] = useState(storedTerminalFontSize)
  return (
    <Section title="外观" desc="只保存在当前浏览器，手机和电脑各设各的，改了立即生效。">
      <Rows>
        <FormRow label="主题" hint="日志和终端两种主题下都是深色。">
          <Segmented
            label="主题"
            value={theme}
            onChange={(t) => {
              setTheme(t)
              setThemeState(t)
            }}
            options={[
              { value: 'system', label: <><Monitor />跟随系统</> },
              { value: 'light', label: <><Sun />浅色</> },
              { value: 'dark', label: <><Moon />深色</> },
            ]}
          />
        </FormRow>
        <FormRow label="日志和代码字号" htmlFor="pref-log-font" hint="任务日志、代码快照和差异的字号。">
          <Select
            id="pref-log-font"
            className="w-40!"
            value={String(logFont)}
            onChange={(e) => {
              const v = Number(e.target.value)
              setLogFontSize(v)
              setLogFont(v)
            }}
          >
            {LOG_FONT_SIZES.map((v) => (
              <option key={v} value={v}>
                {v} px{v === LOG_FONT_DEFAULT ? '（默认）' : ''}
              </option>
            ))}
          </Select>
        </FormRow>
        {terminal && (
          <FormRow label="网页终端字号" htmlFor="pref-term-font" hint="终端页工具栏上的 − / + 改的也是它。">
            <Select
              id="pref-term-font"
              className="w-40!"
              value={String(termFont)}
              onChange={(e) => {
                const v = Number(e.target.value)
                saveTerminalFontSize(v)
                setTermFont(v)
              }}
            >
              {Array.from({ length: TERMINAL_FONT.max - TERMINAL_FONT.min + 1 }, (_, i) => TERMINAL_FONT.min + i).map((v) => (
                <option key={v} value={v}>
                  {v} px{v === TERMINAL_FONT.default ? '（默认）' : ''}
                </option>
              ))}
            </Select>
          </FormRow>
        )}
      </Rows>
    </Section>
  )
}

// ---------- 存在服务器上的偏好 ----------

const intRule: Partial<Record<keyof Prefs, [number, number]>> = {
  new_job_gpus: [0, 16],
  warn_cpu_percent: [50, 100],
  warn_mem_percent: [50, 100],
  warn_swap_percent: [10, 100],
  warn_gpu_temp_c: [60, 100],
}

/** 整数项在表单里是输入框里的原样文字 */
type PrefForm<K extends keyof Prefs> = { [P in K]: Prefs[P] extends number ? string : Prefs[P] }

function toForm<K extends keyof Prefs>(prefs: Prefs, keys: K[]): PrefForm<K> {
  return Object.fromEntries(keys.map((k) => [k, typeof prefs[k] === 'number' ? String(prefs[k]) : prefs[k]])) as PrefForm<K>
}

function validatePrefs<K extends keyof Prefs>(form: PrefForm<K>, changed: Record<K, boolean>, keys: K[]) {
  const found: FormErrors<PrefForm<K>> = {}
  for (const k of keys) {
    const rule = intRule[k]
    if (!rule || !changed[k]) continue
    const s = String(form[k]).trim()
    if (!/^\d+$/.test(s) || Number(s) < rule[0] || Number(s) > rule[1]) found[k as keyof PrefForm<K> & string] = `需要是 ${rule[0]} 到 ${rule[1]} 之间的整数`
  }
  return found
}

/** 存在服务器上的一组偏好：只提交改了的项，保存后立即生效。"恢复默认"只填进表单，保存才生效 */
function usePrefsForm<K extends keyof Prefs & string>(prefs: Prefs, keys: K[], title: string) {
  const cache = useSettingsCache()
  const f = useServerForm({
    server: toForm(prefs, keys),
    save: (form, changed) => {
      const patch: PrefsPatch = {}
      for (const k of keys) {
        if (!changed[k]) continue
        const v = form[k]
        ;(patch as Record<string, unknown>)[k] = typeof PREF_DEFAULTS[k] === 'number' ? Number(v) : v
      }
      return api.updatePrefs(patch)
    },
    fromResult: (r) => toForm({ ...PREF_DEFAULTS, ...r.prefs }, keys),
    onSaved: (r) => {
      cache.merge({ prefs: r.prefs })
      announce(`${title}已保存`)
    },
    onSettled: cache.refresh,
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (f.check(validatePrefs(f.form, f.changed, keys), keys) && f.dirty) f.m.mutate()
  }
  const isDefault = keys.every((k) => String(f.form[k]) === String(PREF_DEFAULTS[k]))
  const restore = () => {
    for (const k of keys) f.edit(k, toForm(PREF_DEFAULTS, [k])[k])
  }
  return { f, submit, isDefault, restore }
}

function Footer({ saved, dirty, pending, isDefault, restore }: { saved: boolean; dirty: boolean; pending: boolean; isDefault: boolean; restore: () => void }) {
  return (
    <SaveFooter saved={saved} dirty={dirty} pending={pending}>
      <Button size="sm" variant="ghost" disabled={isDefault} onClick={restore}>
        <RotateCcw />
        恢复默认
      </Button>
    </SaveFooter>
  )
}

function Check({ checked, onChange, children }: { checked: boolean; onChange: (v: boolean) => void; children: string }) {
  return (
    <label className="flex min-h-9 items-center gap-2 text-ui text-fg max-sm:min-h-11">
      <input type="checkbox" className="size-4 accent-inv" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {children}
    </label>
  )
}

const defaultKeys = ['new_job_gpus', 'new_job_bashrc', 'new_job_notify_done', 'new_job_conda', 'jobs_page_size', 'log_wrap'] as const

function DefaultsSection({ prefs }: { prefs: Prefs }) {
  const form = usePrefsForm(prefs, [...defaultKeys], '默认值')
  const { f } = form
  return (
    <form onSubmit={form.submit} noValidate>
      <Section title="默认值" desc="存在服务器上，手机和电脑共用。只影响以后新打开的页面和新建的任务。" footer={<Footer saved={f.saved} dirty={f.dirty} pending={f.m.isPending} isDefault={form.isDefault} restore={form.restore} />}>
        <Rows>
          <FormRow label="新建任务的 GPU 数" htmlFor={f.id('new_job_gpus')} hint="复制和重跑任务时沿用原任务的数量。0 表示只用 CPU。">
            <span className="flex items-center gap-2 text-ui text-fg-2">
              <Input {...f.field('new_job_gpus', true)} className="w-20!" inputMode="numeric" value={f.form.new_job_gpus} onChange={(e) => f.edit('new_job_gpus', e.target.value.trim())} />张
            </span>
            <FieldError id={f.errId('new_job_gpus')}>{f.errors.new_job_gpus}</FieldError>
          </FormRow>
          <FormRow label="新建任务的选项" hint="继承 .bashrc 会让启动慢几秒，需要 .bashrc 里的环境变量或 module load 时再打开。失败总会推送到手机，结束提醒控制成功和取消时要不要推送。">
            <Check checked={f.form.new_job_bashrc} onChange={(v) => f.edit('new_job_bashrc', v)}>
              默认继承 .bashrc
            </Check>
            <Check checked={f.form.new_job_notify_done} onChange={(v) => f.edit('new_job_notify_done', v)}>
              默认勾选结束提醒
            </Check>
          </FormRow>
          <FormRow label="新建任务的 conda 环境" hint="自动选择：环境列表加载后选第一个不是 base 的环境。">
            <Segmented
              label="新建任务的 conda 环境"
              value={f.form.new_job_conda}
              onChange={(v) => f.edit('new_job_conda', v)}
              options={[
                { value: 'auto', label: '自动选择' },
                { value: 'none', label: '不预选' },
              ]}
            />
          </FormRow>
          <FormRow label="任务列表每页">
            <Segmented
              label="任务列表每页条数"
              value={f.form.jobs_page_size}
              onChange={(v) => f.edit('jobs_page_size', v)}
              options={[
                { value: '20', label: '20 条' },
                { value: '50', label: '50 条' },
                { value: '100', label: '100 条' },
              ]}
            />
          </FormRow>
          <FormRow label="日志" hint="日志框上随时可以切换，这里决定打开时的默认值。">
            <Check checked={f.form.log_wrap} onChange={(v) => f.edit('log_wrap', v)}>
              打开时自动换行
            </Check>
          </FormRow>
          {f.m.error && (
            <div className="px-4 py-3 sm:px-5">
              <ErrorBox error={f.m.error} />
            </div>
          )}
        </Rows>
      </Section>
    </form>
  )
}

const thresholdKeys = ['warn_cpu_percent', 'warn_mem_percent', 'warn_swap_percent', 'warn_gpu_temp_c'] as const
const thresholdMeta: Record<(typeof thresholdKeys)[number], { label: string; unit: string }> = {
  warn_cpu_percent: { label: 'CPU 使用率', unit: '%' },
  warn_mem_percent: { label: '内存使用率', unit: '%' },
  warn_swap_percent: { label: '交换区使用率', unit: '%' },
  warn_gpu_temp_c: { label: 'GPU 温度', unit: '°C' },
}

function ThresholdsSection({ prefs }: { prefs: Prefs }) {
  const form = usePrefsForm(prefs, [...thresholdKeys], '资源页告警阈值')
  const { f } = form
  return (
    <form onSubmit={form.submit} noValidate>
      <Section title="资源页告警阈值" desc="资源页上超过这些值时用告警色标出。只影响显示，不推送、不影响调度。存在服务器上，手机和电脑共用。" footer={<Footer saved={f.saved} dirty={f.dirty} pending={f.m.isPending} isDefault={form.isDefault} restore={form.restore} />}>
        <Rows>
          {thresholdKeys.map((k) => (
            <FormRow key={k} label={thresholdMeta[k].label} htmlFor={f.id(k)}>
              <span className="flex items-center gap-2 text-ui text-fg-2">
                <Input {...f.field(k)} className="w-20!" inputMode="numeric" value={f.form[k]} onChange={(e) => f.edit(k, e.target.value.trim())} />
                {thresholdMeta[k].unit}
              </span>
              <FieldError id={f.errId(k)}>{f.errors[k]}</FieldError>
            </FormRow>
          ))}
          {f.m.error && (
            <div className="px-4 py-3 sm:px-5">
              <ErrorBox error={f.m.error} />
            </div>
          )}
        </Rows>
      </Section>
    </form>
  )
}
