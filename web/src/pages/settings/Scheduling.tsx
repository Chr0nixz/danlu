import { useQuery } from '@tanstack/react-query'
import { TriangleAlert } from 'lucide-react'
import type { RuntimeKey, RuntimeSettings, Settings } from '@danlu/core'
import { api } from '../../lib/api'
import { t } from '../../lib/i18n'
import { RuntimeForm, type RowMeta } from './RuntimeForm'
import { Section } from './shared'
import { StrictForm } from './StrictForm'

/** 行标签和提示按当前语言取值：是函数不是模块级常量表，切换语言后重渲染即生效 */
function rowMeta(): Record<RuntimeKey, RowMeta> {
  return {
    fail_fast: {
      label: t('settings.sched.fail_fast'),
      hint: t('settings.sched.fail_fast_hint'),
    },
    kill_grace: {
      label: t('settings.sched.kill_grace'),
      hint: t('settings.sched.kill_grace_hint'),
    },
    unknown_gpu_memory_mb: {
      label: t('settings.sched.unknown_gpu_memory'),
      hint: t('settings.sched.unknown_gpu_memory_hint'),
    },
    disk_min_gb: {
      label: t('settings.sched.disk_min'),
      hint: t('settings.sched.disk_min_hint'),
    },
    disk_warn_gb: {
      label: t('settings.sched.disk_warn'),
      hint: t('settings.sched.disk_warn_hint'),
    },
    silence_alert: {
      label: t('settings.sched.silence_alert'),
      hint: t('settings.sched.silence_alert_hint'),
    },
    snapshot_max_file_mb: {
      label: t('settings.sched.snapshot_file'),
      hint: t('settings.sched.snapshot_file_hint'),
    },
    snapshot_max_total_mb: {
      label: t('settings.sched.snapshot_total'),
      hint: t('settings.sched.snapshot_total_hint'),
    },
    terminal_session_lifetime: {
      label: t('settings.sched.terminal_lifetime'),
      hint: t('settings.sched.terminal_lifetime_hint'),
    },
  }
}

const gib = (bytes: number) => (bytes / 1024 ** 3).toFixed(1)

export function Scheduling({ st }: { st: Settings }) {
  if (!st.runtime) return <Section title={t('settings.group_scheduling')}>{t('settings.old_server')}</Section>
  const items: RuntimeSettings = st.runtime
  const meta = rowMeta()
  const update = (patch: Parameters<typeof api.updateRuntime>[0]) => api.updateRuntime(patch).then((r) => r.runtime)
  return (
    <>
      <RuntimeForm
        title={t('settings.sched.title')}
        desc={t('settings.sched.desc')}
        items={items}
        keys={['fail_fast', 'kill_grace', 'unknown_gpu_memory_mb']}
        meta={meta}
        cacheKey="runtime"
        update={update}
      />
      <DiskForm items={items} update={update} />
      <RuntimeForm title={t('settings.sched.alerts')} desc={t('settings.sched.alerts_desc')} items={items} keys={['silence_alert']} meta={meta} cacheKey="runtime" update={update} />
      <RuntimeForm
        title={t('settings.sched.snapshot')}
        desc={t('settings.sched.snapshot_desc')}
        items={items}
        keys={['snapshot_max_file_mb', 'snapshot_max_total_mb']}
        meta={meta}
        cacheKey="runtime"
        update={update}
        crossCheck={(v, changed) =>
          (changed.snapshot_max_file_mb || changed.snapshot_max_total_mb) && v.snapshot_max_file_mb > v.snapshot_max_total_mb ? { snapshot_max_file_mb: t('settings.sched.err_snapshot_lines') } : {}
        }
      />
      {st.terminal_enabled && <RuntimeForm title={t('settings.sched.terminal')} items={items} keys={['terminal_session_lifetime']} meta={meta} cacheKey="runtime" update={update} />}
      {st.strict && <StrictForm strict={st.strict} />}
    </>
  )
}

function DiskForm({ items, update }: { items: RuntimeSettings; update: (patch: Parameters<typeof api.updateRuntime>[0]) => Promise<RuntimeSettings> }) {
  const host = useQuery({ queryKey: ['host-metrics'], queryFn: api.hostMetrics, refetchInterval: 10_000 })
  const data = host.data?.disks?.find((d) => d.data && !d.error)
  return (
    <RuntimeForm
      title={t('settings.sched.disk')}
      desc={t('settings.sched.disk_desc')}
      items={items}
      keys={['disk_min_gb', 'disk_warn_gb']}
      meta={rowMeta()}
      cacheKey="runtime"
      update={update}
      crossCheck={(v, changed) =>
        (changed.disk_min_gb || changed.disk_warn_gb) && v.disk_min_gb > 0 && v.disk_warn_gb > 0 && v.disk_min_gb >= v.disk_warn_gb ? { disk_min_gb: t('settings.sched.err_disk_lines') } : {}
      }
      extra={{
        disk_min_gb: (_, v) =>
          data && (
            <p className="mt-1.5 flex items-start gap-1.5 text-xs leading-relaxed text-fg-3">
              {v.disk_min_gb > 0 && data.available_bytes < v.disk_min_gb * 1024 ** 3 && <TriangleAlert className="mt-px size-3.5 shrink-0 text-warn" aria-hidden />}
              <span>
                {t('settings.sched.disk_now', { free: gib(data.available_bytes), total: gib(data.total_bytes) })}
                {v.disk_min_gb > 0 && data.available_bytes < v.disk_min_gb * 1024 ** 3 && t('settings.sched.disk_below')}
              </span>
            </p>
          ),
      }}
    />
  )
}
