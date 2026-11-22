import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Dices, LogOut, ShieldCheck } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { fmtTime, type DeviceToken, type LoginSession, type SecurityPatch, type SecuritySettings, type Settings } from '@danlu/core'
import { announce, Button, Dialog, ErrorBox, Field, FieldError, FormRow, Input, Select, Skeleton } from '../../components/ui'
import { api } from '../../lib/api'
import { t } from '../../lib/i18n'
import { KV, Rows, SaveFooter, Section } from './shared'
import { useServerForm, type FormErrors } from './useServerForm'

export function Security({ st }: { st: Settings }) {
  const logout = useMutation({ mutationFn: api.logout, onSuccess: () => (location.href = '/login') })
  return (
    <>
      {st.security ? (
        <SecuritySection security={st.security} totpEnabled={!!st.totp_enabled} />
      ) : (
        // 旧版服务器没有安全设置，两步验证状态仍显示在这里
        <Section title={t('settings.security.totp')}>
          <dl className="-my-2">
            <KV k={t('settings.security.totp')} v={<TotpStatus enabled={!!st.totp_enabled} />} />
          </dl>
        </Section>
      )}
      <PasswordSection />
      <SessionsSection />
      <TokensSection />
      <Button className="w-full sm:w-auto" loading={logout.isPending} onClick={() => logout.mutate()}>
        <LogOut />
        {t('settings.security.logout')}
      </Button>
    </>
  )
}

function TotpStatus({ enabled }: { enabled: boolean }) {
  return (
    <>
      {enabled ? t('settings.on') : t('settings.off')}
      <span className="text-fg-3">
        {t('settings.security.totp_run')}
        <code className="font-mono text-[12.5px]">{enabled ? 'danlu totp disable' : 'danlu totp enable'}</code>
        {enabled ? t('settings.security.totp_disable_word') : t('settings.security.totp_enable_word')}
      </span>
    </>
  )
}

// ---------- 安全设置 ----------

// 和服务器 config.ValidEntrance 一致：入口地址一律被拦下来发 cookie，和面板页面同名的话那个页面就打不开了
const reservedEntrances = ['api', 'assets', 'login', 'compare', 'resources', 'settings', 'terminal']

/** 和 danlu init 一样：12 位小写字母和数字 */
function randomEntrance() {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let out = ''
  while (out.length < 12) {
    for (const b of crypto.getRandomValues(new Uint8Array(16))) {
      // 252 = 36 × 7，丢掉多出来的几个值，每个字符的概率才一样
      if (b < 252 && out.length < 12) out += chars[b % 36]
    }
  }
  return out
}

type TtlUnit = 'm' | 'h' | 'd'
const ttlUnits: { value: TtlUnit; seconds: number }[] = [
  { value: 'm', seconds: 60 },
  { value: 'h', seconds: 3600 },
  { value: 'd', seconds: 86400 },
]
/** 单位词按当前语言取值，不在模块级常量里冻结 */
function ttlUnitLabel(u: TtlUnit): string {
  switch (u) {
    case 'm':
      return t('settings.unit_m')
    case 'h':
      return t('settings.unit_h')
    case 'd':
      return t('settings.unit_d')
  }
}
const unitSeconds = (u: TtlUnit) => ttlUnits.find((x) => x.value === u)!.seconds

/** 表单里都是输入框里的原样文字；有没有改动按文字比，免得服务器上不是整分钟的旧值被悄悄改掉 */
type SecurityForm = {
  entrance: string
  ttl: { amount: string; unit: TtlUnit }
  maxFailures: string
  lockout: string
}

function formFrom(sec: SecuritySettings): SecurityForm {
  const unit: TtlUnit = sec.session_ttl % 86400 === 0 ? 'd' : sec.session_ttl % 3600 === 0 ? 'h' : 'm'
  return {
    entrance: sec.entrance,
    ttl: { amount: String(Math.round(sec.session_ttl / unitSeconds(unit))), unit },
    maxFailures: String(sec.max_failures),
    lockout: String(Math.round(sec.lockout / 60)),
  }
}

const isInt = (s: string) => /^\d+$/.test(s.trim())

