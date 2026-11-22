import { fmtTime, type DeviceToken } from '@danlu/core'
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'
import { Button, closeDialog, cx, Dialog, ErrorText, Field, inputClass, Kbd, useToast } from '../components/ui'
import { apiFor, appVersion, getPrefs, setPrefs, type Prefs, type ServerView } from '../lib/backend'
import { t } from '../lib/i18n'
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
        <h1 className="px-1 pt-1 text-[13px] font-semibold">{t('settings.title')}</h1>
        {p && (
          <Section title={t('settings.general')}>
            <Toggle label={t('settings.notify')} desc={t('settings.notify_desc')} checked={p.notify} onChange={(notify) => update({ notify })} />
            <Toggle label={t('settings.autostart')} desc={t('settings.autostart_desc')} checked={p.autostart} onChange={(autostart) => update({ autostart })} />
            <Field label={t('settings.lang')} hint={t('settings.lang_desc')}>
              <select className={cx(inputClass, 'w-40')} value={p.lang} onChange={(e) => update({ lang: e.target.value })}>
                <option value="auto">{t('settings.lang_auto')}</option>
                <option value="zh-CN">{t('settings.lang_zh')}</option>
                <option value="en">{t('settings.lang_en')}</option>
              </select>
            </Field>
            <ShortcutRow value={p.shortcut} onSave={(shortcut) => update({ shortcut })} />
          </Section>
        )}
        <Devices servers={servers} />
        <Section title={t('settings.about')}>
          <dl className="grid grid-cols-[72px_1fr] gap-x-3 gap-y-1">
            <dt className="text-fg-3">{t('settings.version')}</dt>
            <dd className="num">{version.data ?? '-'}</dd>
            <dt className="text-fg-3">{t('settings.data')}</dt>
            <dd className="text-fg-2">{t('settings.data_desc', { store: isMac ? t('settings.keychain') : t('settings.win_cred') })}</dd>
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
        <span className="block font-medium">{t('settings.shortcut_label')}</span>
        <span className="block text-fg-3">{t('settings.shortcut_desc')}</span>
      </span>
      {recording ? <span className="text-fg-2">{t('settings.shortcut_recording')}</span> : <Kbd className="h-6 px-2 text-[11.5px]">{shortcutLabel(value)}</Kbd>}
      <Button onClick={() => setRecording((r) => !r)}>{recording ? t('common.cancel') : t('settings.shortcut_change')}</Button>
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
    <Section title={t('settings.devices')} desc={t('settings.devices_desc')}>
      <div className="-mx-1 space-y-2.5">
        {servers.map((s, i) => {
          const r = results[i]
          return (
            <div key={s.name}>
              <div className="px-1 pb-1 text-[11.5px] font-medium text-fg-2">{s.name}</div>
              {r?.error ? (
                <ErrorText error={r.error} className="px-1" />
              ) : (
                r?.data?.tokens.map((tok) => (
                  <div key={tok.id} className="flex h-8 items-center gap-3 rounded-[6px] px-1 hover:bg-hover">
                    <span className="min-w-0 flex-1 truncate">
                      {tok.name}
                      {tok.id === s.token_id && <span className="ml-2 text-[11px] text-fg-3">{t('settings.this_computer')}</span>}
                    </span>
                    <span className="num text-[11px] text-fg-3">
                      {tok.last_used_at ? t('settings.last_used', { time: fmtTime(tok.last_used_at).slice(5, 16), ip: tok.last_ip }) : t('settings.never_used')}
                    </span>
                    {tok.id !== s.token_id && (
                      <Button variant="ghost" className="text-fail" onClick={() => setRevoking({ server: s, token: tok })}>
                        {t('settings.revoke')}
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
      title={t('settings.revoke_title', { token: token.name, server: server.name })}
      onClose={onClose}
      footer={
        <>
          <Button onClick={closeDialog}>{t('servers.remove_keep')}</Button>
          <Button variant="danger" loading={revoke.isPending} onClick={() => revoke.mutate()}>
            {t('settings.revoke')}
          </Button>
        </>
      }
    >
      <p>{t('settings.revoke_body')}</p>
      <ErrorText error={revoke.error} />
    </Dialog>
  )
}
