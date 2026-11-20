import { ApiError, createApi, type Transport } from '@danlu/core'

// 面板和服务器同源：直接 fetch，靠 gate cookie 和会话 cookie 认证
const transport: Transport = {
  async request(method, path, body) {
    let res: Response
    let text: string
    try {
      res = await fetch(path, {
        method,
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
        body,
      })
      text = await res.text()
    } catch {
      // 没连上服务器：status 0，和桌面端同一套约定；文案直白，别把浏览器的英文 TypeError 抛给用户
      throw new ApiError(0, '无法连接服务器（网络中断、服务正在重启，或证书不受信任）')
    }
    if (res.status === 401 && !location.pathname.startsWith('/login')) {
      // 登录后回到原来的页面
      location.href = `/login?next=${encodeURIComponent(location.pathname + location.search)}`
    }
    return { status: res.status, body: text }
  },
  stream(path, h) {
    const es = new EventSource(path)
    es.onmessage = (ev) => h.onEvent('message', ev.data)
    es.addEventListener('end', (ev) => h.onEvent('end', (ev as MessageEvent).data))
    // 浏览器的自动重连会带着旧的 offset，所以出错就关掉，由调用方自己重连
    es.onerror = () => {
      es.close()
      h.onError()
    }
    return () => es.close()
  },
}

export const api = createApi(transport, {
  // 安全入口 cookie 过期后，所有请求都会是空的 404
  emptyNotFound: '访问凭证已失效，请重新打开带安全入口的地址',
})
