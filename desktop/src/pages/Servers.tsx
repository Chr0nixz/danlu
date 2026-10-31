import { API_VERSION } from '@danlu/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUpCircle, ChevronDown, FileText, Plus, RotateCw, ShieldAlert, ShieldCheck, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { shortAgo } from '../components/GpuStrip'
import { Button, closeDialog, cx, Dialog, ErrorText, Field, inputClass, useToast } from '../components/ui'
import {
  addServer,
  removeServer,
  serviceAction,
  sshHosts,
  upgradeServer,
  upgradeSources,
  type FleetView,
  type ServerSnap,
  type ServerView,
  type ServiceAction,
  type UpgradeResult,
} from '../lib/backend'
import { isOffline } from '../lib/fleet'

/** 服务器：每台一张卡片，运维操作都在卡片上。升级先升一台，确认没问题再升其余的 */
export function Servers({ servers, view, focus }: { servers: ServerView[]; view: FleetView; focus: string | null }) {
  const [adding, setAdding] = useState(servers.length === 0)
  const [notice, setNotice] = useState<string | null>(null)
  // 第一台升级通过后记下来，才出现"升级其余 N 台"
  const [canary, setCanary] = useState<UpgradeResult & { server: string } | null>(null)
  const snaps = new Map(view.servers.map((s) => [s.name, s]))
  const behind = canary ? servers.filter((s) => s.name !== canary.server && snaps.get(s.name)?.me?.version !== canary.to) : []

  return (
    <div className="h-full min-w-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-[860px] space-y-2.5 px-1 pb-3">
        <div className="flex items-center gap-2 px-1 pt-1">
          <h1 className="text-[13px] font-semibold">服务器</h1>
          <span className="text-fg-3">{servers.length} 台</span>
          <span className="ml-auto" />
          {!adding && (
            <Button onClick={() => setAdding(true)}>
              <Plus />
              添加服务器
            </Button>
          )}
        </div>
        {adding && <AddServer existing={servers} onDone={() => setAdding(false)} canCancel={servers.length > 0} />}
        <UpgradeSourceLine />
        {canary && behind.length > 0 && <UpgradeRest canary={canary} rest={behind} onDone={() => setCanary(null)} />}
        {notice && <p className="rounded-[6px] border border-warn/40 bg-warn/10 px-3 py-2 text-warn">{notice}</p>}
        {servers.map((s) => (
          <ServerCard
            key={s.name}
            server={s}
            snap={snaps.get(s.name)}
            highlight={focus === s.name}
            onRemoved={setNotice}
            onUpgraded={(r) => r.ok && setCanary({ ...r, server: s.name })}
          />
        ))}
      </div>
    </div>
  )
}

/** 升级用的是哪个文件：安装包自带的，或者开发时 dist/ 里刚构建的 */
function UpgradeSourceLine() {
  const q = useQuery({ queryKey: ['upgrade-sources'], queryFn: upgradeSources, staleTime: 30_000 })
  if (!q.data) return null
  return (
    <p className="px-1 text-[11.5px] text-fg-3">
      {q.data.length === 0
        ? '找不到用来升级的 danlu 程序：先运行 scripts/build.ps1 构建（安装包里会自带）'
        : q.data.map((s) => `${s.arch}：${s.path}（${shortAgo(s.modified * 1000)}构建）`).join('　')}
    </p>
  )
}

const tlsText = { 'local-ca': '本地 CA，已固定证书', file: '正式证书', off: '未加密，仅限本地测试' }

