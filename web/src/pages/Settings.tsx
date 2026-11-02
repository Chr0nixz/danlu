import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, LogOut, Monitor, Moon, ShieldCheck, Sun } from 'lucide-react'
import { useState, type FormEvent, type ReactNode } from 'react'
import { Link } from 'react-router'
import { Button, buttonClass, Card, Dialog, ErrorBox, Field, Input, Segmented, Skeleton } from '../components/ui'
import { api } from '../lib/api'
import { fmtTime, type DeviceToken, type NotifyStatus } from '@danlu/core'
import { getTheme, setTheme, type Theme } from '../lib/theme'

const actionLabel: Record<string, string> = {
  login_ok: '登录成功',
  login_fail: '登录失败',
  login_locked: '登录被锁定',
  logout: '退出登录',
  submit: '提交任务',
  cancel: '取消任务',
  password_change: '修改密码',
  token_create: '创建设备令牌',
  token_revoke: '吊销设备令牌',
  notify_test: '测试手机推送',
}

function Section({ title, desc, children, footer }: { title: string; desc?: ReactNode; children: ReactNode; footer?: ReactNode }) {
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

function KV({ k, v }: { k: string; v: ReactNode }) {
  return (
    <div className="grid grid-cols-[96px_1fr] gap-3 py-2 text-ui sm:grid-cols-[128px_1fr]">
      <dt className="text-fg-3">{k}</dt>
      <dd className="min-w-0 break-all text-fg">{v}</dd>
    </div>
  )
}

export function Settings() {
  const s = useQuery({ queryKey: ['settings'], queryFn: api.settings, refetchInterval: 5000 })
  const audit = useQuery({ queryKey: ['audit'], queryFn: api.audit })
  const logout = useMutation({ mutationFn: api.logout, onSuccess: () => (location.href = '/login') })
  const [theme, setThemeState] = useState<Theme>(getTheme)

  if (s.isLoading) {
    return (
      <div className="mx-auto max-w-3xl space-y-4">
        <Skeleton className="h-7 w-24" />
        <Skeleton className="h-56 w-full rounded-lg" />
      </div>
    )
  }
  if (s.error) return <ErrorBox error={s.error} />
  const st = s.data!

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <h1 className="text-lg font-semibold">设置</h1>

      <Section title="服务信息" desc="这些设置在服务器上用 danlu 命令修改，改完重启服务生效。">
        <dl className="-my-2 divide-y divide-line">
          <KV k="地址" v={<span className="font-mono text-[12.5px]">{`${st.tls_mode === 'off' ? 'http' : 'https'}://${st.public_ip}:${st.port}`}</span>} />
          <KV k="HTTPS" v={{ 'local-ca': '本地 CA 证书', file: '自备证书', off: '未启用（仅限测试）' }[st.tls_mode] ?? st.tls_mode} />
          <KV k="用户名" v={st.username} />
          <KV k="版本" v={st.version} />
          <KV k="conda" v={st.conda_base ? <span className="font-mono text-[12.5px]">{st.conda_base}</span> : '未配置'} />
          <KV k="数据目录" v={<span className="font-mono text-[12.5px]">{st.data_dir}</span>} />
        </dl>
      </Section>

      <Section title="外观" desc="只保存在当前浏览器。">
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
      </Section>

      {st.notify && <NotifySection status={st.notify} />}

      {st.ca_available && (
        <Section title="安装 CA 证书" desc="装好后浏览器就不再提示证书不受信任。这个 CA 只能为本服务器的 IP 签发证书，不会影响其他网站。每台设备装一次。">
          <div className="space-y-3 text-ui">
            <a href="/api/settings/ca.crt" className={buttonClass('secondary', 'sm')}>
              <Download />
              下载 danlu-ca.crt
            </a>
            <ol className="list-decimal space-y-1.5 pl-5 text-fg-2">
              <li>
                <b className="font-medium text-fg">安卓：</b>设置 → 安全 → 加密与凭据 → 安装证书 → CA 证书，选择下载的文件。
              </li>
              <li>
                <b className="font-medium text-fg">Windows：</b>双击文件 → 安装证书 → 放入"受信任的根证书颁发机构"。
              </li>
              <li>装好后重新打开浏览器。</li>
            </ol>
            <div>
              <div className="text-xs text-fg-3">SHA-256 指纹，用来核对下载的文件</div>
              <div className="mt-1 break-all font-mono text-[11.5px] text-fg-2">{st.ca_fingerprint}</div>
            </div>
          </div>
        </Section>
      )}

      <PasswordSection />

      <TokensSection />

      <Section title="操作记录" desc="最近的登录和任务操作。">
        {audit.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : (
          <div className="-mx-4 -my-4 divide-y divide-line sm:-mx-5">
            {audit.data?.entries.map((e) => (
              <div key={e.id} className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5 px-4 py-2 text-ui sm:grid-cols-[140px_96px_1fr_auto] sm:px-5">
                <span className="text-fg-3">{fmtTime(e.ts).slice(5)}</span>
                <span className="order-first font-mono text-xs text-fg-3 sm:order-last sm:self-center">{e.ip}</span>
                <span className={e.action === 'login_fail' || e.action === 'login_locked' ? 'font-medium text-fail' : 'font-medium'}>
                  {actionLabel[e.action] ?? e.action}
                </span>
                <span className="col-span-2 min-w-0 truncate text-fg-3 sm:col-span-1">
                  <AuditDetail text={e.detail} />
                </span>
              </div>
            ))}
          </div>
        )}
      </Section>

      <Button variant="danger" className="w-full sm:w-auto" loading={logout.isPending} onClick={() => logout.mutate()}>
        <LogOut />
        退出登录
      </Button>
    </div>
  )
}

