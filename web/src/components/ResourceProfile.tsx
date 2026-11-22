import { bytes, duration, jobDuration, type Job } from '@danlu/core'
import { t } from '../lib/i18n'

export function ProfileSummary({ job, showHint = false }: { job: Job; showHint?: boolean }) {
  const { gpu, host } = job.profile ?? {}
  if (!gpu && !host) return <span className="text-fg-3">{t('resources.profile_none')}</span>
  const covered = (seconds: number) => seconds < 1 ? t('resources.profile_under_1s') : duration(Math.floor(seconds))
  return (
    <div className="space-y-1 text-ui">
      {gpu && (
        <>
          <div>{t('resources.profile_gpu_avg', { value: Math.round(gpu.avg_util) })} <span className="text-fg-3">{t('resources.profile_gpu_cards', { n: gpu.cards.length })}</span></div>
          <div className="flex flex-wrap gap-x-3 gap-y-0.5">
            <span>{t('resources.profile_vram_peak')}</span>
            {gpu.cards.map((card) => (
              <span key={card.index} className="whitespace-nowrap text-fg-2">
                GPU {card.index} · {bytes(card.peak_mem_mb * 2 ** 20)} / {bytes(card.total_mem_mb * 2 ** 20)}
              </span>
            ))}
          </div>
          <div className="text-xs text-fg-3">{t('resources.profile_gpu_samples', { n: gpu.samples, covered: covered(gpu.covered_seconds) })}</div>
        </>
      )}
      {host && (
        <>
          <div>{t('resources.profile_host_peak', { value: bytes(host.peak_rss_bytes) })}</div>
          <div className="text-xs text-fg-3">{t('resources.profile_host_samples', { n: host.samples, covered: covered(host.covered_seconds) })}</div>
        </>
      )}
      {showHint && gpu && (jobDuration(job) ?? 0) >= 600 && gpu.avg_util < 40 && (
        <div className="text-xs text-fg-3">{t('resources.profile_low_util')}</div>
      )}
    </div>
  )
}
