import { useQuery } from '@tanstack/react-query'
import { TriangleAlert } from 'lucide-react'
import type { RuntimeKey, RuntimeSettings, Settings } from '@danlu/core'
import { api } from '../../lib/api'
import { RuntimeForm, type RowMeta } from './RuntimeForm'
import { Section } from './shared'

const meta: Record<RuntimeKey, RowMeta> = {
  fail_fast: {
    label: '快速失败暂停',
    hint: '任务启动后这么短时间内就失败，暂停队列，免得排在后面的任务接着失败。关闭后失败照常推送，队列不暂停。',
  },
  kill_grace: {
    label: '取消后强杀等待',
    hint: '取消任务时先发 SIGTERM，等这么久还没退出就 SIGKILL，给训练脚本留出保存检查点的时间。',
  },
  unknown_gpu_memory_mb: {
    label: '未知显存阈值',
    hint: '卡上对不上进程的显存超过这个值，这张卡就不分配给新任务。图形界面、驱动自身会占一点，阈值太低会误判。',
  },
  disk_min_gb: {
    label: '磁盘保护暂停线',
    hint: '数据目录所在分区剩余空间低于它时暂停队列，回到它的两倍以上自动恢复。',
  },
  disk_warn_gb: {
    label: '磁盘低余量提示',
    hint: '数据目录、输出目录或最近用过的工作目录所在分区低于它时，在首页提示并推送到手机。最多 1 分钟后按新值判断。',
  },
  silence_alert: {
    label: '日志静默提醒',
    hint: '运行中的任务超过这么久没有新输出，且明显长于它以往最长的输出间隔时提醒，多半是卡住了。',
  },
  snapshot_max_file_mb: {
    label: '单文件上限',
    hint: '超过的文件不进快照，数据集、权重这类大文件一般都会被跳过。',
  },
  snapshot_max_total_mb: {
    label: '总大小上限',
    hint: '快照加起来超过这个大小就截断，剩下的文件不再收录。',
  },
  terminal_session_lifetime: {
    label: '断开后保留',
    hint: '关掉终端页面后 shell 继续保留多久，回来能接着用。改短后，已断开超过新时长的终端会在 15 秒内关闭。',
  },
}

const gib = (bytes: number) => (bytes / 1024 ** 3).toFixed(1)

export function Scheduling({ st }: { st: Settings }) {
  if (!st.runtime) return <Section title="调度与告警">这台服务器的版本较旧，不支持在面板上修改。</Section>
  const items: RuntimeSettings = st.runtime
  const update = (patch: Parameters<typeof api.updateRuntime>[0]) => api.updateRuntime(patch).then((r) => r.runtime)
  return (
    <>
      <RuntimeForm
        title="任务调度"
        desc="保存后下一轮调度（1 秒内）生效，已经在跑的任务也按新值处理。"
        items={items}
        keys={['fail_fast', 'kill_grace', 'unknown_gpu_memory_mb']}
        meta={meta}
        cacheKey="runtime"
        update={update}
      />
      <DiskForm items={items} update={update} />
      <RuntimeForm title="告警" desc="触发时在首页提示，开了手机推送的也推送到手机。" items={items} keys={['silence_alert']} meta={meta} cacheKey="runtime" update={update} />
      <RuntimeForm
        title="代码快照"
        desc="每个任务启动时把工作目录里的代码打包留底，之后能查看和用快照重跑。新的上限从下一个启动的任务开始生效。"
        items={items}
        keys={['snapshot_max_file_mb', 'snapshot_max_total_mb']}
        meta={meta}
        cacheKey="runtime"
        update={update}
        crossCheck={(v, changed) =>
          (changed.snapshot_max_file_mb || changed.snapshot_max_total_mb) && v.snapshot_max_file_mb > v.snapshot_max_total_mb ? { snapshot_max_file_mb: '单文件上限不能超过总大小上限' } : {}
        }
      />
      {st.terminal_enabled && <RuntimeForm title="网页终端" items={items} keys={['terminal_session_lifetime']} meta={meta} cacheKey="runtime" update={update} />}
    </>
  )
}

function DiskForm({ items, update }: { items: RuntimeSettings; update: (patch: Parameters<typeof api.updateRuntime>[0]) => Promise<RuntimeSettings> }) {
  const host = useQuery({ queryKey: ['host-metrics'], queryFn: api.hostMetrics, refetchInterval: 10_000 })
  const data = host.data?.disks?.find((d) => d.data && !d.error)
  return (
    <RuntimeForm
      title="磁盘保护"
      desc="空间不够时，训练写检查点和丹炉写日志都会失败。暂停线要低于提示线。"
      items={items}
      keys={['disk_min_gb', 'disk_warn_gb']}
      meta={meta}
      cacheKey="runtime"
      update={update}
      crossCheck={(v, changed) =>
        (changed.disk_min_gb || changed.disk_warn_gb) && v.disk_min_gb > 0 && v.disk_warn_gb > 0 && v.disk_min_gb >= v.disk_warn_gb ? { disk_min_gb: '暂停线需要低于低余量提示线' } : {}
      }
      extra={{
        disk_min_gb: (_, v) =>
          data && (
            <p className="mt-1.5 flex items-start gap-1.5 text-xs leading-relaxed text-fg-3">
              {v.disk_min_gb > 0 && data.available_bytes < v.disk_min_gb * 1024 ** 3 && <TriangleAlert className="mt-px size-3.5 shrink-0 text-warn" aria-hidden />}
              <span>
                数据目录所在分区现在剩 {gib(data.available_bytes)} GiB / {gib(data.total_bytes)} GiB
                {v.disk_min_gb > 0 && data.available_bytes < v.disk_min_gb * 1024 ** 3 && '，低于这条暂停线，保存后队列会立即暂停'}
              </span>
            </p>
          ),
      }}
    />
  )
}
