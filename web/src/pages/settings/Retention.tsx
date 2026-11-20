import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'
import { bytes, fmtTime, type RetentionKey, type RetentionPreview, type RetentionSettings, type Settings, type SettingsPatch } from '@danlu/core'
import { Button, cx, Dialog, ErrorBox, hitArea } from '../../components/ui'
import { api } from '../../lib/api'
import { RuntimeForm, type RowMeta } from './RuntimeForm'
import { KV, Section } from './shared'

const meta: Record<RetentionKey, RowMeta> = {
  keep_days: {
    label: '保留天数',
    hint: '任务结束超过这么多天后，清理它的日志、代码快照和运行目录。',
  },
  max_total_gb: {
    label: '总量上限',
    hint: '这些任务文件加起来超过上限时，从最早结束的任务开始清理，直到回到上限以内。',
  },
}

/** 收紧了策略才需要预览：打开了某项，或者调低了 */
function tightens(items: RetentionSettings, patch: SettingsPatch<RetentionKey>) {
  return (Object.keys(patch) as RetentionKey[]).some((k) => {
    const next = patch[k] ?? items[k].default
    const cur = items[k].value
    return next !== 0 && (cur === 0 || next < cur)
  })
}

export function Retention({ st }: { st: Settings }) {
  const [confirm, setConfirm] = useState<{ preview: RetentionPreview; save: () => void } | null>(null)
  const preview = useMutation({ mutationFn: (patch: SettingsPatch<RetentionKey>) => api.previewRetention(patch) })
  if (!st.retention) return <Section title="数据保留">这台服务器的版本较旧，不支持在面板上修改。</Section>
  const items = st.retention

  const intercept = (patch: SettingsPatch<RetentionKey>, save: () => void) => {
    if (!tightens(items, patch)) return save()
    preview.mutate(patch, {
      onSuccess: (p) => (p.jobs > 0 ? setConfirm({ preview: p, save }) : save()),
    })
  }

  return (
    <>
      <RuntimeForm
        title="保留策略"
        desc="只清理已结束任务的日志、代码快照和运行目录。任务记录、标星的任务、正在用快照重跑的源任务和训练产物（输出目录）都保留。"
        items={items}
        keys={['keep_days', 'max_total_gb']}
        meta={meta}
        cacheKey="retention"
        update={(patch) => api.updateRetention(patch).then((r) => r.retention)}
        intercept={intercept}
        busy={preview.isPending}
        savedText="已保存，正在后台按新策略清理"
      >
        {preview.error && (
          <div className="px-4 py-3 sm:px-5">
            <ErrorBox error={preview.error} />
          </div>
        )}
      </RuntimeForm>

      <Section title="自动清理" desc="服务每 6 小时按保留策略清理一次，在这里收紧策略后会马上清理一次。命令行用 danlu prune --dry-run 也能预览。">
        <dl className="-my-2 divide-y divide-line">
          <KV
            k="上次清理"
            v={
              st.prune
                ? `${fmtTime(st.prune.at)} · ${st.prune.jobs} 个任务、${st.prune.envs} 个环境文件，释放 ${bytes(st.prune.bytes)}`
                : items.keep_days.value === 0 && items.max_total_gb.value === 0
                  ? '保留策略都已关闭，不自动清理'
                  : '服务启动后还没有清理过'
            }
          />
          {st.prune?.error && <KV k="出错" v={<span className="text-fail">{st.prune.error}</span>} />}
          <KV
            k="当前占用"
            v={
              <Link to="/resources" className={cx(hitArea, 'text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg hover:decoration-fg')}>
                在资源页查看丹炉自身占用
              </Link>
            }
          />
        </dl>
      </Section>

      {confirm && <ConfirmPrune preview={confirm.preview} onConfirm={confirm.save} onClose={() => setConfirm(null)} />}
    </>
  )
}

function ConfirmPrune({ preview: p, onConfirm, onClose }: { preview: RetentionPreview; onConfirm: () => void; onClose: () => void }) {
  return (
    <Dialog
      title={`保存并清理 ${p.jobs} 个任务的文件？`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>取消</Button>
          <Button
            variant="danger"
            onClick={(e) => {
              onConfirm()
              e.currentTarget.closest('dialog')?.close()
            }}
          >
            保存并清理
          </Button>
        </>
      }
    >
      <p>
        按新的保留策略，会删除这些已结束任务的日志、代码快照和运行目录，约释放 {bytes(p.bytes)}（现在任务文件共 {bytes(p.total_bytes)}）。任务记录、标星任务和训练产物都保留，但删掉的日志和快照找不回来。
      </p>
      <ul className="divide-y divide-line rounded-md border border-line text-ui">
        {p.items.slice(0, 5).map((e) => (
          <li key={e.job_id} className="flex items-center gap-2 px-3 py-1.5">
            <span className="font-mono text-xs text-fg-3">#{e.job_id}</span>
            <span className="min-w-0 flex-1 truncate">{e.name}</span>
            <span className="shrink-0 font-mono text-xs text-fg-3">{bytes(e.bytes)}</span>
          </li>
        ))}
        {p.jobs > 5 && <li className="px-3 py-1.5 text-fg-3">还有 {p.jobs - 5} 个任务</li>}
      </ul>
      {p.errors.length > 0 && <p className="text-xs leading-relaxed text-fg-3">另有 {p.errors.length} 个任务统计时出错，这次不会清理它们：{p.errors[0]}</p>}
    </Dialog>
  )
}
