import { useQuery } from '@tanstack/react-query'
import { Activity, CircleCheck, CircleX, RefreshCw, TriangleAlert } from 'lucide-react'
import type { Settings } from '@danlu/core'
import { Button, ErrorBox } from '../../components/ui'
import { api } from '../../lib/api'
import { KV, Section } from './shared'

const doctorLevel: Record<string, string> = { ok: '通过', warn: '警告', fail: '失败' }

export function Overview({ st }: { st: Settings }) {
  const doctor = useQuery({ queryKey: ['doctor'], queryFn: api.doctor, enabled: false })
  return (
    <>
      <Section title="服务信息" desc="这些项在服务器上用 danlu 命令修改，改完重启服务生效。调度、告警和数据保留可以直接在这里的其他分组里改。">
        <dl className="-my-2 divide-y divide-line">
          <KV k="地址" v={<span className="font-mono text-[12.5px]">{`${st.tls_mode === 'off' ? 'http' : 'https'}://${st.public_ip}:${st.port}`}</span>} />
          <KV k="HTTPS" v={{ 'local-ca': '本地 CA 证书', file: '自备证书', off: '未启用（仅限测试）' }[st.tls_mode] ?? st.tls_mode} />
          <KV k="用户名" v={st.username} />
          <KV k="版本" v={st.version} />
          <KV k="conda" v={st.conda_base ? <span className="font-mono text-[12.5px]">{st.conda_base}</span> : '未配置'} />
          <KV k="数据目录" v={<span className="font-mono text-[12.5px]">{st.data_dir}</span>} />
          <KV k="网页终端" v={st.terminal_enabled ? '已开启' : '已关闭（config.toml 的 [terminal] enabled）'} />
        </dl>
      </Section>

      <Section
        title="机器体检"
        footer={
          <Button size="sm" variant="secondary" className="ml-auto" loading={doctor.isFetching} onClick={() => doctor.refetch()}>
            <RefreshCw />
            {doctor.data ? '重新体检' : '运行体检'}
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
                  <span className="sr-only">{doctorLevel[check.level]}：</span>
                  {check.title}
                </div>
                <div className="min-w-0 break-all text-ui text-fg-2">
                  <div>{check.detail}</div>
                  {check.fix && <div className="mt-0.5 text-xs text-fg-3">建议：{check.fix}</div>}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="flex items-center gap-2 text-ui text-fg-3">
            <Activity className="size-4" />
            尚未运行
          </div>
        )}
      </Section>
    </>
  )
}