function validateSecurity(f: SecurityForm, changed: Record<keyof SecurityForm, boolean>) {
  const found: FormErrors<SecurityForm> = {}
  // 只查改过的项：config.toml 里超出面板范围的旧值照样生效，不拦着改别的项
  if (changed.entrance) {
    if (!/^[a-z0-9]{6,64}$/.test(f.entrance)) found.entrance = t('settings.security.err_entrance')
    else if (reservedEntrances.includes(f.entrance)) found.entrance = t('settings.security.err_entrance_reserved')
  }
  if (changed.ttl) {
    const sec = Number(f.ttl.amount) * unitSeconds(f.ttl.unit)
    if (!isInt(f.ttl.amount) || sec < 600 || sec > 365 * 86400) found.ttl = t('settings.security.err_ttl')
  }
  if (changed.maxFailures && (!isInt(f.maxFailures) || Number(f.maxFailures) < 1 || Number(f.maxFailures) > 100)) found.maxFailures = t('settings.security.err_max_failures')
  if (changed.lockout && (!isInt(f.lockout) || Number(f.lockout) < 1 || Number(f.lockout) > 1440)) found.lockout = t('settings.security.err_lockout')
  return found
}

function SecuritySection({ security, totpEnabled }: { security: SecuritySettings; totpEnabled: boolean }) {
  const qc = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const f = useServerForm({
    server: formFrom(security),
    save: (form, changed) => {
      const p: SecurityPatch = {}
      if (changed.entrance) p.entrance = form.entrance
      if (changed.ttl) p.session_ttl = Number(form.ttl.amount) * unitSeconds(form.ttl.unit)
      if (changed.maxFailures) p.max_failures = Number(form.maxFailures)
      if (changed.lockout) p.lockout = Number(form.lockout) * 60
      return api.updateSecurity(p)
    },
    fromResult: (r) => formFrom(r.security),
    onSaved: () => {
      setConfirming(false)
      announce(t('settings.section_saved', { title: t('settings.security.title') }))
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['settings'] })
      qc.invalidateQueries({ queryKey: ['sessions'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
    },
  })
  const { form, changed, errors, m } = f

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!f.check(validateSecurity(form, changed)) || !f.dirty) return
    // 换入口会把别的设备关在门外，先确认
    if (changed.entrance) setConfirming(true)
    else m.mutate()
  }
  const newURL = `${security.base_url}/${form.entrance}`

  return (
    <form onSubmit={submit} noValidate>
      <Section title={t('settings.security.title')} desc={t('settings.security.desc')} footer={<SaveFooter saved={f.saved} dirty={f.dirty} pending={m.isPending && !confirming} />}>
        <Rows>
          <FormRow label={t('settings.security.entrance')} htmlFor={f.id('entrance')} hint={t('settings.security.entrance_hint')}>
            <div className="flex gap-2">
              <Input
                {...f.field('entrance', true)}
                className="flex-1 font-mono text-[12.5px]"
                value={form.entrance}
                onChange={(e) => f.edit('entrance', e.target.value.toLowerCase().replace(/\s/g, ''))}
                autoComplete="off"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                maxLength={64}
              />
              <Button onClick={() => f.edit('entrance', randomEntrance())}>
                <Dices />
                {t('settings.security.random')}
              </Button>
            </div>
            <p className="mt-1.5 break-all text-xs leading-relaxed text-fg-3">
              {changed.entrance ? t('settings.security.new_url') : t('settings.security.full_url')}
              <span className="font-mono text-fg-2">{newURL}</span>
            </p>
            <FieldError id={f.errId('entrance')}>{errors.entrance}</FieldError>
          </FormRow>

          <FormRow label={t('settings.security.ttl')} hint={t('settings.security.ttl_hint')} describedBy={f.describe('ttl')}>
            <div className="flex items-center gap-2">
              <Input {...f.field('ttl')} aria-label={t('settings.security.ttl_amount_aria')} className="w-24!" inputMode="numeric" value={form.ttl.amount} onChange={(e) => f.edit('ttl', { ...form.ttl, amount: e.target.value.trim() })} />
              <Select aria-label={t('settings.security.ttl_unit_aria')} className="w-24!" value={form.ttl.unit} onChange={(e) => f.edit('ttl', { ...form.ttl, unit: e.target.value as TtlUnit })}>
                {ttlUnits.map((u) => (
                  <option key={u.value} value={u.value}>
                    {ttlUnitLabel(u.value)}
                  </option>
                ))}
              </Select>
            </div>
            <FieldError id={f.errId('ttl')}>{errors.ttl}</FieldError>
          </FormRow>

          <FormRow
            label={t('settings.security.lockout')}
            hint={t('settings.security.lockout_hint')}
            describedBy={[errors.maxFailures && f.errId('maxFailures'), errors.lockout && f.errId('lockout')].filter(Boolean).join(' ') || undefined}
          >
            {/* 窄屏放不下一行时，在"次，"后面折行，不把"分钟"单独甩到下一行 */}
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-ui text-fg-2">
              <span className="flex items-center gap-2">
                {t('settings.security.lockout_prefix')}
                <Input {...f.field('maxFailures')} aria-label={t('settings.security.max_failures_aria')} className="w-20!" inputMode="numeric" value={form.maxFailures} onChange={(e) => f.edit('maxFailures', e.target.value.trim())} />
                {t('settings.security.lockout_times')}
              </span>
              <span className="flex items-center gap-2">
                {t('settings.security.lockout_lock')}
                <Input {...f.field('lockout')} aria-label={t('settings.security.lockout_minutes_aria')} className="w-20!" inputMode="numeric" value={form.lockout} onChange={(e) => f.edit('lockout', e.target.value.trim())} />
                {t('settings.unit_m')}
              </span>
            </div>
            <FieldError id={f.errId('maxFailures')}>{errors.maxFailures}</FieldError>
            <FieldError id={f.errId('lockout')}>{errors.lockout}</FieldError>
          </FormRow>

          <FormRow label={t('settings.security.totp')} hint={t('settings.security.totp_hint')}>
            <p className="text-ui leading-relaxed text-fg">
              <TotpStatus enabled={totpEnabled} />
            </p>
          </FormRow>
          {/* 换入口时的错误显示在确认框里 */}
          {!confirming && m.error && (
            <div className="px-4 py-3 sm:px-5">
              <ErrorBox error={m.error} />
            </div>
          )}
        </Rows>
      </Section>

      {confirming && (
        <Dialog
          title={t('settings.security.change_title')}
          onClose={() => setConfirming(false)}
          footer={
            <>
              <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{t('settings.security.change_cancel')}</Button>
              <Button variant="primary" loading={m.isPending} onClick={() => m.mutate()}>
                {t('settings.security.change_confirm')}
              </Button>
            </>
          }
        >
          <p>{t('settings.security.change_body')}</p>
          <p className="break-all rounded-md border border-line bg-subtle px-3 py-2 font-mono text-xs text-fg">{newURL}</p>
          <ErrorBox error={m.error} />
        </Dialog>
      )}
    </form>
  )
}