function ServerCard({
  server: s,
  snap,
  highlight,
  onRemoved,
  onUpgraded,
}: {
  server: ServerView
  snap?: ServerSnap
  highlight: boolean
  onRemoved: (warning: string | null) => void
  onUpgraded: (r: UpgradeResult) => void
}) {
  const [output, setOutput] = useState<{ title: string; text: string; tone?: 'fail' } | null>(null)
  const [dialog, setDialog] = useState<null | 'entrance' | 'port' | 'cert' | 'password' | 'remove'>(null)
  const [more, setMore] = useState(false)
  const toast = useToast()
  const ref = useRef<HTMLElement>(null)
  useEffect(() => {
    if (highlight) ref.current?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [highlight])

  const action = useMutation({
    mutationFn: ({ a, value }: { a: ServiceAction; value?: string }) => serviceAction(s.name, a, value),
    onSuccess: (r) => setOutput({ title: r.command, text: r.output }),
    onError: (e) => setOutput({ title: '失败', text: String(e), tone: 'fail' }),
  })
  const run = (a: ServiceAction, value?: string) => {
    setMore(false)
    setOutput(null)
    action.mutate({ a, value })
  }

  const [progress, setProgress] = useState<string[]>([])
  const upgrade = useMutation({
    mutationFn: () => {
      setProgress([])
      setOutput(null)
      return upgradeServer(s.name, (l) => setProgress((p) => [...p, l]))
    },
    onSuccess: (r) => {
      onUpgraded(r)
      if (r.ok) toast(`${s.name} 已升级到 ${r.to}`)
      setOutput({
        title: r.ok ? `已从 ${r.from} 升级到 ${r.to}` : `升级没有通过检查：${r.problem}`,
        text: r.journal ? `服务日志最后几十行：\n${r.journal}` : '',
        tone: r.ok ? undefined : 'fail',
      })
    },
    onError: (e) => setOutput({ title: '升级失败', text: String(e), tone: 'fail' }),
  })

  const offline = snap ? isOffline(snap) : false
  const me = snap?.me
  const busy = action.isPending || upgrade.isPending

  return (
    <section
      ref={ref}
      className={cx(
        'rounded-[10px] border bg-panel shadow-panel transition-[border-color] duration-300',
        highlight ? 'border-fg-3' : 'border-line',
      )}
    >
      <div className="flex items-center gap-2 px-4 pt-3.5">
        <span className={cx('size-2 rounded-full', offline ? 'bg-fail' : snap?.online ? 'bg-ok' : 'bg-fg-3')} aria-hidden />
        <h2 className="text-[13px] font-semibold">{s.name}</h2>
        <span className="text-fg-3">
          {offline ? (snap?.updated_at ? `连不上 · 最后在线 ${shortAgo(snap.updated_at)}` : '连不上') : snap?.online ? '在线' : '连接中…'}
        </span>
        <span className="ml-auto" />
        <Button variant="ghost" className="px-1.5 text-fg-3" aria-label={`删除 ${s.name}`} title="删除" onClick={() => setDialog('remove')}>
          <Trash2 />
        </Button>
      </div>

      <dl className="grid grid-cols-[72px_1fr] gap-x-3 gap-y-1 px-4 pt-2.5">
        <Meta k="版本">
          <span className="num text-[11.5px]">{me?.version ?? '-'}</span>
          {me && me.api_version !== API_VERSION && (
            <span className="ml-2 text-warn">接口版本 {me.api_version ?? '旧'}，客户端是 {API_VERSION}，请升级</span>
          )}
        </Meta>
        <Meta k="地址">
          <span className="num selectable text-[11.5px]">{s.base_url}</span>
        </Meta>
        <Meta k="SSH">
          <span className="num selectable text-[11.5px]">{s.ssh}</span>
        </Meta>
        <Meta k="HTTPS">
          <span className="inline-flex items-center gap-1.5">
            {s.tls_mode === 'off' ? <ShieldAlert className="size-3.5 text-warn" /> : <ShieldCheck className="size-3.5 text-ok" />}
            {tlsText[s.tls_mode] ?? s.tls_mode}
            {s.ca_fingerprint && (
              <span className="num selectable truncate text-[10.5px] text-fg-3" title={s.ca_fingerprint}>
                {s.ca_fingerprint.slice(0, 23)}…
              </span>
            )}
          </span>
        </Meta>
        {snap?.error && (
          <Meta k="错误">
            <span className="selectable text-fail">{snap.error}</span>
          </Meta>
        )}
      </dl>

      <div className="flex flex-wrap items-center gap-1.5 px-4 pt-3 pb-3.5">
        <Button disabled={busy || offline} loading={upgrade.isPending} onClick={() => upgrade.mutate()} title="上传客户端自带的 danlu，替换后重启服务；训练不中断">
          <ArrowUpCircle />
          升级
        </Button>
        <Button disabled={busy} loading={action.isPending && action.variables?.a === 'restart'} onClick={() => run('restart')} title="正在跑的训练不受影响">
          <RotateCw />
          重启服务
        </Button>
        <Button disabled={busy} loading={action.isPending && action.variables?.a === 'logs'} onClick={() => run('logs')}>
          <FileText />
          服务日志
        </Button>
        <div className="relative">
          <Button variant="ghost" disabled={busy} onClick={() => setMore((m) => !m)} aria-expanded={more}>
            更多
            <ChevronDown />
          </Button>
          {more && (
            <div className="pop absolute top-full left-0 z-20 mt-1 w-44 rounded-[8px] border border-line-strong bg-panel p-1 shadow-float" onMouseLeave={() => setMore(false)}>
              {(
                [
                  ['entrance', '换安全入口'],
                  ['port', '改端口'],
                  ['cert', '重新生成证书'],
                  ['password', '改面板密码'],
                ] as const
              ).map(([k, label]) => (
                <button key={k} type="button" onClick={() => (setMore(false), setDialog(k))} className="flex h-7 w-full items-center rounded-[5px] px-2 text-left hover:bg-hover">
                  {label}
                </button>
              ))}
            </div>
          )}
        </div>
        <span className="ml-auto text-[11.5px] text-fg-3">都通过 SSH 执行 danlu 子命令</span>
      </div>

      {(progress.length > 0 || output || action.isPending) && (
        <div className="mx-3 mb-3 overflow-hidden rounded-[8px] bg-term text-[#d6d7dc]">
          <pre className="selectable max-h-72 overflow-auto px-3 py-2 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap">
            {upgrade.isPending || progress.length ? progress.join('\n') + (output ? '\n' : '') : ''}
            {action.isPending && <span className="text-[#8b8e97]">执行中…</span>}
            {output && (
              <>
                <span className={output.tone === 'fail' ? 'text-[#ff7b72]' : 'text-[#7ee787]'}>{output.title.startsWith('danlu') ? `$ ${output.title}` : output.title}</span>
                {output.text && `\n${output.text}`}
              </>
            )}
          </pre>
        </div>
      )}

      {dialog === 'remove' && <RemoveDialog server={s} onClose={() => setDialog(null)} onRemoved={onRemoved} />}
      {dialog === 'entrance' && (
        <ConfirmAction title={`换掉 ${s.name} 的安全入口？`} confirm="换新入口" onClose={() => setDialog(null)} onConfirm={() => run('entrance')}>
          旧的面板地址立即失效，浏览器书签要换成新地址（会显示在下面）。客户端用令牌访问，不受影响。
        </ConfirmAction>
      )}
      {dialog === 'cert' && (
        <ConfirmAction title={`重新生成 ${s.name} 的证书？`} confirm="重新生成" onClose={() => setDialog(null)} onConfirm={() => run('cert')}>
          会换一个新的本地 CA。手机和电脑上装过的旧 CA 要删掉重装；客户端会自动通过 SSH 读取新证书。
        </ConfirmAction>
      )}
      {dialog === 'port' && <PortDialog server={s} onClose={() => setDialog(null)} onConfirm={(p) => run('port', p)} />}
      {dialog === 'password' && <PasswordDialog server={s} onClose={() => setDialog(null)} onConfirm={(p) => run('password', p)} />}
    </section>
  )
}

function Meta({ k, children }: { k: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-fg-3">{k}</dt>
      <dd className="min-w-0 truncate">{children}</dd>
    </>
  )
}

function ConfirmAction({ title, confirm, children, onClose, onConfirm }: { title: string; confirm: string; children: ReactNode; onClose: () => void; onConfirm: () => void }) {
  return (
    <Dialog
      title={title}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>算了</Button>
          <Button
            variant="primary"
            onClick={(e) => {
              onConfirm()
              closeDialog(e)
            }}
          >
            {confirm}
          </Button>
        </>
      }
    >
      <p>{children}</p>
    </Dialog>
  )
}

