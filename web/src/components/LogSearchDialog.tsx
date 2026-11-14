import { type LogSearchResult } from '@danlu/core'
import { Button, Dialog, ErrorBox, Input } from './ui'

const shortcuts = ['error', 'warning', 'nan', 'Traceback', 'val']

export function LogSearchDialog({ keyword, onChange, term, result, loading, error, onSearch, onJump, onClose }: {
  keyword: string
  onChange(keyword: string): void
  term?: string
  result?: LogSearchResult
  loading: boolean
  error: unknown
  onSearch(term: string): void
  onJump(index: number): void
  onClose(): void
}) {
  const valid = keyword.trim() !== '' && Array.from(keyword.trim()).length <= 256
  const pattern = term ? new RegExp(`(${term.replace(/[.*+?^\${}()|[\]\\]/g, '\\$&')})`, 'giu') : null
  return (
    <Dialog title="搜索日志" onClose={onClose} wide className="max-sm:h-[50dvh] max-sm:open:flex max-sm:flex-col"
      footer={<Button size="sm" onClick={onClose}>关闭</Button>}>
      <form className="mt-2 flex gap-2" onSubmit={(e) => { e.preventDefault(); if (valid && !loading) onSearch(keyword.trim()) }}>
        {/* 不用浏览器自带的蓝色清除图标 */}
        <Input type="search" className="min-w-0 flex-1 max-sm:text-base [&::-webkit-search-cancel-button]:appearance-none" aria-label="日志关键词" autoFocus
          placeholder="输入关键词" value={keyword} onChange={(e) => onChange(e.target.value)} />
        <Button type="submit" variant="primary" disabled={!valid} loading={loading}>搜索</Button>
      </form>
      <div className="flex flex-wrap gap-1.5" aria-label="快捷关键词">
        {shortcuts.map((word) => (
          // 半屏面板里高度紧，快捷词在手机上是 36px，和最近用过的工作目录小标签一样
          <Button size="sm" key={word} className="max-sm:h-9!" disabled={loading} onClick={() => { onChange(word); onSearch(word) }}>{word}</Button>
        ))}
      </div>
      <ErrorBox error={error} />
      {result && (
        <>
          <div className="text-xs text-fg-3" role="status">
            {result.matches.length ? `${result.matches.length} 条匹配` : '未找到匹配行'}
            {result.truncated && (result.size > 0
              ? ` · 已扫描前 ${Math.floor(100 * result.scanned / result.size)}%`
              : ' · 扫描超时')}
          </div>
          {result.matches.length > 0 && (
            <ul className="max-h-[min(38dvh,24rem)] divide-y divide-line overflow-y-auto rounded-md border border-line max-sm:max-h-[19dvh]">
              {result.matches.map((match, i) => (
                <li key={match.offset}>
                  <button type="button" onClick={() => onJump(i)}
                    className="flex min-h-9 w-full gap-2 px-2.5 py-2 text-left hover:bg-hover max-sm:min-h-11">
                    <span className="shrink-0 text-xs text-fg-3">{i + 1}</span>
                    <span className="min-w-0 whitespace-pre-wrap font-mono text-xs [overflow-wrap:anywhere]">
                      {(pattern ? match.text.split(pattern) : [match.text]).map((part, n) => n % 2 === 1
                        ? <mark key={n} className="rounded bg-inv/10 text-fg underline">{part}</mark>
                        : part)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </Dialog>
  )
}
