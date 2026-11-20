import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Dices, LogOut, ShieldCheck } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { fmtTime, type DeviceToken, type LoginSession, type SecurityPatch, type SecuritySettings, type Settings } from '@danlu/core'
import { announce, Button, Dialog, ErrorBox, Field, FieldError, FormRow, Input, Select, Skeleton } from '../../components/ui'
import { api } from '../../lib/api'
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
        <Section title="两步验证">
          <dl className="-my-2">
            <KV k="两步验证" v={<TotpStatus enabled={!!st.totp_enabled} />} />
          </dl>
        </Section>
      )}
      <PasswordSection />
      <SessionsSection />
      <TokensSection />
      <Button className="w-full sm:w-auto" loading={logout.isPending} onClick={() => logout.mutate()}>
        <LogOut />
        退出登录
      </Button>
    </>
  )
}

function TotpStatus({ enabled }: { enabled: boolean }) {
  return (
    <>
      {enabled ? '已开启' : '未开启'}
      <span className="text-fg-3">
        {' · 在服务器上运行 '}
        <code className="font-mono text-[12.5px]">{enabled ? 'danlu totp disable' : 'danlu totp enable'}</code>
        {enabled ? ' 关闭' : ' 开启'}
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
const ttlUnits: { value: TtlUnit; label: string; seconds: number }[] = [
  { value: 'm', label: '分钟', seconds: 60 },
  { value: 'h', label: '小时', seconds: 3600 },
  { value: 'd', label: '天', seconds: 86400 },
]
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
    if (!/^[a-z0-9]{6,64}$/.test(f.entrance)) found.entrance = '需要 6–64 位小写字母或数字'
    else if (reservedEntrances.includes(f.entrance)) found.entrance = '不能和面板页面重名'
  }
  if (changed.ttl) {
    const sec = Number(f.ttl.amount) * unitSeconds(f.ttl.unit)
    if (!isInt(f.ttl.amount) || sec < 600 || sec > 365 * 86400) found.ttl = '需要在 10 分钟到 365 天之间'
  }
  if (changed.maxFailures && (!isInt(f.maxFailures) || Number(f.maxFailures) < 1 || Number(f.maxFailures) > 100)) found.maxFailures = '失败次数需要是 1 到 100 之间的整数'
  if (changed.lockout && (!isInt(f.lockout) || Number(f.lockout) < 1 || Number(f.lockout) > 1440)) found.lockout = '锁定时长需要是 1 到 1440 分钟（24 小时）之间的整数'
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
      announce('安全设置已保存')
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
      <Section title="安全设置" desc="保存后立即生效，不用重启服务。" footer={<SaveFooter saved={f.saved} dirty={f.dirty} pending={m.isPending && !confirming} />}>
        <Rows>
          <FormRow label="安全入口" htmlFor={f.id('entrance')} hint="不经过入口地址，所有页面都返回 404。更换后旧地址立即失效。">
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
                随机生成
              </Button>
            </div>
            <p className="mt-1.5 break-all text-xs leading-relaxed text-fg-3">
              {changed.entrance ? '新地址 ' : '完整地址 '}
              <span className="font-mono text-fg-2">{newURL}</span>
            </p>
            <FieldError id={f.errId('entrance')}>{errors.entrance}</FieldError>
          </FormRow>

          <FormRow label="登录有效期" hint="登录和入口 cookie 的有效期，10 分钟到 365 天。缩短后，已有的登录也按新时长到期。" describedBy={f.describe('ttl')}>
            <div className="flex items-center gap-2">
              <Input {...f.field('ttl')} aria-label="时长" className="w-24!" inputMode="numeric" value={form.ttl.amount} onChange={(e) => f.edit('ttl', { ...form.ttl, amount: e.target.value.trim() })} />
              <Select aria-label="时长单位" className="w-24!" value={form.ttl.unit} onChange={(e) => f.edit('ttl', { ...form.ttl, unit: e.target.value as TtlUnit })}>
                {ttlUnits.map((u) => (
                  <option key={u.value} value={u.value}>
                    {u.label}
                  </option>
                ))}
              </Select>
            </div>
            <FieldError id={f.errId('ttl')}>{errors.ttl}</FieldError>
          </FormRow>

          <FormRow
            label="登录失败锁定"
            hint="同一 IP 连续输错密码或验证码达到次数后锁定，锁定期间密码对了也不能登录。"
            describedBy={[errors.maxFailures && f.errId('maxFailures'), errors.lockout && f.errId('lockout')].filter(Boolean).join(' ') || undefined}
          >
            {/* 窄屏放不下一行时，在"次，"后面折行，不把"分钟"单独甩到下一行 */}
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-ui text-fg-2">
              <span className="flex items-center gap-2">
                连续输错
                <Input {...f.field('maxFailures')} aria-label="失败次数上限" className="w-20!" inputMode="numeric" value={form.maxFailures} onChange={(e) => f.edit('maxFailures', e.target.value.trim())} />
                次，
              </span>
              <span className="flex items-center gap-2">
                锁定
                <Input {...f.field('lockout')} aria-label="锁定分钟数" className="w-20!" inputMode="numeric" value={form.lockout} onChange={(e) => f.edit('lockout', e.target.value.trim())} />
                分钟
              </span>
            </div>
            <FieldError id={f.errId('maxFailures')}>{errors.maxFailures}</FieldError>
            <FieldError id={f.errId('lockout')}>{errors.lockout}</FieldError>
          </FormRow>

          <FormRow label="两步验证" hint="登录时除了密码，还要输入验证器 App 里的 6 位验证码。">
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
          title="更换安全入口？"
          onClose={() => setConfirming(false)}
          footer={
            <>
              <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>暂不更换</Button>
              <Button variant="primary" loading={m.isPending} onClick={() => m.mutate()}>
                更换并保存
              </Button>
            </>
          }
        >
          <p>旧地址立即失效。这个浏览器会自动换上新入口；其他打开过面板的设备，以及手机推送里的链接，要先用新地址打开一次。已登录的会话不受影响。</p>
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
    if (!old) found.old = '请填写当前密码'
    if (!next) found.next = '请填写新密码'
    else if (Array.from(next).length < 8) found.next = '新密码至少 8 位'
    if (!again) found.again = '请再输入一次新密码'
    else if (next && next !== again) found.again = '两次输入的新密码不一致'
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
        title="修改密码"
        desc="至少 8 位。修改后，其他设备上的登录会失效。"
        footer={
          <>
            {m.isSuccess && (
              <span className="flex items-center gap-1.5 text-ui text-ok">
                <ShieldCheck className="size-4" />
                密码已修改
              </span>
            )}
            <Button type="submit" size="sm" variant="primary" className="ml-auto" loading={m.isPending}>
              保存
            </Button>
          </>
        }
      >
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="当前密码" error={errs.old} errorId={`${ids.old}-err`}>
            <Input {...field('old')} type="password" value={old} onChange={(e) => (setOld(e.target.value), clear('old'))} autoComplete="current-password" />
          </Field>
          <Field label="新密码" error={errs.next} errorId={`${ids.next}-err`}>
            <Input {...field('next')} type="password" value={next} onChange={(e) => (setNext(e.target.value), clear('next'))} autoComplete="new-password" />
          </Field>
          <Field label="再输入一次" error={errs.again} errorId={`${ids.again}-err`}>
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
  if (!ua) return '未知设备'
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
            : ua.split(/[\s/]/)[0] || '浏览器'
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
  const revoke = useMutation({ mutationFn: (id: string) => api.revokeSession(id), onSuccess: () => announce('已注销这个会话'), onSettled: refresh })
  const others = useMutation({ mutationFn: api.revokeOtherSessions, onSuccess: (r) => announce(`已注销 ${r.revoked} 个会话`), onSettled: refresh })
  const list: LoginSession[] = q.data?.sessions ?? []
  const otherCount = list.filter((s) => !s.current).length
  const error = revoke.error || others.error
  return (
    <Section
      title="登录会话"
      desc="用密码登录、还没到期的浏览器。注销后那台设备需要重新登录；桌面客户端用设备令牌连接，不在这里。"
      footer={
        otherCount > 1 ? (
          <>
            <span className="min-w-0 flex-1 text-ui text-fg-2">另有 {otherCount} 个设备登录着</span>
            <Button size="sm" variant="danger" loading={others.isPending} onClick={() => others.mutate()}>
              注销其他设备
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
        <p className="text-ui text-fg-3">没有用密码登录的浏览器。</p>
      ) : (
        <Rows>
          {list.map((s) => (
            <div key={s.id} className="flex items-center gap-3 px-4 py-2.5 text-ui sm:px-5">
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-medium" title={s.user_agent || undefined}>
                    {deviceName(s.user_agent)}
                  </span>
                  {s.current && <span className="shrink-0 rounded border border-line-strong px-1.5 text-xs leading-[18px] text-fg-2">当前设备</span>}
                </div>
                <div className="truncate text-xs text-fg-3">
                  <span className="font-mono">{s.ip || '未知 IP'}</span> · 登录于 {fmtTime(s.created_at).slice(5, 16)} · {fmtTime(s.expires_at).slice(5, 16)} 到期
                </div>
              </div>
              {!s.current && (
                <Button size="sm" variant="danger" loading={revoke.isPending && revoke.variables === s.id} onClick={() => revoke.mutate(s.id)}>
                  注销
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
      title="设备令牌"
      desc={
        <>
          桌面客户端用令牌连接，不需要密码，长期有效。令牌在服务器上用 <code className="font-mono text-[12px]">danlu token create</code> 创建，客户端添加服务器时会自动完成。吊销后立即失效。
        </>
      }
    >
      {q.isLoading ? (
        <Skeleton className="h-10 w-full" />
      ) : q.error ? (
        <ErrorBox error={q.error} />
      ) : q.data!.tokens.length === 0 ? (
        <p className="text-ui text-fg-3">还没有设备连接过。</p>
      ) : (
        <Rows>
          {q.data!.tokens.map((t) => (
            <div key={t.id} className="flex items-center gap-3 px-4 py-2.5 text-ui sm:px-5">
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{t.name}</div>
                <div className="truncate text-xs text-fg-3">
                  创建于 {fmtTime(t.created_at).slice(0, 16)} ·{' '}
                  {t.last_used_at ? (
                    <>
                      最后使用 {fmtTime(t.last_used_at).slice(5, 16)} <span className="font-mono">{t.last_ip}</span>
                    </>
                  ) : (
                    '从未使用'
                  )}
                </div>
              </div>
              <Button size="sm" variant="danger" onClick={() => setRevoking(t)}>
                吊销
              </Button>
            </div>
          ))}
        </Rows>
      )}
      {revoking && <RevokeDialog token={revoking} onClose={() => setRevoking(null)} />}
    </Section>
  )
}

function RevokeDialog({ token: t, onClose }: { token: DeviceToken; onClose: () => void }) {
  const qc = useQueryClient()
  const revoke = useMutation({
    mutationFn: () => api.revokeToken(t.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tokens'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
      onClose()
    },
  })
  return (
    <Dialog
      title={`吊销「${t.name}」的令牌？`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>保留</Button>
          <Button variant="danger" loading={revoke.isPending} onClick={() => revoke.mutate()}>
            吊销
          </Button>
        </>
      }
    >
      <p>这台设备上的客户端会立即断开。要重新连接，在客户端里重新添加这台服务器。</p>
      <ErrorBox error={revoke.error} />
    </Dialog>
  )
}
