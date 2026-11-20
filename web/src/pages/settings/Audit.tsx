import { useInfiniteQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { fmtTime } from '@danlu/core'
import { Button, cx, ErrorBox, hitArea, Skeleton } from '../../components/ui'
import { api } from '../../lib/api'
import { actionLabel, Rows, Section } from './shared'

const PAGE = 50 // 和服务器 /api/audit 每页条数一致

export function Audit() {
  const q = useInfiniteQuery({
    queryKey: ['audit'],
    queryFn: ({ pageParam }) => api.audit(pageParam),
    initialPageParam: 1,
    getNextPageParam: (last, pages) => (last.entries.length < PAGE ? undefined : pages.length + 1),
  })
  // 按偏移分页：翻页之间有新记录时，下一页开头会和已加载的重复，按 id 去掉
  const seen = new Set<number>()
  const entries = (q.data?.pages.flatMap((p) => p.entries) ?? []).filter((e) => !seen.has(e.id) && !!seen.add(e.id))
  return (
    <Section
      title="操作记录"
      desc="登录、任务操作和设置改动，最近的在前。"
      footer={
        q.hasNextPage ? (
          <Button size="sm" className="ml-auto" loading={q.isFetchingNextPage} onClick={() => q.fetchNextPage()}>
            加载更多
          </Button>
        ) : undefined
      }
    >
      {q.isLoading ? (
        <Skeleton className="h-24 w-full" />
      ) : q.error ? (
        <ErrorBox error={q.error} />
      ) : entries.length === 0 ? (
        <p className="text-ui text-fg-3">还没有记录。</p>
      ) : (
        <Rows>
          {entries.map((e) => (
            <div key={e.id} className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5 px-4 py-2 text-ui sm:grid-cols-[140px_112px_1fr_auto] sm:px-5">
              <span className="text-fg-3">{fmtTime(e.ts).slice(5)}</span>
              <span className="order-first font-mono text-xs text-fg-3 sm:order-last sm:self-center">{e.ip}</span>
              <span className={e.action === 'login_fail' || e.action === 'login_locked' ? 'font-medium text-fail' : 'font-medium'}>{actionLabel[e.action] ?? e.action}</span>
              {/* 设置改动的说明可能很长，手机上折行显示全；桌面上一行放不下时悬停看全文 */}
              <span className="col-span-2 min-w-0 text-fg-3 max-sm:break-all sm:col-span-1 sm:truncate" title={e.detail || undefined}>
                <AuditDetail text={e.detail} />
              </span>
            </div>
          ))}
        </Rows>
      )}
    </Section>
  )
}

/** 操作记录的说明里，开头的"#7"链接到那个任务 */
function AuditDetail({ text }: { text: string }) {
  const m = text.match(/^#(\d+)(.*)$/)
  if (!m) return <>{text}</>
  return (
    <>
      <Link to={`/jobs/${m[1]}`} className={cx(hitArea, 'text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg hover:decoration-fg')}>
        #{m[1]}
      </Link>
      {m[2]}
    </>
  )
}
