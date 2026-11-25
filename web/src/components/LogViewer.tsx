import { apiErrorText, LogRenderer, useJobLog } from '@danlu/core'
import { useMutation } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, ChevronLeft, ChevronRight, Download, Search, WrapText } from 'lucide-react'
import { useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from 'react'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { usePrefs } from '../lib/prefs'
import { LogSearchDialog } from './LogSearchDialog'
import { cx, ErrorBox, Spinner } from './ui'

// 按钮 28px 高，点击区上下撑满 40px 高的工具栏。手机上工具栏 44px 高，按钮 36px 高、至少 40px 宽，点击区撑满工具栏
const toolbarButton =
  'relative inline-flex h-7 shrink-0 items-center justify-center gap-1.5 rounded px-2 text-xs text-[#a1a1a1] transition-colors duration-150 before:absolute before:inset-x-0 before:-inset-y-1.5 hover:bg-white/10 hover:text-white disabled:opacity-50 max-sm:h-9 max-sm:min-w-10 max-sm:before:-inset-y-1 [&_svg]:size-3.5'

export function LogViewer({ jobId, offset, onJump }: { jobId: number; offset?: number; onJump(offset?: number): void }) {
  const [searchOpen, setSearchOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const box = useRef<HTMLDivElement>(null)
  const search = useMutation({ mutationFn: (term: string) => api.searchLog(jobId, term) })
  const matches = search.data?.matches ?? []
  const selected = matches.findIndex((match) => match.offset === offset)
  const jump = (index: number) => {
    const match = matches[index]
    if (match) {
      if (match.offset === offset && box.current) box.current.scrollTop = 0
      onJump(match.offset)
      setSearchOpen(false)
    }
  }
  return (
    <>
      <LogOutput key={offset ?? 'tail'} jobId={jobId} offset={offset} box={box} onTail={() => onJump()} compact={selected >= 0} tools={
        <>
          <button type="button" className={toolbarButton} aria-label={t('job.log_search')} title={t('job.log_search')} onClick={() => setSearchOpen(true)}>
            <Search aria-hidden />
            <span className={selected >= 0 ? 'max-sm:hidden' : undefined}>{t('job.search')}</span>
          </button>
          {selected >= 0 && (
            <div className="flex items-center" aria-label={t('job.log_match_position', { current: selected + 1, total: matches.length })}>
              <button type="button" className={toolbarButton} aria-label={t('job.log_prev_match')} title={t('job.log_prev_match')} disabled={selected === 0}
                onClick={() => jump(selected - 1)}><ChevronLeft aria-hidden /></button>
              <button type="button" className={toolbarButton} aria-label={t('job.log_next_match')} title={t('job.log_next_match')} disabled={selected + 1 >= matches.length}
                onClick={() => jump(selected + 1)}><ChevronRight aria-hidden /></button>
            </div>
          )}
        </>
      } />
      {searchOpen && (
        <LogSearchDialog keyword={keyword} onChange={setKeyword} term={search.variables} result={search.data}
          loading={search.isPending} error={search.error} onSearch={(term) => search.mutate(term)} onJump={jump} onClose={() => setSearchOpen(false)} />
      )}
    </>
  )
}

/** 先读日志末尾 256KB，再用 SSE 接收增量（见 @danlu/core 的 useJobLog） */
function LogOutput({ jobId, offset, box, onTail, tools, compact }: {
  jobId: number
  offset?: number
  box: RefObject<HTMLDivElement | null>
  onTail(): void
  tools: ReactNode
  /** 工具栏里多了"上一个 / 下一个命中"时，手机上放不下文字，只留图标 */
  compact: boolean
}) {
  const { raw, start, loading, error, ended, loadingOlder, olderError, loadOlder, exactStart, windowLimited } = useJobLog(api, jobId, { offset })
  const [follow, setFollow] = useState(offset === undefined)
  // 打开时换不换行在「设置 → 面板偏好」里改；这里随时切换，只管这一次。
  // 直接打开任务页时偏好可能还没到：到了之后，只要还没手动切换过就跟上
  const { prefs } = usePrefs()
  const [wrapState, setWrap] = useState<boolean | null>(null)
  const wrap = wrapState ?? prefs.log_wrap
  const pre = useRef<HTMLPreElement>(null)

  // 从文件中间开始读时，第一行是半截的，丢掉
  const text = useMemo(() => (start !== 0 && !exactStart ? raw.slice(raw.indexOf('\n') + 1) : raw), [raw, start, exactStart])

  // 增量渲染，不经过 React：每批新换行的内容是 pre 里单独的一块，最后一行单独一块原地重画。
  // 浏览器只排版新的这一块，前面几百 KB 不用重排（整段替换 256KB 要一百多毫秒，每秒两次）
  const view = useRef<{ el: HTMLPreElement; renderer: LogRenderer; tail: HTMLDivElement } | null>(null)
  useLayoutEffect(() => {
    const el = pre.current
    if (!el) return
    let v = view.current
    if (v?.el !== el) {
      v = view.current = { el, renderer: new LogRenderer(), tail: document.createElement('div') }
      el.replaceChildren(v.tail)
    }
    const { reset, lines, tail } = v.renderer.update(text)
    if (reset) el.replaceChildren(v.tail)
    // 块末尾的换行不会多出一个空行，复制出来的文字也和原文一样
    if (lines) v.tail.insertAdjacentHTML('beforebegin', `<div>${lines}</div>`)
    v.tail.innerHTML = tail
  }, [text])

  // 往前加载了一段时，保持正在看的那一行不动
  const prev = useRef({ start, height: 0 })
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    if (follow) el.scrollTop = el.scrollHeight
    else if (start >= 0 && start < prev.current.start) el.scrollTop += el.scrollHeight - prev.current.height
    prev.current = { start, height: el.scrollHeight }
  }, [text, follow, start])

  // 日志框从自己的位置铺到视口底部（见 index.css 的 .log-fit），上面的内容变高变矮时重新量。
  // 手机上首屏里剩不下 120px 时换成高的一档（data-tall）：底部导航 56px、页面下边距 16px、面板边线 1px
  const [top, setTop] = useState(0)
  const [cramped, setCramped] = useState(false)
  const card = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const measure = () => {
      const nextTop = Math.round(el.getBoundingClientRect().top + window.scrollY)
      setTop(nextTop)
      setCramped(window.innerWidth < 640 && window.innerHeight - nextTop - 73 < 120)
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(document.body)
    window.addEventListener('resize', measure)
    return () => {
      ro.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [])

  // 手机上定位浏览时日志框是高的一档，滚到让它占满屏幕的位置。放在这里而不是调用方：要等高度生效之后才滚得到
  useLayoutEffect(() => {
    if (offset === undefined || !matchMedia('(max-width: 639.98px)').matches) return
    const smooth = !matchMedia('(prefers-reduced-motion: reduce)').matches
    card.current?.scrollIntoView({ block: 'start', behavior: smooth ? 'smooth' : 'auto' })
  }, [offset])

  const onScroll = () => {
    const el = box.current
    if (!el || offset !== undefined) return
    setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
  }

  const tb = toolbarButton
  // 手机上搜索、换行、下载都写出文字（触屏上看不到悬停提示）；放不下时只留图标
  const label = compact ? 'max-sm:hidden' : undefined

  return (
    // scroll-mt：滚到这里时让出顶栏、标签页和间距，和详情页滚到标签页的位置一致
    <div ref={card} className="overflow-hidden rounded-lg border border-line bg-[#0b0b0b] max-sm:scroll-mt-[7.5rem]">
      <div className="flex h-10 items-center gap-1 border-b border-white/10 px-2 text-xs text-[#8f8f8f] max-sm:h-11 max-sm:gap-0.5">
        <span className="flex shrink-0 items-center gap-1.5 px-1.5">
          {offset !== undefined ? t('job.log_positioned') : !ended ? (
            <>
              <span className="size-1.5 animate-pulse rounded-full bg-[#3dd68c]" aria-hidden />
              {t('job.log_live')}
            </>
          ) : (
            <>
              <span className="size-1.5 rounded-full bg-[#5a5a5a]" />
              {t('job.log_ended')}
            </>
          )}
        </span>
        {start === -1 && <span className="truncate px-1.5">{t('job.log_window_hint')}</span>}
        {windowLimited && <span className="truncate px-1.5">{t('job.log_window_full')}</span>}
        <span className="ml-auto" />
        {tools}
        {start > 0 && !windowLimited && !!olderError && !loadingOlder && (
          // 只是往前读失败：日志照常接收，按钮还能再点
          <span role="alert" className="truncate px-1.5 text-[#ff7b72]" title={apiErrorText(olderError)}>
            {t('job.log_load_older_failed')}
          </span>
        )}
        {start > 0 && !windowLimited && (
          <button type="button" className={tb} aria-label={t('job.log_load_older')} title={t('job.log_load_older')} disabled={loadingOlder} onClick={loadOlder}>
            {loadingOlder ? <Spinner className="size-3.5" /> : <ArrowUp />}
            <span className="hidden sm:inline">{t('job.log_load_older')}</span>
          </button>
        )}
        <button type="button" className={cx(tb, wrap && 'bg-white/10 text-white')} aria-pressed={wrap} title={t('job.log_wrap_title')} onClick={() => setWrap(!wrap)}>
          <WrapText />
          <span className={label}>{t('job.log_wrap')}</span>
        </button>
        <a href={`/api/jobs/${jobId}/log/download`} className={tb} title={t('job.log_download_title')}>
          <Download />
          <span className={label}>{t('job.log_download')}</span>
        </a>
      </div>
      <div className="relative">
        <div
          ref={box}
          onScroll={onScroll}
          className="term log-fit overflow-auto px-3 py-2.5"
          data-tall={cramped || offset !== undefined || undefined}
          style={top ? ({ '--log-top': `${top}px` } as CSSProperties) : undefined}
        >
          {loading ? (
            <Spinner />
          ) : error ? (
            <ErrorBox error={error} term />
          ) : raw === '' ? (
            <span className="text-[#8f8f8f]">{ended ? t('job.log_empty') : t('job.log_waiting')}</span>
          ) : (
            <pre ref={pre} className={wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'whitespace-pre'} />
          )}
        </div>
        {!follow && (
          <button
            type="button"
            onClick={() => offset !== undefined ? onTail() : setFollow(true)}
            className="absolute bottom-3 right-4 inline-flex h-8 items-center gap-1 rounded-md bg-white px-3 text-xs font-medium text-black shadow-[0_4px_12px_rgb(0_0_0/0.4)] hover:bg-[#e6e6e6] max-sm:h-11"
          >
            <ArrowDown className="size-3.5" />
            {offset !== undefined ? t('job.log_back_to_tail') : t('job.log_back_to_bottom')}
          </button>
        )}
      </div>
    </div>
  )
}