function NotifySection({ status }: { status: NotifyStatus }) {
  const qc = useQueryClient()
  const test = useMutation({
    mutationFn: api.testNotify,
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['settings'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
    },
  })
  return (
    <Section
      title="手机推送"
      desc={status.enabled
        ? '通过 ntfy 提醒任务失败、队列暂停和资源告警。30 秒内的任务失败会合并发送。'
        : <>在服务器上运行 <code className="font-mono text-[12px]">danlu notify setup</code>，扫描终端中的二维码订阅，重启服务后生效。</>}
      footer={
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-3">
          <span role="status" className="min-w-0 flex-1 text-ui text-fg-2">
            {test.isPending ? '正在发送测试推送…' : test.isSuccess ? '测试推送已发送，请在 ntfy App 中确认。' : status.enabled ? '通知链接需要手机已访问过安全入口。' : '开启后可发送测试，确认手机能够收到。'}
          </span>
          <Button size="sm" loading={test.isPending} disabled={!status.enabled || test.isPending} aria-busy={test.isPending} onClick={() => test.mutate()}>
            发一条测试
          </Button>
        </div>
      }
    >
      <dl className="-my-2 divide-y divide-line">
        <KV k="推送状态" v={status.enabled ? '已开启' : '未开启'} />
        <KV k="最近发送" v={status.last_sent_at ? fmtTime(status.last_sent_at) : '尚未发送'} />
        <KV k="最近错误" v={status.last_error || '无'} />
      </dl>
      <ErrorBox error={test.error} className="mt-3" />
    </Section>
  )
}

/** 操作记录的说明里，开头的"#7"链接到那个任务 */
function AuditDetail({ text }: { text: string }) {
  const m = text.match(/^#(\d+)(.*)$/)
  if (!m) return <>{text}</>
  return (
    <>
      <Link to={`/jobs/${m[1]}`} className="text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg hover:decoration-fg">
        #{m[1]}
      </Link>
      {m[2]}
    </>
  )
}

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
        <div className="-mx-4 -my-4 divide-y divide-line sm:-mx-5">
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
        </div>
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

function PasswordSection() {
  const [old, setOld] = useState('')
  const [next, setNext] = useState('')
  const [again, setAgain] = useState('')
  const [err, setErr] = useState<unknown>(null)
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
    setErr(null)
    if (next !== again) return setErr(new Error('两次输入的新密码不一致'))
    m.mutate()
  }
  return (
    <form onSubmit={submit}>
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
          <Field label="当前密码">
            <Input type="password" value={old} onChange={(e) => setOld(e.target.value)} autoComplete="current-password" required />
          </Field>
          <Field label="新密码">
            <Input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" minLength={8} required />
          </Field>
          <Field label="再输入一次">
            <Input type="password" value={again} onChange={(e) => setAgain(e.target.value)} autoComplete="new-password" required />
          </Field>
        </div>
        <ErrorBox error={err ?? m.error} className="mt-3" />
      </Section>
    </form>
  )
}