// ---------- 修改密码 ----------

function PasswordSection() {
  const [old, setOld] = useState('')
  const [next, setNext] = useState('')
  const [again, setAgain] = useState('')
  // 字段错误写在字段下方，不用浏览器自带的必填气泡（和登录页、新建页一样）
  const [errs, setErrs] = useState<Partial<Record<'old' | 'next' | 'again', string>>>({})
  const uid = useId()
  const ids = { old: `${uid}-old`, next: `${uid}-next`, again: `${uid}-again` }
  const field = (k: keyof typeof ids) => ({ id: ids[k], 'aria-invalid': !!errs[k] || undefined, 'aria-describedby': errs[k] ? `${ids[k]}-err` : undefined })
  const clear = (k: keyof typeof ids) => errs[k] && setErrs((e) => ({ ...e, [k]: undefined }))
  const m = useMutation({
    mutationFn: () => api.changePassword(old, next),
    onSuccess: () => {
      setOld('')
      setNext('')
      setAgain('')
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const found: typeof errs = {}
    if (!old) found.old = t('settings.security.err_old_required')
    if (!next) found.next = t('settings.security.err_new_required')
    else if (Array.from(next).length < 8) found.next = t('settings.security.err_new_short')
    if (!again) found.again = t('settings.security.err_again_required')
    else if (next && next !== again) found.again = t('settings.security.err_mismatch')
    setErrs(found)
    const first = (['old', 'next', 'again'] as const).find((k) => found[k])
    if (first) {
      document.getElementById(ids[first])?.focus()
      return
    }
    m.mutate()
  }
  return (
    <form onSubmit={submit} noValidate>
      <Section
        title={t('settings.security.password')}
        desc={t('settings.security.password_desc')}
        footer={
          <>
            {m.isSuccess && (
              <span className="flex items-center gap-1.5 text-ui text-ok">
                <ShieldCheck className="size-4" />
                {t('settings.security.password_saved')}
              </span>
            )}
            <Button type="submit" size="sm" variant="primary" className="ml-auto" loading={m.isPending}>
              {t('common.save')}
            </Button>
          </>
        }
      >
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label={t('settings.security.password_old')} error={errs.old} errorId={`${ids.old}-err`}>
            <Input {...field('old')} type="password" value={old} onChange={(e) => (setOld(e.target.value), clear('old'))} autoComplete="current-password" />
          </Field>
          <Field label={t('settings.security.password_new')} error={errs.next} errorId={`${ids.next}-err`}>
            <Input {...field('next')} type="password" value={next} onChange={(e) => (setNext(e.target.value), clear('next'))} autoComplete="new-password" />
          </Field>
          <Field label={t('settings.security.password_again')} error={errs.again} errorId={`${ids.again}-err`}>
            <Input {...field('again')} type="password" value={again} onChange={(e) => (setAgain(e.target.value), clear('again'))} autoComplete="new-password" />
          </Field>
        </div>
        <ErrorBox error={m.error} className="mt-3" />
      </Section>
    </form>
  )
}

// ---------- 登录会话 ----------

/** 从 User-Agent 认出浏览器和系统，认不出时照实说 */
function deviceName(ua: string) {
  if (!ua) return t('settings.security.unknown_device')
  const browser = /Edg(A|iOS)?\//.test(ua)
    ? 'Edge'
    : /OPR\//.test(ua)
      ? 'Opera'
      : /Firefox\/|FxiOS\//.test(ua)
        ? 'Firefox'
        : /Chrome\/|CriOS\//.test(ua)
          ? 'Chrome'
          : /Safari\//.test(ua)
            ? 'Safari'
            : ua.split(/[\s/]/)[0] || t('settings.security.browser_fallback')
  // iPhone 和 iPad 的 UA 里也有 "Mac OS X"，安卓的 UA 里也有 "Linux"，先判断它们
  const os = /Android/.test(ua)
    ? 'Android'
    : /iPhone|iPod/.test(ua)
      ? 'iPhone'
      : /iPad/.test(ua)
        ? 'iPad'
        : /Windows/.test(ua)
          ? 'Windows'
          : /Mac OS X|Macintosh/.test(ua)
            ? 'macOS'
            : /CrOS/.test(ua)
              ? 'ChromeOS'
              : /Linux/.test(ua)
                ? 'Linux'
                : ''
  return os ? `${browser} · ${os}` : browser
}

/** 用密码登录的浏览器。手机丢了的时候，在这里把它踢下线 */
function SessionsSection() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['sessions'], queryFn: api.sessions })
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['sessions'] })
    qc.invalidateQueries({ queryKey: ['audit'] })
  }
  const revoke = useMutation({ mutationFn: (id: string) => api.revokeSession(id), onSuccess: () => announce(t('settings.security.session_revoked')), onSettled: refresh })
  const others = useMutation({ mutationFn: api.revokeOtherSessions, onSuccess: (r) => announce(t('settings.security.sessions_revoked', { n: r.revoked })), onSettled: refresh })
  const list: LoginSession[] = q.data?.sessions ?? []
  const otherCount = list.filter((s) => !s.current).length
  const error = revoke.error || others.error
  return (
    <Section
      title={t('settings.security.sessions')}
      desc={t('settings.security.sessions_desc')}
      footer={
        otherCount > 1 ? (
          <>
            <span className="min-w-0 flex-1 text-ui text-fg-2">{t('settings.security.other_devices', { n: otherCount })}</span>
            <Button size="sm" variant="danger" loading={others.isPending} onClick={() => others.mutate()}>
              {t('settings.security.revoke_others')}
            </Button>
          </>
        ) : undefined
      }
    >
      {q.isLoading ? (
        <Skeleton className="h-10 w-full" />
      ) : q.error ? (
        <ErrorBox error={q.error} />
      ) : list.length === 0 ? (
        <p className="text-ui text-fg-3">{t('settings.security.sessions_empty')}</p>
      ) : (
        <Rows>
          {list.map((s) => (
            <div key={s.id} className="flex items-center gap-3 px-4 py-2.5 text-ui sm:px-5">
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-medium" title={s.user_agent || undefined}>
                    {deviceName(s.user_agent)}
                  </span>
                  {s.current && <span className="shrink-0 rounded border border-line-strong px-1.5 text-xs leading-[18px] text-fg-2">{t('settings.security.current_device')}</span>}
                </div>
                <div className="truncate text-xs text-fg-3">
                  <span className="font-mono">{s.ip || t('settings.security.unknown_ip')}</span>
                  {t('settings.security.session_times', { created: fmtTime(s.created_at).slice(5, 16), expires: fmtTime(s.expires_at).slice(5, 16) })}
                </div>
              </div>
              {!s.current && (
                <Button size="sm" variant="danger" loading={revoke.isPending && revoke.variables === s.id} onClick={() => revoke.mutate(s.id)}>
                  {t('settings.security.revoke')}
                </Button>
              )}
            </div>
          ))}
          {error && (
            <div className="px-4 py-3 sm:px-5">
              <ErrorBox error={error} />
            </div>
          )}
        </Rows>
      )}
    </Section>
  )
}

