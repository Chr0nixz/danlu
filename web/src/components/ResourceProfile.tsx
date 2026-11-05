import { bytes, duration, jobDuration, type Job } from '@danlu/core'

export function ProfileSummary({ job, showHint = false }: { job: Job; showHint?: boolean }) {
  const { gpu, host } = job.profile ?? {}
  if (!gpu && !host) return <span className="text-fg-3">无记录</span>
  const covered = (seconds: number) => seconds < 1 ? '不足 1 秒' : duration(Math.floor(seconds))
  return (
    <div className="space-y-1 text-ui">
      {gpu && (
        <>
          <div>GPU 平均利用率 {Math.round(gpu.avg_util)}% <span className="text-fg-3">（{gpu.cards.length} 卡）</span></div>
          <div className="flex flex-wrap gap-x-3 gap-y-0.5">
            <span>显存峰值</span>
            {gpu.cards.map((card) => (
              <span key={card.index} className="whitespace-nowrap text-fg-2">
                GPU {card.index} · {bytes(card.peak_mem_mb * 2 ** 20)} / {bytes(card.total_mem_mb * 2 ** 20)}
              </span>
            ))}
          </div>
          <div className="text-xs text-fg-3">GPU 采样 {gpu.samples} 次 · 覆盖 {covered(gpu.covered_seconds)}</div>
        </>
      )}
      {host && (
        <>
          <div>主机内存峰值 {bytes(host.peak_rss_bytes)}</div>
          <div className="text-xs text-fg-3">主机采样 {host.samples} 次 · 覆盖 {covered(host.covered_seconds)}</div>
        </>
      )}
      {showHint && gpu && (jobDuration(job) ?? 0) >= 600 && gpu.avg_util < 40 && (
        <div className="text-xs text-fg-3">GPU 利用率偏低，可能卡在数据加载或 CPU 预处理</div>
      )}
    </div>
  )
}
