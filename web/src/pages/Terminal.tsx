import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Minus, Plus, Send, Terminal as TerminalIcon, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { Navigate, useSearchParams } from 'react-router'
import { type TerminalSession } from '@danlu/core'
import { Button, Card, cx, Dialog, ErrorBox, Spinner, Textarea } from '../components/ui'
import { api } from '../lib/api'
import { t } from '../lib/i18n'
import { saveTerminalFontSize, storedTerminalFontSize } from '../lib/localPrefs'
import { connectTerminal, TERMINAL_REPLACED, TERMINAL_UNAUTHORIZED, type TerminalSocket } from '../lib/terminal'

const encoder = new TextEncoder()
const emptySessions: TerminalSession[] = []

export function Terminal() {
  const queryClient = useQueryClient()
  const [params, setParams] = useSearchParams()
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, staleTime: 60_000 })
  const sessions = useQuery({
    queryKey: ['terminal-sessions'],
    queryFn: api.terminalSessions,
    enabled: settings.data?.terminal_enabled === true,
    refetchInterval: 3000,
  })
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [fontSize, setFontSize] = useState(storedTerminalFontSize)
  const [openError, setOpenError] = useState<unknown>(null)
  // 关闭终端会结束里面的进程，先确认
  const [closing, setClosing] = useState<TerminalSession | null>(null)
  const open = useMutation({
    mutationFn: (jobId?: number) => api.openTerminal(jobId),
    onSuccess: (session) => {
      queryClient.setQueryData<{ sessions: TerminalSession[] }>(['terminal-sessions'], (old) => ({
        sessions: [...(old?.sessions ?? []).filter((s) => s.id !== session.id), session],
      }))
      setSelectedId(session.id)
      setOpenError(null)
    },
    onError: (error) => setOpenError(error),
  })
  const close = useMutation({
    mutationFn: (id: string) => api.closeTerminal(id),
    onSuccess: (_, id) => {
      queryClient.setQueryData<{ sessions: TerminalSession[] }>(['terminal-sessions'], (old) => ({
        sessions: (old?.sessions ?? []).filter((s) => s.id !== id),
      }))
      setSelectedId((current) => current === id ? null : current)
      setClosing(null)
    },
  })
  const jobParam = params.get('job_id')
  const jobOpenStarted = useRef(false)
  useEffect(() => {
    if (!jobParam || settings.data?.terminal_enabled !== true || jobOpenStarted.current) return
    jobOpenStarted.current = true
    const id = Number(jobParam)
    if (Number.isSafeInteger(id) && id > 0) {
      open.mutate(id, { onSuccess: () => setParams({}, { replace: true }) })
    } else {
      setParams({}, { replace: true })
    }
  }, [jobParam, open, setParams, settings.data?.terminal_enabled])

  const items = sessions.data?.sessions ?? emptySessions
  const selected = items.find((s) => s.id === selectedId)
  useEffect(() => {
    if (!selectedId && items.length) setSelectedId(items[0].id)
    else if (selectedId && !items.some((s) => s.id === selectedId)) setSelectedId(items[0]?.id ?? null)
  }, [items, selectedId])
  useEffect(() => saveTerminalFontSize(fontSize), [fontSize])

  const maxed = items.length >= 3
  const requestOpen = (jobId?: number) => {
    setOpenError(null)
    open.mutate(jobId)
  }

  if (settings.data && settings.data.terminal_enabled !== true) return <Navigate to="/" replace />
  if (settings.error) return <ErrorBox error={settings.error} />

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold">{t('terminal.title')}</h1>
        <span className="text-xs text-fg-3">{items.length}/3</span>
        <Button size="sm" variant="secondary" className="ml-auto" disabled={maxed || open.isPending || settings.data?.terminal_enabled !== true} loading={open.isPending} onClick={() => requestOpen()}>
          <Plus />
          {t('terminal.new')}
        </Button>
      </div>

      {openError !== null && <ErrorBox error={openError} />}
      {sessions.error && <ErrorBox error={sessions.error} />}

      {items.length > 0 && (
        // 和页内标签页同一套键盘约定：只有选中的会话能 Tab 到，左右方向键和 Home / End 切换
        <div
          className="flex min-w-0 items-center gap-1 overflow-x-auto border-b border-line"
          role="tablist"
          aria-label={t('terminal.sessions_aria')}
          onKeyDown={(e) => {
            const i = items.findIndex((s) => s.id === selectedId)
            const to = ({ ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: items.length - 1 } as Record<string, number>)[e.key]
            if (to === undefined) return
            e.preventDefault()
            const next = items[(to + items.length) % items.length]
            setSelectedId(next.id)
            document.getElementById(`terminal-tab-${next.id}`)?.focus()
          }}
        >
          {items.map((session) => {
            const active = selectedId === session.id
            const label = session.workdir.split('/').filter(Boolean).at(-1) || '/'
            return (
              <button
                key={session.id}
                type="button"
                role="tab"
                id={`terminal-tab-${session.id}`}
                aria-selected={active}
                aria-controls={active ? 'terminal-panel' : undefined}
                tabIndex={active ? 0 : -1}
                title={session.workdir}
                onClick={() => setSelectedId(session.id)}
                className={cx('flex h-10 max-w-56 shrink-0 items-center gap-2 border-b-2 px-3 text-ui focus-visible:-outline-offset-2 max-sm:h-11', active ? 'border-fg text-fg' : 'border-transparent text-fg-3 hover:text-fg')}
              >
                <TerminalIcon className="size-4 shrink-0" aria-hidden />
                <span className="truncate">{label}</span>
                <span className="font-mono text-[11px] text-fg-3">{new Date(session.opened_at * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
              </button>
            )
          })}
        </div>
      )}

      {sessions.isLoading || settings.isLoading ? (
        <div className="grid min-h-64 place-items-center"><Spinner /></div>
      ) : selected ? (
        <div role="tabpanel" id="terminal-panel" aria-labelledby={`terminal-tab-${selected.id}`}>
          <TerminalSessionPanel
            key={selected.id}
            session={selected}
            fontSize={fontSize}
            onFontSize={setFontSize}
            onClose={() => {
              close.reset()
              setClosing(selected)
            }}
            closing={close.isPending}
          />
        </div>
      ) : (
        <Card className="flex min-h-64 flex-col items-center justify-center gap-3 border-dashed px-5 py-10 text-center">
          <TerminalIcon className="size-5 text-fg-3" aria-hidden />
          <p className="text-ui font-medium">{t('terminal.empty')}</p>
          <Button size="sm" variant="primary" disabled={open.isPending} loading={open.isPending} onClick={() => requestOpen()}>
            <Plus />
            {t('terminal.open')}
          </Button>
        </Card>
      )}
      {closing && (
        <Dialog
          title={t('terminal.close_title')}
          onClose={() => setClosing(null)}
          footer={
            <>
              <Button onClick={(e) => e.currentTarget.closest('dialog')?.close()}>{t('terminal.keep')}</Button>
              <Button variant="danger" loading={close.isPending} onClick={() => close.mutate(closing.id)}>
                {t('terminal.close')}
              </Button>
            </>
          }
        >
          <p className="break-all font-mono text-[12.5px] text-fg">{closing.workdir}</p>
          <p>{t('terminal.close_body')}</p>
          <ErrorBox error={close.error} />
        </Dialog>
      )}
    </div>
  )
}