function PortDialog({ server, onClose, onConfirm }: { server: ServerView; onClose: () => void; onConfirm: (port: string) => void }) {
  const [port, setPort] = useState('')
  const ok = /^\d+$/.test(port) && +port > 0 && +port < 65536
  return (
    <Dialog
      title={`改 ${server.name} 的端口`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>算了</Button>
          <Button variant="primary" disabled={!ok} onClick={(e) => (onConfirm(port), closeDialog(e))}>
            改端口并重启
          </Button>
        </>
      }
    >
      <p>改完先在防火墙或云安全组里放行新端口，否则客户端和面板都会连不上。客户端会自动更新地址。</p>
      <input className={cx(inputClass, 'num')} value={port} onChange={(e) => setPort(e.target.value.trim())} placeholder="例如 30001" autoFocus />
    </Dialog>
  )
}

function PasswordDialog({ server, onClose, onConfirm }: { server: ServerView; onClose: () => void; onConfirm: (pw: string) => void }) {
  const [pw, setPw] = useState('')
  const [again, setAgain] = useState('')
  const ok = pw.length >= 8 && pw === again
  return (
    <Dialog
      title={`改 ${server.name} 的面板密码`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>算了</Button>
          <Button variant="primary" disabled={!ok} onClick={(e) => (onConfirm(pw), closeDialog(e))}>
            改密码
          </Button>
        </>
      }
    >
      <p>只影响网页面板的登录，面板上已登录的设备会退出。客户端用令牌，不受影响。密码经 SSH 的标准输入传过去，不出现在命令行里。</p>
      <input className={inputClass} type="password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder="新密码，至少 8 位" autoComplete="new-password" autoFocus />
      <input className={inputClass} type="password" value={again} onChange={(e) => setAgain(e.target.value)} placeholder="再输入一次" autoComplete="new-password" />
      {again && pw !== again && <p className="text-fail">两次输入不一致</p>}
    </Dialog>
  )
}

