import { Monitor, Moon, RotateCcw, Sun } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import type { Prefs, PrefsPatch, Settings } from '@danlu/core'
import { ShortcutList } from '../../components/Shortcuts'
import { announce, Button, ErrorBox, FieldError, FormRow, Input, Segmented, Select } from '../../components/ui'
import { api } from '../../lib/api'
import { LOG_FONT_DEFAULT, LOG_FONT_SIZES, setLogFontSize, storedLogFontSize, storedTerminalFontSize, saveTerminalFontSize, TERMINAL_FONT } from '../../lib/localPrefs'
import { setLang, storedLang, t, type LangPref } from '../../lib/i18n'
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
        <Section title={t('settings.prefs.shortcuts')} desc={t('settings.prefs.shortcuts_desc')}>
          <ShortcutList />
        </Section>
      </div>
    </>
  )
}

/** 跟屏幕有关的显示偏好，只存在当前浏览器，改了立即生效 */
function AppearanceSection({ terminal }: { terminal: boolean }) {
  const [theme, setThemeState] = useState<Theme>(getTheme)
  const [lang, setLangState] = useState<LangPref>(storedLang)
  const [logFont, setLogFont] = useState(storedLogFontSize)
  const [termFont, setTermFont] = useState(storedTerminalFontSize)
  return (
    <Section title={t('settings.prefs.appearance')} desc={t('settings.prefs.appearance_desc')}>
      <Rows>
        <FormRow label={t('settings.prefs.lang')} htmlFor="pref-lang" hint={t('settings.prefs.lang_hint')}>
          <Select
            id="pref-lang"
            className="w-40!"
            value={lang}
            onChange={(e) => {
              const v = e.target.value as LangPref
              setLang(v)
              setLangState(v)
            }}
          >
            <option value="auto">{t('settings.prefs.lang_auto')}</option>
            <option value="zh-CN">{t('settings.prefs.lang_zh')}</option>
            <option value="en">{t('settings.prefs.lang_en')}</option>
          </Select>
        </FormRow>
        <FormRow label={t('settings.prefs.theme')} hint={t('settings.prefs.theme_hint')}>
          <Segmented
            label={t('settings.prefs.theme')}
            value={theme}
            onChange={(t) => {
              setTheme(t)
              setThemeState(t)
            }}
            options={[
              { value: 'system', label: <><Monitor />{t('settings.prefs.theme_system')}</> },
              { value: 'light', label: <><Sun />{t('settings.prefs.theme_light')}</> },
              { value: 'dark', label: <><Moon />{t('settings.prefs.theme_dark')}</> },
            ]}
          />
        </FormRow>
        <FormRow label={t('settings.prefs.log_font')} htmlFor="pref-log-font" hint={t('settings.prefs.log_font_hint')}>
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
                {v} px{v === LOG_FONT_DEFAULT ? t('settings.prefs.default_suffix') : ''}
              </option>
            ))}
          </Select>
        </FormRow>
        {terminal && (
          <FormRow label={t('settings.prefs.term_font')} htmlFor="pref-term-font" hint={t('settings.prefs.term_font_hint')}>
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
                  {v} px{v === TERMINAL_FONT.default ? t('settings.prefs.default_suffix') : ''}
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
    if (!/^\d+$/.test(s) || Number(s) < rule[0] || Number(s) > rule[1]) found[k as keyof PrefForm<K> & string] = t('settings.prefs.err_int_range', { min: rule[0], max: rule[1] })
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
      announce(t('settings.section_saved', { title }))
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
        {t('settings.prefs.restore_defaults')}
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
  const form = usePrefsForm(prefs, [...defaultKeys], t('settings.prefs.defaults'))
  const { f } = form
  return (
    <form onSubmit={form.submit} noValidate>
      <Section title={t('settings.prefs.defaults')} desc={t('settings.prefs.defaults_desc')} footer={<Footer saved={f.saved} dirty={f.dirty} pending={f.m.isPending} isDefault={form.isDefault} restore={form.restore} />}>
        <Rows>
          <FormRow label={t('settings.prefs.new_job_gpus')} htmlFor={f.id('new_job_gpus')} hint={t('settings.prefs.new_job_gpus_hint')}>
            <span className="flex items-center gap-2 text-ui text-fg-2">
              <Input {...f.field('new_job_gpus', true)} className="w-20!" inputMode="numeric" value={f.form.new_job_gpus} onChange={(e) => f.edit('new_job_gpus', e.target.value.trim())} />{t('settings.prefs.gpu_suffix', { n: Number(f.form.new_job_gpus) })}
            </span>
            <FieldError id={f.errId('new_job_gpus')}>{f.errors.new_job_gpus}</FieldError>
          </FormRow>
          <FormRow label={t('settings.prefs.new_job_options')} hint={t('settings.prefs.new_job_options_hint')}>
            <Check checked={f.form.new_job_bashrc} onChange={(v) => f.edit('new_job_bashrc', v)}>
              {t('settings.prefs.new_job_bashrc')}
            </Check>
            <Check checked={f.form.new_job_notify_done} onChange={(v) => f.edit('new_job_notify_done', v)}>
              {t('settings.prefs.new_job_notify')}
            </Check>
          </FormRow>
          <FormRow label={t('settings.prefs.new_job_conda')} hint={t('settings.prefs.new_job_conda_hint')}>
            <Segmented
              label={t('settings.prefs.new_job_conda')}
              value={f.form.new_job_conda}
              onChange={(v) => f.edit('new_job_conda', v)}
              options={[
                { value: 'auto', label: t('settings.prefs.conda_auto') },
                { value: 'none', label: t('settings.prefs.conda_none') },
              ]}
            />
          </FormRow>
          <FormRow label={t('settings.prefs.page_size')}>
            <Segmented
              label={t('settings.prefs.page_size_aria')}
              value={f.form.jobs_page_size}
              onChange={(v) => f.edit('jobs_page_size', v)}
              options={[
                { value: '20', label: t('settings.prefs.page_size_option', { n: 20 }) },
                { value: '50', label: t('settings.prefs.page_size_option', { n: 50 }) },
                { value: '100', label: t('settings.prefs.page_size_option', { n: 100 }) },
              ]}
            />
          </FormRow>
          <FormRow label={t('settings.prefs.log')} hint={t('settings.prefs.log_hint')}>
            <Check checked={f.form.log_wrap} onChange={(v) => f.edit('log_wrap', v)}>
              {t('settings.prefs.log_wrap')}
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
// 表里存目录键而不是文案，避免模块加载时冻结语言；单位符号不翻译
const thresholdMeta: Record<(typeof thresholdKeys)[number], { labelKey: Parameters<typeof t>[0]; unit: string }> = {
  warn_cpu_percent: { labelKey: 'settings.prefs.warn_cpu', unit: '%' },
  warn_mem_percent: { labelKey: 'settings.prefs.warn_mem', unit: '%' },
  warn_swap_percent: { labelKey: 'settings.prefs.warn_swap', unit: '%' },
  warn_gpu_temp_c: { labelKey: 'settings.prefs.warn_gpu_temp', unit: '°C' },
}

function ThresholdsSection({ prefs }: { prefs: Prefs }) {
  const form = usePrefsForm(prefs, [...thresholdKeys], t('settings.prefs.thresholds'))
  const { f } = form
  return (
    <form onSubmit={form.submit} noValidate>
      <Section title={t('settings.prefs.thresholds')} desc={t('settings.prefs.thresholds_desc')} footer={<Footer saved={f.saved} dirty={f.dirty} pending={f.m.isPending} isDefault={form.isDefault} restore={form.restore} />}>
        <Rows>
          {thresholdKeys.map((k) => (
            <FormRow key={k} label={t(thresholdMeta[k].labelKey)} htmlFor={f.id(k)}>
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
