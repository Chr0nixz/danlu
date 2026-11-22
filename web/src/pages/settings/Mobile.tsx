import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Download, QrCode } from 'lucide-react'
import { useState } from 'react'
import { fmtTime, type NotifyStatus, type Prefs, type Settings } from '@danlu/core'
import { Button, buttonClass, ErrorBox, Field, FormRow, Select } from '../../components/ui'
import { api } from '../../lib/api'
import { t } from '../../lib/i18n'
import { KV, Section } from './shared'

type CaPlatform = 'android' | 'ios' | 'macos' | 'windows' | 'other'

const caPlatforms: CaPlatform[] = ['android', 'ios', 'macos', 'windows', 'other']

/** 平台名按当前语言取值（品牌名不翻译），不在模块级常量里冻结 */
function caPlatformName(p: CaPlatform): string {
  if (p === 'other') return t('settings.mobile.platform_other')
  return { android: 'Android', ios: 'iOS', macos: 'macOS', windows: 'Windows' }[p]
}

function detectCaPlatform(): CaPlatform {
  const ua = navigator.userAgent
  if (/android/i.test(ua)) return 'android'
  if (/iPhone|iPad|iPod/i.test(ua) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)) return 'ios'
  if (/Macintosh|Mac OS X/i.test(ua)) return 'macos'
  if (/Windows/i.test(ua)) return 'windows'
  return 'other'
}

/** 图片地址的版本号：内容对应的地址变了，版本号就跟着变 */
function shortHash(s: string) {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (Math.imul(h, 31) + s.charCodeAt(i)) | 0
  return (h >>> 0).toString(36)
}

export function Mobile({ st }: { st: Settings }) {
  const [qrVisible, setQrVisible] = useState(false)
  return (
    <>
      <Section
        title={t('settings.mobile.title')}
        desc={t('settings.mobile.desc')}
        footer={
          <Button size="sm" variant="secondary" className="ml-auto" aria-expanded={qrVisible} aria-controls="phone-access-qr" onClick={() => setQrVisible((visible) => !visible)}>
            <QrCode />
            {qrVisible ? t('settings.mobile.hide_qr') : t('settings.mobile.show_qr')}
          </Button>
        }
      >
        {qrVisible ? (
          <img
            id="phone-access-qr"
            // 换了入口要换一张图：同一个地址的图片，浏览器可能直接复用
            src={`/api/settings/qr.svg?v=${shortHash(st.security?.url ?? '')}`}
            alt={t('settings.mobile.qr_alt')}
            loading="lazy"
            decoding="async"
            referrerPolicy="no-referrer"
            className="size-48 rounded-md border border-line bg-white p-2"
          />
        ) : (
          <p id="phone-access-qr" className="text-ui text-fg-3">
            {t('settings.mobile.qr_hidden')}
          </p>
        )}
      </Section>
      {st.notify && <NotifySection status={st.notify} prefs={st.prefs} />}
      {st.ca_available && <CaInstallSection fingerprint={st.ca_fingerprint ?? ''} />}
    </>
  )
}

