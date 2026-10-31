import { fmtTime, type DeviceToken } from '@danlu/core'
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'
import { Button, closeDialog, cx, Dialog, ErrorText, Kbd, useToast } from '../components/ui'
import { apiFor, appVersion, getPrefs, setPrefs, type Prefs, type ServerView } from '../lib/backend'
import { isMac, shortcutLabel } from '../lib/platform'

function Section({ title, desc, children }: { title: string; desc?: ReactNode; children: ReactNode }) {
  return (
    <section className="rounded-[10px] border border-line bg-panel px-4 py-3.5 shadow-panel">
      <h2 className="text-[13px] font-semibold">{title}</h2>
      {desc && <p className="mt-0.5 text-fg-3">{desc}</p>}
      <div className="mt-3">{children}</div>
    </section>
  )
}

function Toggle({ label, desc, checked, onChange }: { label: string; desc: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-3 py-1.5">
      <span className="flex-1">
        <span className="block font-medium">{label}</span>
        <span className="block text-fg-3">{desc}</span>
      </span>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={cx('relative h-[18px] w-8 shrink-0 rounded-full transition-colors duration-[120ms]', checked ? 'bg-primary' : 'bg-line-strong')}
      >
        <span className={cx('absolute top-[2px] size-[14px] rounded-full bg-panel shadow-sm transition-[left] duration-[120ms]', checked ? 'left-[16px]' : 'left-[2px]')} />
      </button>
    </label>
  )
}

/** 设置：通知、开机自启、全局快捷键、已登录的设备、关于 */
export function Settings({ servers }: { servers: ServerView[] }) {
  const qc = useQueryClient()
  const toast = useToast()
  const prefs = useQuery({ queryKey: ['prefs'], queryFn: getPrefs })
  const version = useQuery({ queryKey: ['version'], queryFn: appVersion, staleTime: Infinity })
  const save = useMutation({
    mutationFn: (p: Prefs) => setPrefs(p),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['prefs'] }),
    onError: (e) => toast(String(e), 'fail'),
  })
  const p = prefs.data
  const update = (patch: Partial<Prefs>) => p && save.mutate({ ...p, ...patch })

  return (
    <div className="h-full min-w-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-[720px] space-y-2.5 px-1 pb-3">
        <h1 className="px-1 pt-1 text-[13px] font-semibold">设置</h1>
        {p && (
          <Section title="常规">
            <Toggle label="系统通知" desc="任务失败、服务器连不上、外部进程影响到你的任务时通知" checked={p.notify} onChange={(notify) => update({ notify })} />
            <Toggle label="开机自启" desc="登录系统后自动在托盘里运行" checked={p.autostart} onChange={(autostart) => update({ autostart })} />
            <ShortcutRow value={p.shortcut} onSave={(shortcut) => update({ shortcut })} />
          </Section>
        )}
        <Devices servers={servers} />
        <Section title="关于">
          <dl className="grid grid-cols-[72px_1fr] gap-x-3 gap-y-1">
            <dt className="text-fg-3">版本</dt>
            <dd className="num">{version.data ?? '-'}</dd>
            <dt className="text-fg-3">数据</dt>
            <dd className="text-fg-2">服务器列表和偏好存在本机，设备令牌存在系统凭据库（{isMac ? '钥匙串' : 'Windows 凭据管理器'}）</dd>
          </dl>
        </Section>
      </div>
    </div>
  )
}

/** 全局快捷键：点"修改"后直接按下想要的组合键 */
function ShortcutRow({ value, onSave }: { value: string; onSave: (v: string) => void }) {
  const [recording, setRecording] = useState(false)
  useEffect(() => {
    if (!recording) return
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault()
      e.stopPropagation()
      if (e.key === 'Escape') return setRecording(false)
      if (['Control', 'Alt', 'Shift', 'Meta'].includes(e.key)) return
      const mods = [e.ctrlKey && 'Ctrl', e.metaKey && 'Cmd', e.altKey && 'Alt', e.shiftKey && 'Shift'].filter(Boolean)
      if (mods.length === 0) return // 全局快捷键必须带修饰键
      const key = e.code.startsWith('Key') ? e.code.slice(3) : e.code.startsWith('Digit') ? e.code.slice(5) : e.code
      setRecording(false)
      onSave([...mods, key].join('+'))
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [recording, onSave])
  return (
    <div className="flex items-center gap-3 py-1.5">
      <span className="flex-1">
        <span className="block font-medium">呼出迷你总览</span>
        <span className="block text-fg-3">在任何地方按下，在托盘旁弹出各台的空卡和运行中的任务</span>
      </span>
      {recording ? <span className="text-fg-2">按下新的组合键，Esc 取消</span> : <Kbd className="h-6 px-2 text-[11.5px]">{shortcutLabel(value)}</Kbd>}
      <Button onClick={() => setRecording((r) => !r)}>{recording ? '取消' : '修改'}</Button>
    </div>
  )
}

/** 已登录的设备：每台服务器上的设备令牌。电脑丢了的时候，也可以用手机打开面板吊销 */
function Devices({ servers }: { servers: ServerView[] }) {
  const results = useQueries({
    queries: servers.map((s) => ({ queryKey: ['tokens', s.name], queryFn: () => apiFor(s.name).tokens() })),
  })
  const [revoking, setRevoking] = useState<{ server: ServerView; token: DeviceToken } | null>(null)
  return (
    <Section title="已登录的设备" desc="每台服务器上的设备令牌。吊销后那台设备上的客户端立即断开。">
      <div className="-mx-1 space-y-2.5">
        {servers.map((s, i) => {
          const r = results[i]
          return (
            <div key={s.name}>
              <div className="px-1 pb-1 text-[11.5px] font-medium text-fg-2">{s.name}</div>
              {r?.error ? (
                <ErrorText error={r.error} className="px-1" />
              ) : (
                r?.data?.tokens.map((t) => (
                  <div key={t.id} className="flex h-8 items-center gap-3 rounded-[6px] px-1 hover:bg-hover">
                    <span className="min-w-0 flex-1 truncate">
                      {t.name}
                      {t.id === s.token_id && <span className="ml-2 text-[11px] text-fg-3">这台电脑</span>}
                    </span>
                    <span className="num text-[11px] text-fg-3">
                      {t.last_used_at ? `最后使用 ${fmtTime(t.last_used_at).slice(5, 16)} · ${t.last_ip}` : '从未使用'}
                    </span>
                    {t.id !== s.token_id && (
                      <Button variant="ghost" className="text-fail" onClick={() => setRevoking({ server: s, token: t })}>
                        吊销
                      </Button>
                    )}
                  </div>
                ))
              )}
            </div>
          )
        })}
      </div>
      {revoking && <RevokeDialog {...revoking} onClose={() => setRevoking(null)} />}
    </Section>
  )
}

function RevokeDialog({ server, token, onClose }: { server: ServerView; token: DeviceToken; onClose: () => void }) {
  const qc = useQueryClient()
  const revoke = useMutation({
    mutationFn: () => apiFor(server.name).revokeToken(token.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tokens', server.name] })
      onClose()
    },
  })
  return (
    <Dialog
      title={`吊销「${token.name}」在 ${server.name} 上的令牌？`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>保留</Button>
          <Button variant="danger" loading={revoke.isPending} onClick={() => revoke.mutate()}>
            吊销
          </Button>
        </>
      }
    >
      <p>那台设备上的客户端会立即断开，要重新连接得重新添加这台服务器。</p>
      <ErrorText error={revoke.error} />
    </Dialog>
  )
}
