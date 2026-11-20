import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Download, QrCode } from 'lucide-react'
import { useState } from 'react'
import { fmtTime, type NotifyStatus, type Settings } from '@danlu/core'
import { Button, buttonClass, ErrorBox, Field, Select } from '../../components/ui'
import { api } from '../../lib/api'
import { KV, Section } from './shared'

type CaPlatform = 'android' | 'ios' | 'macos' | 'windows' | 'other'

const caPlatformNames: Record<CaPlatform, string> = {
  android: 'Android',
  ios: 'iOS',
  macos: 'macOS',
  windows: 'Windows',
  other: 'Linux / 其他',
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
        title="在手机上打开"
        desc="二维码包含完整安全入口地址。手机先装好本机 CA（从这台电脑把 danlu-ca.crt 传过去），再扫码打开；仍提示证书不受信任时不要登录。"
        footer={
          <Button size="sm" variant="secondary" className="ml-auto" aria-expanded={qrVisible} aria-controls="phone-access-qr" onClick={() => setQrVisible((visible) => !visible)}>
            <QrCode />
            {qrVisible ? '隐藏二维码' : '显示二维码'}
          </Button>
        }
      >
        {qrVisible ? (
          <img
            id="phone-access-qr"
            // 换了入口要换一张图：同一个地址的图片，浏览器可能直接复用
            src={`/api/settings/qr.svg?v=${shortHash(st.security?.url ?? '')}`}
            alt="丹炉安全入口地址二维码"
            loading="lazy"
            decoding="async"
            referrerPolicy="no-referrer"
            className="size-48 rounded-md border border-line bg-white p-2"
          />
        ) : (
          <p id="phone-access-qr" className="text-ui text-fg-3">
            二维码默认不显示，免得在别人看得到屏幕时露出入口地址。
          </p>
        )}
      </Section>
      {st.notify && <NotifySection status={st.notify} />}
      {st.ca_available && <CaInstallSection fingerprint={st.ca_fingerprint ?? ''} />}
    </>
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
        ? '通过 ntfy 发送任务失败、已勾选的结束提醒、队列暂停和资源告警。30 秒窗口内的结束提醒会合并。'
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
        {status.queue_drained !== undefined && <KV k="跑完汇总" v={status.queue_drained ? '已开启 · 至少两个任务' : '已关闭'} />}
        <KV k="最近发送" v={status.last_sent_at ? fmtTime(status.last_sent_at) : '尚未发送'} />
        <KV k="最近错误" v={status.last_error || '无'} />
      </dl>
      <ErrorBox error={test.error} className="mt-3" />
    </Section>
  )
}

const caSteps: Record<CaPlatform, string[]> = {
  android: [
    '下载证书后打开“设置 → 安全与隐私 → 更多安全设置 → 加密与凭据 → 安装证书 → CA 证书”。不同品牌的菜单名称可能略有差异。',
    '选择 danlu-ca.crt 并确认系统提示；设备需要先设置锁屏密码。',
    '强制关闭并重新打开 Chrome，再访问面板。',
  ],
  ios: [
    '下载证书并在系统提示时打开“设置”；也可在“设置 → 已下载描述文件”中找到它。',
    '点“安装”完成描述文件安装，然后打开“设置 → 通用 → 关于本机 → 证书信任设置”。',
    '为 Danlu CA 打开完全信任，再重新打开 Safari 或 Chrome。',
  ],
  macos: [
    '打开“钥匙串访问”，选择“文件 → 导入项目”，导入 danlu-ca.crt 到“登录”钥匙串。',
    '双击 Danlu CA 证书，展开“信任”，将“使用此证书时”设为“始终信任”，并确认系统授权。',
    '重新打开 Safari、Chrome 或 Edge。',
  ],
  windows: [
    '双击 danlu-ca.crt，选择“当前用户”并继续。',
    '选择“将所有的证书都放入下列存储”，指定“受信任的根证书颁发机构”，完成导入。',
    '关闭并重新打开 Chrome、Edge 或 Firefox。',
  ],
  other: [
    '下载 danlu-ca.crt，并按系统或浏览器文档将它导入受信任的根 CA 存储。',
    '确认导入的证书指纹和 danlu info 显示的一致，然后重新打开浏览器。',
  ],
}

function CaInstallSection({ fingerprint }: { fingerprint: string }) {
  const [platform, setPlatform] = useState<CaPlatform>(detectCaPlatform)
  return (
    <Section title="安装 CA 证书" desc="本机 CA 只为这台服务器的 IP 签发证书，每台设备安装一次。导入前和服务器上 danlu info 显示的 CA 指纹核对，对不上不要安装；还没装 CA 的设备，从已装好的电脑传过去，或用 ssh <服务器> danlu ca > danlu-ca.crt 取回。">
      <div className="space-y-4">
        <a href="/api/settings/ca.crt" download="danlu-ca.crt" className={buttonClass('primary', 'sm')}>
          <Download />
          下载 danlu-ca.crt
        </a>
        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_240px]">
          <div className="min-w-0">
            <Field label="设备平台">
              <Select value={platform} onChange={(e) => setPlatform(e.target.value as CaPlatform)}>
                {Object.entries(caPlatformNames).map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </Select>
            </Field>
            <ol className="mt-3 list-decimal space-y-1.5 pl-5 text-ui leading-relaxed text-fg-2">
              {caSteps[platform].map((step) => (
                <li key={step}>{step}</li>
              ))}
            </ol>
          </div>
          <div className="min-w-0 rounded-md border border-line bg-subtle px-3 py-2.5">
            <div className="text-xs text-fg-3">SHA-256 指纹</div>
            <div className="mt-1 break-all font-mono text-xs leading-relaxed text-fg">{fingerprint}</div>
          </div>
        </div>
      </div>
    </Section>
  )
}