function NotifySection({ status, prefs }: { status: NotifyStatus; prefs?: Partial<Prefs> }) {
  const qc = useQueryClient()
  const test = useMutation({
    mutationFn: api.testNotify,
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['settings'] })
      qc.invalidateQueries({ queryKey: ['audit'] })
    },
  })
  // 推送语言是服务端行为，存在服务器上，和面板显示语言解耦；保存即生效
  const lang = useMutation({
    mutationFn: (v: 'zh-CN' | 'en') => api.updatePrefs({ notify_lang: v }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings'] }),
  })
  return (
    <Section
      title={t('settings.mobile.notify')}
      desc={status.enabled
        ? t('settings.mobile.notify_desc')
        : <>{t('settings.mobile.notify_setup_1')}<code className="font-mono text-[12px]">danlu notify setup</code>{t('settings.mobile.notify_setup_2')}</>}
      footer={
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-3">
          <span role="status" className="min-w-0 flex-1 text-ui text-fg-2">
            {test.isPending ? t('settings.mobile.notify_sending') : test.isSuccess ? t('settings.mobile.notify_sent') : status.enabled ? t('settings.mobile.notify_need_gate') : t('settings.mobile.notify_off_hint')}
          </span>
          <Button size="sm" loading={test.isPending} disabled={!status.enabled || test.isPending} aria-busy={test.isPending} onClick={() => test.mutate()}>
            {t('settings.mobile.notify_test')}
          </Button>
        </div>
      }
    >
      <dl className="-my-2 divide-y divide-line">
        <KV k={t('settings.mobile.notify_status')} v={status.enabled ? t('settings.on') : t('settings.off')} />
        {status.queue_drained !== undefined && <KV k={t('settings.mobile.queue_drained')} v={status.queue_drained ? t('settings.mobile.queue_drained_on') : t('settings.mobile.queue_drained_off')} />}
        <KV k={t('settings.mobile.last_sent')} v={status.last_sent_at ? fmtTime(status.last_sent_at) : t('settings.mobile.not_sent')} />
        <KV k={t('settings.mobile.last_error')} v={status.last_error || t('settings.mobile.none')} />
      </dl>
      {/* 旧版服务器没有 prefs，不显示这一行 */}
      {prefs && (
        <div className="-mx-4 mt-2 border-t border-line sm:-mx-5">
          <FormRow label={t('settings.mobile.notify_lang')} htmlFor="notify-lang" hint={t('settings.mobile.notify_lang_hint')}>
            <Select
              id="notify-lang"
              className="w-40!"
              value={prefs.notify_lang ?? 'zh-CN'}
              disabled={lang.isPending}
              onChange={(e) => lang.mutate(e.target.value as 'zh-CN' | 'en')}
            >
              <option value="zh-CN">{t('settings.prefs.lang_zh')}</option>
              <option value="en">{t('settings.prefs.lang_en')}</option>
            </Select>
          </FormRow>
        </div>
      )}
      <ErrorBox error={test.error} className="mt-3" />
      <ErrorBox error={lang.error} className="mt-3" />
    </Section>
  )
}

// 安装指引逐段进目录：表里存键，渲染时按当前语言取
const caStepKeys: Record<CaPlatform, Parameters<typeof t>[0][]> = {
  android: ['settings.mobile.ca_android_1', 'settings.mobile.ca_android_2', 'settings.mobile.ca_android_3'],
  ios: ['settings.mobile.ca_ios_1', 'settings.mobile.ca_ios_2', 'settings.mobile.ca_ios_3'],
  macos: ['settings.mobile.ca_macos_1', 'settings.mobile.ca_macos_2', 'settings.mobile.ca_macos_3'],
  windows: ['settings.mobile.ca_windows_1', 'settings.mobile.ca_windows_2', 'settings.mobile.ca_windows_3'],
  other: ['settings.mobile.ca_other_1', 'settings.mobile.ca_other_2'],
}

function CaInstallSection({ fingerprint }: { fingerprint: string }) {
  const [platform, setPlatform] = useState<CaPlatform>(detectCaPlatform)
  return (
    <Section title={t('settings.mobile.ca_title')} desc={t('settings.mobile.ca_desc')}>
      <div className="space-y-4">
        <a href="/api/settings/ca.crt" download="danlu-ca.crt" className={buttonClass('primary', 'sm')}>
          <Download />
          {t('settings.mobile.ca_download')}
        </a>
        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_240px]">
          <div className="min-w-0">
            <Field label={t('settings.mobile.ca_platform')}>
              <Select value={platform} onChange={(e) => setPlatform(e.target.value as CaPlatform)}>
                {caPlatforms.map((value) => (
                  <option key={value} value={value}>
                    {caPlatformName(value)}
                  </option>
                ))}
              </Select>
            </Field>
            <ol className="mt-3 list-decimal space-y-1.5 pl-5 text-ui leading-relaxed text-fg-2">
              {caStepKeys[platform].map((key) => (
                <li key={key}>{t(key)}</li>
              ))}
            </ol>
          </div>
          <div className="min-w-0 rounded-md border border-line bg-subtle px-3 py-2.5">
            <div className="text-xs text-fg-3">{t('settings.mobile.fingerprint')}</div>
            <div className="mt-1 break-all font-mono text-xs leading-relaxed text-fg">{fingerprint}</div>
          </div>
        </div>
      </div>
    </Section>
  )
}