function RemoveDialog({ server: s, onClose, onRemoved }: { server: ServerView; onClose: () => void; onRemoved: (w: string | null) => void }) {
  const qc = useQueryClient()
  const [revoke, setRevoke] = useState(true)
  const remove = useMutation({
    mutationFn: () => removeServer(s.name, revoke),
    onSuccess: (warning) => {
      onRemoved(warning)
      qc.invalidateQueries({ queryKey: ['servers'] })
      onClose()
    },
  })
  return (
    <Dialog
      title={`删除 ${s.name}？`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>保留</Button>
          <Button variant="danger" loading={remove.isPending} onClick={() => remove.mutate()}>
            删除
          </Button>
        </>
      }
    >
      <p>只是从客户端里移除，服务器上的 danlu 和任务都不受影响。</p>
      <label className="flex items-center gap-2 text-fg">
        <input type="checkbox" checked={revoke} onChange={(e) => setRevoke(e.target.checked)} />
        同时通过 SSH 吊销这台电脑的令牌
      </label>
      <ErrorText error={remove.error} />
    </Dialog>
  )
}

/** 第一台升级通过后，一键升级其余的；一台一台来，任何一台没通过就停下 */
function UpgradeRest({ canary, rest, onDone }: { canary: UpgradeResult & { server: string }; rest: ServerView[]; onDone: () => void }) {
  const [log, setLog] = useState<string[]>([])
  const toast = useToast()
  const go = useMutation({
    mutationFn: async () => {
      for (const s of rest) {
        setLog((l) => [...l, `== ${s.name}`])
        const r = await upgradeServer(s.name, (line) => setLog((l) => [...l, line]))
        if (!r.ok) throw new Error(`${s.name} 没有通过检查：${r.problem}${r.journal ? `\n${r.journal}` : ''}`)
      }
    },
    onSuccess: () => {
      toast(`全部升级到 ${canary.to}`)
      onDone()
    },
  })
  return (
    <div className="rounded-[10px] border border-line bg-panel px-4 py-3 shadow-panel">
      <div className="flex items-center gap-3">
        <ArrowUpCircle className="size-4 text-ok" />
        <p className="flex-1">
          {canary.server} 已升级到 <span className="num">{canary.to}</span>，新版本在服务、在跑的任务都已接管。还有 {rest.length} 台是旧版本：
          {rest.map((s) => s.name).join('、')}
        </p>
        <Button variant="primary" loading={go.isPending} onClick={() => go.mutate()}>
          升级其余 {rest.length} 台
        </Button>
      </div>
      {(log.length > 0 || go.error) && (
        <pre className="selectable mt-2.5 max-h-60 overflow-auto rounded-[8px] bg-term px-3 py-2 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap text-[#d6d7dc]">
          {log.join('\n')}
          {go.error && <span className="text-[#ff7b72]">{`\n${go.error.message}`}</span>}
        </pre>
      )}
    </div>
  )
}

