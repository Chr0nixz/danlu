export interface TerminalSocketHandlers {
  onOpen(): void
  onData(data: Uint8Array): void
  /** 后面的输出从会话输出的这个字节偏移开始（首次连接，或中间的输出已被服务器缓冲覆盖） */
  onOffset(offset: number): void
  onClose(event: CloseEvent): void
}

export interface TerminalSocket {
  send(data: Uint8Array): boolean
  resize(cols: number, rows: number): void
  close(): void
}

/** 会话被另一个页面接走时服务器用的关闭码 */
export const TERMINAL_REPLACED = 4001

/** offset 是已经写进终端的输出末尾，服务器只补发之后的部分；首次连接传 null，补发整个缓冲。 */
export function connectTerminal(id: string, offset: number | null, handlers: TerminalSocketHandlers): TerminalSocket {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const query = offset === null ? '' : `?offset=${offset}`
  const ws = new WebSocket(`${scheme}//${location.host}/api/terminal/${encodeURIComponent(id)}/ws${query}`)
  ws.binaryType = 'arraybuffer'
  ws.onopen = handlers.onOpen
  ws.onmessage = (event) => {
    if (event.data instanceof ArrayBuffer) {
      handlers.onData(new Uint8Array(event.data))
      return
    }
    try {
      const msg = JSON.parse(event.data)
      if (msg?.type === 'offset' && Number.isSafeInteger(msg.offset)) handlers.onOffset(msg.offset)
    } catch {
      // 未知的控制消息直接忽略
    }
  }
  ws.onclose = handlers.onClose
  ws.onerror = () => ws.close()
  return {
    send(data) {
      if (ws.readyState !== WebSocket.OPEN) return false
      const owned = new Uint8Array(new ArrayBuffer(data.byteLength))
      owned.set(data)
      ws.send(owned.buffer)
      return true
    },
    resize(cols, rows) {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    },
    close() {
      ws.close()
    },
  }
}