function TerminalSessionPanel({
  session,
  fontSize,
  onFontSize,
  onClose,
  closing,
}: {
  session: TerminalSession
  fontSize: number
  onFontSize(value: number): void
  onClose(): void
  closing: boolean
}) {
  const host = useRef<HTMLDivElement>(null)
  const terminal = useRef<XTerminal | null>(null)
  const fitAddon = useRef<FitAddon | null>(null)
  const socket = useRef<TerminalSocket | null>(null)
  const reconnect = useRef<() => void>(() => {})
  const [status, setStatus] = useState<'connecting' | 'connected' | 'reconnecting' | 'replaced' | 'closed'>('connecting')
  const [draft, setDraft] = useState('')
  const [ctrl, setCtrl] = useState(false)
  const [alt, setAlt] = useState(false)
  const history = useRef<string[]>([])
  const historyIndex = useRef(-1)
  const [sendError, setSendError] = useState('')
  const draftInput = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    const el = host.current
    if (!el) return
    const term = new XTerminal({
      convertEol: false,
      cursorBlink: true,
      fontFamily: 'ui-monospace, "SFMono-Regular", "Cascadia Mono", "JetBrains Mono", Menlo, Consolas, "Liberation Mono", "Roboto Mono", "Noto Sans Mono CJK SC", monospace',
      fontSize,
      scrollback: 5000,
      theme: { background: '#0b0b0b', foreground: '#d9d9d9', cursor: '#d9d9d9', selectionBackground: '#ffffff38' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    fitAddon.current = fit
    term.open(el)
    terminal.current = term
    let retry: ReturnType<typeof setTimeout> | undefined
    let attempt = 0
    let disposed = false
    let ended = false
    let hiddenAt = 0
    // 已写进终端的输出末尾（会话输出里的字节偏移）。重连时交给服务器，只补发没收到的部分
    let received: number | null = null

    const sendResize = () => {
      try {
        fit.fit()
        socket.current?.resize(term.cols, term.rows)
      } catch {
        // 路由切换时终端容器可能短暂没有尺寸
      }
    }
    const start = () => {
      if (disposed || ended || socket.current || document.visibilityState === 'hidden' || !navigator.onLine) return
      if (retry) clearTimeout(retry)
      retry = undefined
      setStatus(attempt === 0 ? 'connecting' : 'reconnecting')
      const current = connectTerminal(session.id, received, {
        onOpen() {
          attempt = 0
          setStatus('connected')
          sendResize()
        },
        onOffset(offset) {
          if (socket.current === current) received = offset
        },
        onData(data) {
          if (socket.current !== current) return
          received = (received ?? 0) + data.length
          term.write(data)
        },
        onClose(event) {
          // 主动换掉或关掉的连接不再处理
          if (socket.current !== current) return
          socket.current = null
          if (disposed) return
          if (event.code === 1000 && event.reason === 'session ended') {
            ended = true
            setStatus('closed')
            return
          }
          if (event.code === TERMINAL_REPLACED) {
            setStatus('replaced')
            return
          }
          if (event.code === TERMINAL_UNAUTHORIZED) {
            // 和接口返回 401 一样回登录页，登录后回到这里
            ended = true
            location.href = `/login?next=${encodeURIComponent(location.pathname + location.search)}`
            return
          }
          setStatus('reconnecting')
          const delay = Math.min(15_000, 500 * 2 ** Math.min(attempt++, 5))
          retry = setTimeout(start, delay)
        },
      })
      socket.current = current
    }
    // 手机切后台、锁屏或换网络后，旧连接可能已经断了，浏览器却还当它开着：关掉重连，服务器只补发没收到的输出
    const restart = () => {
      const old = socket.current
      socket.current = null
      old?.close()
      attempt = 0
      start()
    }
    reconnect.current = restart
    term.onData((data) => {
      if (!socket.current?.send(encoder.encode(data))) start()
    })
    const observer = new ResizeObserver(sendResize)
    observer.observe(el)
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        hiddenAt = Date.now()
      } else if (!socket.current) {
        start()
      } else if (Date.now() - hiddenAt >= 5_000) {
        restart()
      }
    }
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', restart)
    start()

    return () => {
      disposed = true
      if (retry) clearTimeout(retry)
      reconnect.current = () => {}
      document.removeEventListener('visibilitychange', onVisibility)
      window.removeEventListener('online', restart)
      observer.disconnect()
      socket.current?.close()
      socket.current = null
      fitAddon.current = null
      term.dispose()
      terminal.current = null
    }
  }, [session.id])

  useEffect(() => {
    if (!terminal.current) return
    terminal.current.options.fontSize = fontSize
    try {
      fitAddon.current?.fit()
      socket.current?.resize(terminal.current.cols, terminal.current.rows)
    } catch {
      return
    }
  }, [fontSize])

  const send = useCallback((data: Uint8Array) => {
    if (!socket.current?.send(data)) {
      setSendError(t('terminal.send_wait'))
      return false
    }
    setSendError('')
    return true
  }, [])

  const submit = useCallback(() => {
    if (!draft) return
    if (!send(encoder.encode(`${draft}\r`))) return
    history.current = [...history.current.filter((line) => line !== draft), draft].slice(-100)
    historyIndex.current = -1
    setDraft('')
  }, [draft, send])

  const sendKey = (value: string, control = ctrl, escape = alt) => {
    let bytes: number[]
    if (value === 'Esc') bytes = [0x1b]
    else if (value === 'Tab') bytes = [0x09]
    else if (value === '↑') bytes = [0x1b, 0x5b, 0x41]
    else if (value === '↓') bytes = [0x1b, 0x5b, 0x42]
    else if (value === '→') bytes = [0x1b, 0x5b, 0x43]
    else if (value === '←') bytes = [0x1b, 0x5b, 0x44]
    else {
      bytes = [...encoder.encode(value)]
      if (control && bytes.length === 1) bytes = [bytes[0] & 0x1f]
      if (escape) bytes.unshift(0x1b)
    }
    send(new Uint8Array(bytes))
    if (control) setCtrl(false)
    if (escape) setAlt(false)
  }

  const onDraftKey = (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if ((ctrl || alt) && event.key.length === 1 && !event.nativeEvent.isComposing) {
      event.preventDefault()
      sendKey(event.key)
    } else if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      submit()
    } else if (event.key === 'ArrowUp' && history.current.length && event.currentTarget.selectionStart === 0 && event.currentTarget.selectionEnd === draft.length) {
      event.preventDefault()
      const next = historyIndex.current < 0 ? history.current.length - 1 : Math.max(0, historyIndex.current - 1)
      historyIndex.current = next
      setDraft(history.current[next])
    } else if (event.key === 'ArrowDown' && historyIndex.current >= 0) {
      event.preventDefault()
      const next = historyIndex.current + 1
      historyIndex.current = next >= history.current.length ? -1 : next
      setDraft(historyIndex.current < 0 ? '' : history.current[historyIndex.current])
    }
  }

  const statusKeys = { connecting: 'terminal.status_connecting', connected: 'terminal.status_connected', reconnecting: 'terminal.status_reconnecting', replaced: 'terminal.status_replaced', closed: 'terminal.status_closed' } as const
  const displayStatus = t(statusKeys[status])

  return (
    <div className="space-y-2.5">
      <div className="flex min-w-0 items-center gap-2 text-xs text-fg-3">
        <span className={cx('size-1.5 shrink-0 rounded-full bg-fg-3', status === 'reconnecting' && 'animate-pulse', status === 'closed' && 'bg-fg-4')} aria-hidden />
        <span className="shrink-0">{displayStatus}</span>
        {status === 'replaced' && (
          <button type="button" className="shrink-0 font-medium text-fg hover:underline" onClick={() => reconnect.current()}>
            {t('terminal.resume_here')}
          </button>
        )}
        <span className="min-w-0 truncate font-mono" title={session.workdir}>{session.workdir}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1">
          <button type="button" className="grid size-8 place-items-center rounded text-fg-3 hover:bg-hover hover:text-fg disabled:opacity-40 max-sm:size-11" title={t('terminal.font_smaller')} aria-label={t('terminal.font_smaller')} disabled={fontSize <= 11} onClick={() => onFontSize(Math.max(11, fontSize - 1))}><Minus className="size-4" /></button>
          <span className="w-6 text-center font-mono text-[11px] sm:w-8" title={t('terminal.font_size')}>{fontSize}</span>
          <button type="button" className="grid size-8 place-items-center rounded text-fg-3 hover:bg-hover hover:text-fg disabled:opacity-40 max-sm:size-11" title={t('terminal.font_larger')} aria-label={t('terminal.font_larger')} disabled={fontSize >= 20} onClick={() => onFontSize(Math.min(20, fontSize + 1))}><Plus className="size-4" /></button>
          {/* 关闭和调字号是两类操作，用分隔线隔开，免得想放大字号时点到关闭 */}
          <span className="mx-1 h-4 w-px shrink-0 bg-line-strong" aria-hidden />
          <Button size="icon-sm" variant="ghost" title={t('terminal.close_confirm_hint')} aria-label={t('terminal.close')} disabled={closing} loading={closing} onClick={onClose}><X /></Button>
        </span>
      </div>

      <div className="overflow-hidden rounded-lg border border-line bg-[#0b0b0b]">
        <div ref={host} className="terminal-fit w-full overflow-hidden p-2.5" aria-label={t('terminal.pane_aria')} />
      </div>

      <div className="space-y-2 sm:hidden">
        <div className="flex items-center gap-1 overflow-x-auto pb-0.5" aria-label={t('terminal.keys_aria')}>
          <KeyButton label="Esc" title="Escape" onClick={() => sendKey('Esc')} />
          <KeyButton label="Tab" title="Tab" onClick={() => sendKey('Tab')} />
          <KeyButton label="Ctrl" pressed={ctrl} title={t('terminal.ctrl_lock')} onClick={() => { setCtrl((v) => !v); requestAnimationFrame(() => draftInput.current?.focus({ preventScroll: true })) }} />
          <KeyButton label="Alt" pressed={alt} title={t('terminal.alt_lock')} onClick={() => { setAlt((v) => !v); requestAnimationFrame(() => draftInput.current?.focus({ preventScroll: true })) }} />
          <KeyButton label="↑" title={t('terminal.key_up')} onClick={() => sendKey('↑', false, false)} />
          <KeyButton label="↓" title={t('terminal.key_down')} onClick={() => sendKey('↓', false, false)} />
          <KeyButton label="←" title={t('terminal.key_left')} onClick={() => sendKey('←', false, false)} />
          <KeyButton label="→" title={t('terminal.key_right')} onClick={() => sendKey('→', false, false)} />
          {['|', '~', '/', '-'].map((key) => <KeyButton key={key} label={key} title={t('terminal.send_key', { key })} onClick={() => sendKey(key)} />)}
        </div>
        <div className="flex items-end gap-2">
          <Textarea
            ref={draftInput}
            value={draft}
            rows={1}
            aria-label={t('terminal.input_aria')}
            placeholder={t('terminal.input_placeholder')}
            className="max-h-28 min-h-11 flex-1 resize-y py-2.5 font-mono text-[13px]"
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={onDraftKey}
          />
          <Button size="icon" variant="primary" title={t('terminal.send')} aria-label={t('terminal.send')} onClick={submit} disabled={!draft}><Send /></Button>
        </div>
        {sendError && <p role="status" className="text-xs text-warn">{sendError}</p>}
      </div>
    </div>
  )
}

function KeyButton({ label, title, pressed, onClick }: { label: string; title: string; pressed?: boolean; onClick(): void }) {
  return (
    <button
      type="button"
      title={title}
      aria-label={title}
      aria-pressed={pressed}
      onClick={onClick}
      // 这一条只在手机上显示，每个键 44px
      className={cx('h-11 min-w-11 shrink-0 rounded border px-2 font-mono text-ui transition-colors', pressed ? 'border-line-strong bg-inv text-inv-fg' : 'border-line bg-surface text-fg-2 hover:bg-hover')}
    >
      {label}
    </button>
  )
}