function AddServer({ existing, onDone, canCancel }: { existing: ServerView[]; onDone: () => void; canCancel: boolean }) {
  const qc = useQueryClient()
  const hosts = useQuery({ queryKey: ['ssh-hosts'], queryFn: sshHosts })
  const [target, setTarget] = useState('')
  const [name, setName] = useState('')
  const add = useMutation({
    mutationFn: () => addServer(target.trim(), (name || defaultName(target)).trim()),
    onSuccess: () => {
      setTarget('')
      setName('')
      qc.invalidateQueries({ queryKey: ['servers'] })
      onDone()
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (target.trim()) add.mutate()
  }
  const unused = (hosts.data ?? []).filter((h) => !existing.some((s) => s.ssh === h))
  return (
    <form onSubmit={submit} className="rounded-[10px] border border-line bg-panel px-4 py-3.5 shadow-panel">
      <h2 className="text-[13px] font-semibold">添加服务器</h2>
      <p className="mt-1 text-fg-3">服务器上要已经装好 danlu。客户端用系统的 ssh 登录，读取地址和证书，再领一个设备令牌，不用输入面板密码。</p>
      <div className="mt-3 grid grid-cols-[1fr_160px_auto_auto] items-end gap-2">
        <Field label="SSH 主机" hint="~/.ssh/config 里的主机名，或者 user@host">
          <input className={cx(inputClass, 'num')} list="ssh-hosts" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="gpu1" autoFocus spellCheck={false} />
        </Field>
        <datalist id="ssh-hosts">
          {unused.map((h) => (
            <option key={h} value={h} />
          ))}
        </datalist>
        <Field label="名字">
          <input className={cx(inputClass, 'num')} value={name} onChange={(e) => setName(e.target.value)} placeholder={defaultName(target) || 'gpu1'} spellCheck={false} />
        </Field>
        {canCancel && <Button onClick={onDone}>算了</Button>}
        <Button type="submit" variant="primary" loading={add.isPending} disabled={!target.trim()}>
          添加
        </Button>
      </div>
      {add.isPending && <p className="mt-2 text-fg-3">正在通过 SSH 读取信息、领取令牌…</p>}
      <ErrorText error={add.error} className="mt-2" />
    </form>
  )
}

/** 默认名字：SSH 别名，或者 user@host 里的 host */
function defaultName(target: string): string {
  return target.trim().replace(/^.*@/, '').replace(/[^A-Za-z0-9._-]/g, '-').slice(0, 32)
}
