import { useMutation } from '@tanstack/react-query'
import type { StrictReason, StrictStatus } from '@danlu/core'
import { Button, cx, ErrorBox } from '../../components/ui'
import { api } from '../../lib/api'
import { t } from '../../lib/i18n'
import { useCopy } from '../../lib/useCopy'
import { KV, Section, useSettingsCache } from './shared'

function reasonText(r?: StrictReason): string {
  switch (r) {
    case 'not_installed':
      return t('settings.strict.reason_not_installed')
    case 'no_sudo':
      return t('settings.strict.reason_no_sudo')
    case 'protocol':
      return t('settings.strict.reason_protocol')
    case 'unsupported':
      return t('settings.strict.reason_unsupported')
    case 'checking':
      return t('settings.strict.reason_checking')
    default:
      return t('settings.strict.reason_failed')
  }
}

/**
 * 严格限卡：开关、助手状态、安装命令。打开前服务器经 sudo 查一次助手，查不过返回 409，原因照实显示。
 * 状态只用两种字色：可用是 ok，不可用是 warn；没装不算异常，用次要字色
 */
export function StrictForm({ strict }: { strict: StrictStatus }) {
  const cache = useSettingsCache()
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.updateStrict(enabled),
    onSuccess: (s) => {
      cache.merge({ strict: s })
      cache.refresh()
    },
    onError: () => cache.refresh(),
  })
  const checked = strict.reason !== 'checking' || strict.ok
  let status
  if (strict.ok) status = <span className="text-ok">{t('settings.strict.ok', { version: strict.helper_version ?? '' })}</span>
  else if (!strict.installed) status = <span className="text-fg-2">{t('settings.strict.not_installed')}</span>
  else if (!checked) status = <span className="text-fg-2">{t('settings.strict.installed_unchecked')}</span>
  else
    status = (
      <span className="text-warn">
        {t('settings.strict.unavailable', { reason: reasonText(strict.reason) })}
        {strict.detail && <span className="mt-0.5 block break-words text-xs text-fg-3">{strict.detail}</span>}
      </span>
    )
  const needInstall = !strict.installed || strict.reason === 'no_sudo' || strict.reason === 'protocol'
  return (
    <Section
      title={t('settings.strict.title')}
      desc={t('settings.strict.desc')}
      footer={
        <>
          <span className={cx('min-w-0 flex-1 text-ui', strict.enabled ? 'text-fg' : 'text-fg-3')}>{strict.enabled ? t('settings.strict.on') : t('settings.strict.off')}</span>
          <Button
            size="sm"
            variant={strict.enabled ? 'secondary' : 'primary'}
            loading={toggle.isPending}
            disabled={!strict.enabled && !strict.installed}
            onClick={() => toggle.mutate(!strict.enabled)}
          >
            {strict.enabled ? t('settings.strict.disable') : t('settings.strict.enable')}
          </Button>
        </>
      }
    >
      <dl>
        <KV k={t('settings.strict.status')} v={status} />
        {needInstall && <KV k={t('settings.strict.install')} v={<InstallCmd cmd={strict.install_cmd} />} />}
      </dl>
      {strict.enabled && <p className="mt-2 text-xs leading-relaxed text-fg-3">{t('settings.strict.enabled_note')}</p>}
      {toggle.error && <ErrorBox error={toggle.error} className="mt-3" />}
    </Section>
  )
}

function InstallCmd({ cmd }: { cmd: string }) {
  const c = useCopy(cmd)
  return (
    <div>
      <div className="flex min-w-0 items-start gap-2">
        <code className="min-w-0 flex-1 rounded-md bg-subtle px-2 py-1 font-mono text-xs break-all text-fg">{cmd}</code>
        <Button variant="ghost" size="sm" className={cx('shrink-0 text-xs', c.state === 'fail' ? 'text-fail' : 'text-fg-3')} onClick={c.copy} title={c.title}>
          {c.icon}
          {t('settings.strict.copy')}
          {c.status}
        </Button>
      </div>
      <p className="mt-1 text-xs leading-relaxed text-fg-3">{t('settings.strict.install_hint')}</p>
    </div>
  )
}