// ---------- 设备令牌 ----------

/** 桌面客户端的设备令牌。电脑丢了的时候，用手机打开面板在这里吊销 */
function TokensSection() {
  const q = useQuery({ queryKey: ['tokens'], queryFn: api.tokens })
  const [revoking, setRevoking] = useState<DeviceToken | null>(null)
  return (
    <Section
      title={t('settings.security.tokens')}
      desc={
        <>
          {t('settings.security.tokens_desc_1')}<code className="font-mono text-[12px]">danlu token create</code>{t('settings.security.tokens_desc_2')}
        </>
      }
    >
      {q.isLoading ? (
        <Skeleton className="h-10 w-full" />
      ) : q.error ? (
        <ErrorBox error={q.error} />
      ) : q.data!.tokens.length === 0 ? (
        <p className="text-ui text-fg-3">{t('settings.security.tokens_empty')}</p>
      ) : (
        <Rows>
          {q.data!.tokens.map((tok) => (
            <div key={tok.id} className="flex items-center gap-3 px-4 py-2.5 text-ui sm:px-5">
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{tok.name}</div>
                <div className="truncate text-xs text-fg-3">
                  {t('settings.security.token_created', { time: fmtTime(tok.created_at).slice(0, 16) })}{' '}
                  {tok.last_used_at ? (
                    <>
                      {t('settings.security.token_last_used', { time: fmtTime(tok.last_used_at).slice(5, 16) })} <span className="font-mono">{tok.last_ip}</span>
                    </>
                  ) : (
                    t('settings.security.token_never_used')
                  )}
                </div>
              </div>
              <Button size="sm" variant="danger" onClick={() => setRevoking(tok)}>
                {t('settings.security.token_revoke')}
              </Button>
            </div>
          ))}
        </Rows>
      )}
      {revoking && <RevokeDialog token={revoking} onClose={() => setRevoking(null)} />}
    </Section>
  )
}

function RevokeDialog({ token: tok, onClose }: { token: DeviceToken; onClose: () => void }) {
  const qc = useQueryClient()
  const revoke = useMutation({
    mutationFn: () => api.revokeToken(tok.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tokens'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
      onClose()
    },
  })
  return (
    <Dialog
      title={t('settings.security.revoke_title', { name: tok.name })}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{t('settings.security.revoke_keep')}</Button>
          <Button variant="danger" loading={revoke.isPending} onClick={() => revoke.mutate()}>
            {t('settings.security.token_revoke')}
          </Button>
        </>
      }
    >
      <p>{t('settings.security.revoke_body')}</p>
      <ErrorBox error={revoke.error} />
    </Dialog>
  )
}
