import { useQuery } from '@tanstack/react-query'
import { Activity, CircleCheck, CircleX, RefreshCw, TriangleAlert } from 'lucide-react'
import type { Settings } from '@danlu/core'
import { Button, ErrorBox } from '../../components/ui'
import { api } from '../../lib/api'
import { t } from '../../lib/i18n'
import { KV, Section } from './shared'

/** 体检结论（读屏用）：按当前语言取值，不在模块级常量里冻结 */
function doctorLevelText(level: string): string {
  if (level === 'ok') return t('settings.doctor.ok')
  if (level === 'warn') return t('settings.doctor.warn')
  return t('settings.doctor.fail')
}

// 检查标题按 check ID 翻译（internal/doctor 的码表）；未知 ID 回退服务器标题。
// Detail/Fix 是服务器诊断文本，保持原样。
const doctorTitleKeys: Record<string, Parameters<typeof t>[0]> = {
  gpu: 'settings.doctor.gpu',
  conda: 'settings.doctor.conda',
  data_disk: 'settings.doctor.data_disk',
  output_disk: 'settings.doctor.output_disk',
  server_cert: 'settings.doctor.server_cert',
  ca_cert: 'settings.doctor.ca_cert',
  cert_ip: 'settings.doctor.cert_ip',
  port: 'settings.doctor.port',
  version: 'settings.doctor.version',
  service_unit: 'settings.doctor.service_unit',
  service_active: 'settings.doctor.service_active',
  service_kill_mode: 'settings.doctor.service_kill_mode',
  service_restart: 'settings.doctor.service_restart',
  service_oom_policy: 'settings.doctor.service_oom_policy',
  service_load: 'settings.doctor.service_load',
  linger: 'settings.doctor.linger',
  time_sync: 'settings.doctor.time_sync',
}

export function Overview({ st }: { st: Settings }) {
  const doctor = useQuery({ queryKey: ['doctor'], queryFn: api.doctor, enabled: false })
  return (
    <>
      <Section title={t('settings.overview.title')} desc={t('settings.overview.desc')}>
        <dl className="-my-2 divide-y divide-line">
          <KV k={t('settings.overview.address')} v={<span className="font-mono text-[12.5px]">{`${st.tls_mode === 'off' ? 'http' : 'https'}://${st.public_ip}:${st.port}`}</span>} />
          <KV k="HTTPS" v={{ 'local-ca': t('settings.overview.tls_local_ca'), file: t('settings.overview.tls_file'), off: t('settings.overview.tls_off') }[st.tls_mode] ?? st.tls_mode} />
          <KV k={t('settings.overview.username')} v={st.username} />
          <KV k={t('settings.overview.version')} v={st.version} />
          <KV k="conda" v={st.conda_base ? <span className="font-mono text-[12.5px]">{st.conda_base}</span> : t('settings.overview.not_set')} />
          <KV k={t('settings.overview.data_dir')} v={<span className="font-mono text-[12.5px]">{st.data_dir}</span>} />
          <KV k={t('settings.overview.terminal')} v={st.terminal_enabled ? t('settings.on') : t('settings.overview.terminal_off')} />
        </dl>
      </Section>

      <Section
        title={t('settings.overview.doctor_title')}
        footer={
          <Button size="sm" variant="secondary" className="ml-auto" loading={doctor.isFetching} onClick={() => doctor.refetch()}>
            <RefreshCw />
            {doctor.data ? t('settings.overview.doctor_rerun') : t('settings.overview.doctor_run')}
          </Button>
        }
      >
        {doctor.error ? (
          <ErrorBox error={doctor.error} />
        ) : doctor.data ? (
          <div className="-my-2 divide-y divide-line">
            {doctor.data.checks.map((check) => (
              <div key={check.id} className="grid gap-x-3 gap-y-0.5 py-2 sm:grid-cols-[150px_minmax(0,1fr)]">
                <div className="flex items-center gap-2 text-ui font-medium">
                  {check.level === 'ok' ? <CircleCheck className="size-4 text-ok" aria-hidden /> : check.level === 'warn' ? <TriangleAlert className="size-4 text-warn" aria-hidden /> : <CircleX className="size-4 text-fail" aria-hidden />}
                  {/* 结论只靠图标的形状和颜色表达，读屏另外读出来 */}
                  <span className="sr-only">{doctorLevelText(check.level)}</span>
                  {doctorTitleKeys[check.id] ? t(doctorTitleKeys[check.id]) : check.title}
                </div>
                <div className="min-w-0 break-all text-ui text-fg-2">
                  <div>{check.detail}</div>
                  {check.fix && <div className="mt-0.5 text-xs text-fg-3">{t('settings.overview.doctor_fix')}{check.fix}</div>}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="flex items-center gap-2 text-ui text-fg-3">
            <Activity className="size-4" />
            {t('settings.overview.doctor_idle')}
          </div>
        )}
      </Section>
    </>
  )
}
