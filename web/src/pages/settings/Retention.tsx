import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'
import { bytes, fmtTime, type RetentionKey, type RetentionPreview, type RetentionPreviewItem, type RetentionSettings, type Settings, type SettingsPatch } from '@danlu/core'
import { Button, cx, Dialog, ErrorBox, hitArea } from '../../components/ui'
import { api } from '../../lib/api'
import { t } from '../../lib/i18n'
import { RuntimeForm, type RowMeta } from './RuntimeForm'
import { KV, Section } from './shared'

/** 行标签和提示按当前语言取值：是函数不是模块级常量表，切换语言后重渲染即生效 */
function rowMeta(): Record<RetentionKey, RowMeta> {
  return {
    keep_days: {
      label: t('settings.retention.keep_days'),
      hint: t('settings.retention.keep_days_hint'),
    },
    max_total_gb: {
      label: t('settings.retention.max_total'),
      hint: t('settings.retention.max_total_hint'),
    },
  }
}

/** 收紧了策略才需要预览：打开了某项，或者调低了 */
function tightens(items: RetentionSettings, patch: SettingsPatch<RetentionKey>) {
  return (Object.keys(patch) as RetentionKey[]).some((k) => {
    const next = patch[k] ?? items[k].default
    const cur = items[k].value
    return next !== 0 && (cur === 0 || next < cur)
  })
}

// 清理原因按 reason_code 翻译（静态映射，不拼键）；旧版服务器没有码，回退 reason 原文
const reasonKeys: Record<string, Parameters<typeof t>[0]> = {
  resume: 'settings.retention.reason_resume',
  age: 'settings.retention.reason_age',
  size: 'settings.retention.reason_size',
}

function reasonText(e: RetentionPreviewItem): string {
  const key = e.reason_code ? reasonKeys[e.reason_code] : undefined
  return key ? t(key) : e.reason
}

export function Retention({ st }: { st: Settings }) {
  const [confirm, setConfirm] = useState<{ preview: RetentionPreview; save: () => void } | null>(null)
  const preview = useMutation({ mutationFn: (patch: SettingsPatch<RetentionKey>) => api.previewRetention(patch) })
  if (!st.retention) return <Section title={t('settings.group_retention')}>{t('settings.old_server')}</Section>
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
        title={t('settings.retention.title')}
        desc={t('settings.retention.desc')}
        items={items}
        keys={['keep_days', 'max_total_gb']}
        meta={rowMeta()}
        cacheKey="retention"
        update={(patch) => api.updateRetention(patch).then((r) => r.retention)}
        intercept={intercept}
        busy={preview.isPending || confirm !== null}
        savedText={t('settings.retention.saved')}
      >
        {preview.error && (
          <div className="px-4 py-3 sm:px-5">
            <ErrorBox error={preview.error} />
          </div>
        )}
      </RuntimeForm>

      <Section title={t('settings.retention.auto')} desc={t('settings.retention.auto_desc')}>
        <dl className="-my-2 divide-y divide-line">
          <KV
            k={t('settings.retention.last_prune')}
            v={
              st.prune
                ? t('settings.retention.last_prune_value', {
                    at: fmtTime(st.prune.at),
                    jobs: t('settings.retention.job_count', { n: st.prune.jobs }),
                    envs: t('settings.retention.env_count', { n: st.prune.envs }),
                    bytes: bytes(st.prune.bytes),
                  })
                : items.keep_days.value === 0 && items.max_total_gb.value === 0
                  ? t('settings.retention.all_off')
                  : t('settings.retention.never')
            }
          />
          {st.prune?.error && <KV k={t('settings.retention.error')} v={<span className="text-fail">{st.prune.error}</span>} />}
          <KV
            k={t('settings.retention.usage')}
            v={
              <Link to="/resources" className={cx(hitArea, 'text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg hover:decoration-fg')}>
                {t('settings.retention.usage_link')}
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
      title={t('settings.retention.confirm_title', { n: p.jobs })}
      onClose={onClose}
      footer={
        <>
          <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{t('common.cancel')}</Button>
          <Button
            variant="danger"
            onClick={(e) => {
              onConfirm()
              e.currentTarget.closest('dialog')?.close()
            }}
          >
            {t('settings.retention.confirm_save')}
          </Button>
        </>
      }
    >
      <p>
        {t('settings.retention.confirm_body', { bytes: bytes(p.bytes), total: bytes(p.total_bytes) })}
      </p>
      <ul className="divide-y divide-line rounded-md border border-line text-ui">
        {p.items.slice(0, 5).map((e) => (
          <li key={e.job_id} className="flex items-center gap-2 px-3 py-1.5">
            <span className="font-mono text-xs text-fg-3">#{e.job_id}</span>
            <span className="min-w-0 flex-1 truncate">{e.name}</span>
            <span className="shrink-0 text-xs text-fg-3">{reasonText(e)}</span>
            <span className="shrink-0 font-mono text-xs text-fg-3">{bytes(e.bytes)}</span>
          </li>
        ))}
        {p.jobs > 5 && <li className="px-3 py-1.5 text-fg-3">{t('settings.retention.more_jobs', { n: p.jobs - 5 })}</li>}
      </ul>
      {p.errors.length > 0 && <p className="text-xs leading-relaxed text-fg-3">{t('settings.retention.preview_errors', { n: p.errors.length, first: p.errors[0] })}</p>}
    </Dialog>
  )
}
